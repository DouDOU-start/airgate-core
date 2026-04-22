package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/account"
	sdk "github.com/DouDOU-start/airgate-sdk"
)

// 状态机使用的默认窗口。
const (
	// rateLimitedDefault 上游没给 RetryAfter 时的兜底冷却。
	rateLimitedDefault = 60 * time.Second
	// rateLimitedMax OAuth 某些限流可能长达数天，设上限防止异常值。
	rateLimitedMax = 7 * 24 * time.Hour
	// degradedDefault 池账号抖动时的软降级窗口。
	degradedDefault = 60 * time.Second
	// degradedMax 池账号最长降级窗口。
	degradedMax = 10 * time.Minute
)

// Judgment forwarder 对一次调用的判决，交给状态机做状态转移。
type Judgment struct {
	Kind       sdk.OutcomeKind
	RetryAfter time.Duration
	Reason     string
	Duration   time.Duration // 仅用于日志 / 指标
	IsPool     bool          // 池账号（upstream_is_pool）走豁免路径
}

// StateMachine 账号状态机。所有状态转移必须通过 Apply 入口。
//
// 职责：
//   - 把 forwarder 的 Judgment 翻译成 DB 字段变更（state / state_until / error_msg / last_used_at）
//   - 关键转移（Active ↔ Disabled）通知上游清 route 缓存
//
// 只有确定性的账号级信号才动 state：AccountRateLimited / AccountDead。
// UpstreamTransient（SSE EOF、上游 5xx、连接抖动）是上游锅，不扣账号分——让 failover 兜底。
type StateMachine struct {
	db  *ent.Client
	rdb *redis.Client

	// onCriticalTransition Active ↔ Disabled 转移后的回调（由 Scheduler 注入）。
	// 用来清 route 缓存，让下次 SelectAccount 立刻看到新状态；
	// RateLimited / Degraded 这种"带 state_until 的临时状态"不走这里，由 TTL 兜底。
	onCriticalTransition func()
}

// NewStateMachine 构造状态机。
func NewStateMachine(db *ent.Client, rdb *redis.Client) *StateMachine {
	return &StateMachine{db: db, rdb: rdb}
}

// notifyCritical 发出关键状态变更事件。nil 回调时安静跳过。
func (sm *StateMachine) notifyCritical() {
	if sm.onCriticalTransition != nil {
		sm.onCriticalTransition()
	}
}

// Apply 把一次判决施加到账号状态机。只产生副作用，不返回要写给客户端的内容。
//
// 语义：
//
//	Success             → state=active，清 state_until，last_used_at=now
//	AccountRateLimited  → state=rate_limited，state_until=now+RetryAfter
//	AccountDead         → 非池：state=disabled；池：state=degraded（限时）
//	UpstreamTransient   → 非池：**不动状态**（上游抖动不扣账号分，靠 failover 切走就行）；池：state=degraded
//	ClientError / StreamAborted / Unknown → 不改状态（账号无辜）
func (sm *StateMachine) Apply(ctx context.Context, accountID int, j Judgment) {
	switch j.Kind {
	case sdk.OutcomeSuccess:
		sm.transitionActive(ctx, accountID)

	case sdk.OutcomeAccountRateLimited:
		dur := j.RetryAfter
		if dur <= 0 {
			dur = rateLimitedDefault
		}
		if dur > rateLimitedMax {
			dur = rateLimitedMax
		}
		until := time.Now().Add(dur)
		sm.transition(ctx, accountID, account.StateRateLimited, &until, j.Reason)

	case sdk.OutcomeAccountDead:
		if j.IsPool {
			sm.applyDegraded(ctx, accountID, j.Reason)
			return
		}
		sm.transition(ctx, accountID, account.StateDisabled, nil, j.Reason)

	case sdk.OutcomeUpstreamTransient:
		// 按定义，UpstreamTransient 是"上游侧瞬时故障"（SSE 提前断流、网络抖动、上游 5xx 等），
		// 账号本身没问题——不动 state，让 failover 切到下一账号就够了。
		//
		// 池账号（IsPool）保留软降级：pool 资源共享，一个账号抖起来可能拖垮整个 pool，
		// 短时间 degraded 让调度器优先选其它账号，到期自动恢复。
		if j.IsPool {
			sm.applyDegraded(ctx, accountID, j.Reason)
		}

	case sdk.OutcomeClientError, sdk.OutcomeStreamAborted, sdk.OutcomeUnknown:
		// 账号无辜，不改状态。
	}
}

// applyDegraded 池账号软降级。state_until 到期后调度器看到就恢复 active。
func (sm *StateMachine) applyDegraded(ctx context.Context, accountID int, reason string) {
	dur := degradedDefault
	if dur > degradedMax {
		dur = degradedMax
	}
	until := time.Now().Add(dur)
	sm.transition(ctx, accountID, account.StateDegraded, &until, reason)
}

// transitionActive 成功时回到 active：清 state_until、清 reason、清失败计数、更新 last_used_at。
func (sm *StateMachine) transitionActive(ctx context.Context, accountID int) {
	now := time.Now()
	dbCtx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	// 读当前状态决定是否触发关键转移通知：只有从非 active 回到 active 才需要
	// 清 route 缓存；已经是 active 的 Success 只是刷 last_used_at，不必清缓存。
	prevState := account.StateActive
	if existing, err := sm.db.Account.Get(dbCtx, accountID); err == nil {
		prevState = existing.State
	}

	err := sm.db.Account.UpdateOneID(accountID).
		SetState(account.StateActive).
		ClearStateUntil().
		SetErrorMsg("").
		SetLastUsedAt(now).
		Exec(dbCtx)
	if err != nil {
		slog.Warn("状态机：转移到 active 失败", "account_id", accountID, "error", err)
		return
	}
	if prevState != account.StateActive {
		sm.notifyCritical()
	}
}

// transition 把账号转到指定状态。stateUntil=nil 表示无到期（disabled）或清空。
func (sm *StateMachine) transition(ctx context.Context, accountID int, newState account.State, stateUntil *time.Time, reason string) {
	dbCtx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	upd := sm.db.Account.UpdateOneID(accountID).
		SetState(newState).
		SetErrorMsg(truncateReason(reason))
	if stateUntil == nil {
		upd = upd.ClearStateUntil()
	} else {
		upd = upd.SetStateUntil(*stateUntil)
	}

	if err := upd.Exec(dbCtx); err != nil {
		slog.Error("状态机：转移状态失败",
			"account_id", accountID,
			"target_state", newState,
			"error", err)
		return
	}
	slog.Info("账号状态转移",
		"account_id", accountID,
		"state", newState,
		"until", stateUntil,
		"reason", reason)

	// Disabled 是关键转移：缓存里还挂着 active 的快照会让调度器反复选它、白白浪费 failover。
	// RateLimited / Degraded 有 state_until，缓存 3s 陈旧期可接受。
	if newState == account.StateDisabled {
		sm.notifyCritical()
	}
}

// SchedulabilityOf 根据当前状态 + 到期时间判断账号是否可调度。
//
// rate_limited / degraded 到期后**不会**自动写 DB（由下一次 Success 判决统一回收），
// 但调度器读到 state_until <= now 就会把它视为 active / StickyOnly，不再排除。
func SchedulabilityOf(acc *ent.Account, now time.Time) Schedulability {
	switch acc.State {
	case account.StateActive:
		return Normal
	case account.StateDisabled:
		return NotSchedulable
	case account.StateRateLimited:
		if acc.StateUntil != nil && acc.StateUntil.After(now) {
			return NotSchedulable
		}
		return Normal // 已到期，lazy 回收
	case account.StateDegraded:
		if acc.StateUntil != nil && acc.StateUntil.After(now) {
			return StickyOnly // 只在没有 Normal 账号时兜底
		}
		return Normal
	default:
		// 未知状态值：保守按不可用处理
		return NotSchedulable
	}
}

// truncateReason 限制 error_msg 长度，防止异常文本把列撑爆。
func truncateReason(s string) string {
	const maxLen = 500
	if len(s) > maxLen {
		return s[:maxLen]
	}
	return s
}
