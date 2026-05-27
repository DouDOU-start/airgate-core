package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/account"
	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

// 状态机使用的默认窗口。
const (
	// rateLimitedDefault 上游没给 RetryAfter 时的兜底冷却。
	rateLimitedDefault = 60 * time.Second
	// rateLimitedMin 最小冷却下限。OpenAI 共享 org 限流时给的 RetryAfter 经常是 15ms~50ms，
	// 跟着这种瞬时值会让账号刚解锁就被同请求或并发请求立刻再撞墙。设一个下限让实际放出
	// 的请求拉开间隔，配合上游窗口老化才有效果。
	rateLimitedMin = 200 * time.Millisecond
	// rateLimitedMax OAuth 某些限流可能长达数天，设上限防止异常值。
	rateLimitedMax = 7 * 24 * time.Hour
	// degradedDefault 池账号抖动时的软降级窗口。
	degradedDefault = 60 * time.Second
	// degradedMax 池账号最长降级窗口。
	degradedMax = 10 * time.Minute
	// accountUnavailableThreshold 账号短暂 403 连续达到该次数后升级为 disabled。
	accountUnavailableThreshold = 3

	accountUnavailableCountExtraKey = "_airgate_account_unavailable_count"
)

// Judgment forwarder 对一次调用的判决，交给状态机做状态转移。
type Judgment struct {
	Kind           sdk.OutcomeKind
	RetryAfter     time.Duration
	Reason         string
	Duration       time.Duration // 仅用于日志 / 指标
	IsPool         bool          // 池账号（upstream_is_pool）走豁免路径
	Family         string        // 模型家族键（见 ModelFamily）。非空时 RateLimited 走 Redis 家族冷却而非账号级 DB state，避免 gpt-image 限流误伤 chat。
	UpstreamStatus int           // 上游 HTTP 状态码，用于池账号区分 401（自身凭证无效）和 403（透传上游错误）。
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
	db             *ent.Client
	rdb            *redis.Client
	familyCooldown *FamilyCooldown

	// onCriticalTransition Active ↔ Disabled 转移后的回调（由 Scheduler 注入）。
	// 用来清 route 缓存，让下次 SelectAccount 立刻看到新状态；
	// RateLimited / Degraded 这种"带 state_until 的临时状态"不走这里，由 TTL 兜底。
	onCriticalTransition func()
}

// NewStateMachine 构造状态机。fc 提供 (account, family) 维度的限流冷却，
// nil 时退化为旧行为：所有 RateLimited 都写账号级 DB state。
func NewStateMachine(db *ent.Client, rdb *redis.Client, fc *FamilyCooldown) *StateMachine {
	return &StateMachine{db: db, rdb: rdb, familyCooldown: fc}
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
//	AccountDead         → state=disabled（凭证失效，需人工介入）
//	AccountUnavailable  → state=degraded，累计 3 次后升级 disabled
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
		if dur < rateLimitedMin {
			dur = rateLimitedMin
		}
		if dur > rateLimitedMax {
			dur = rateLimitedMax
		}
		until := time.Now().Add(dur)
		// 有 Family 信息时走家族级冷却：撞 gpt-image 4000/min 不会让同账号 chat 被跳过。
		// 无 Family（admin 巡检 / 老插件）保留账号级 rate_limited 兜底，行为与改造前一致。
		if j.Family != "" && sm.familyCooldown != nil {
			sm.familyCooldown.Mark(ctx, accountID, j.Family, until, j.Reason)
			slog.Info("scheduler_family_cooldown",
				sdk.LogFieldAccountID, accountID,
				"family", j.Family,
				"until", until,
				sdk.LogFieldReason, j.Reason,
			)
			return
		}
		sm.transition(ctx, accountID, account.StateRateLimited, &until, j.Reason)

	case sdk.OutcomeAccountDead:
		if j.IsPool && j.UpstreamStatus != 401 {
			// 池账号的 403 等是上游透传的错误，池子本身没问题，
			// 不动状态，靠 failover 重试消化。
			// 401 表示池子自身的凭证无效，仍需禁用并说明原因。
			return
		}
		sm.transition(ctx, accountID, account.StateDisabled, nil, j.Reason)

	case sdk.OutcomeAccountUnavailable:
		sm.applyAccountUnavailable(ctx, accountID, j.Reason)

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

func (sm *StateMachine) applyAccountUnavailable(ctx context.Context, accountID int, reason string) {
	dbCtx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	existing, err := sm.db.Account.Get(dbCtx, accountID)
	if err != nil {
		slog.Warn("scheduler_account_unavailable_load_failed",
			sdk.LogFieldAccountID, accountID, sdk.LogFieldError, err)
		return
	}

	extra := cloneExtra(existing.Extra)
	now := time.Now()
	if existing.State == account.StateDegraded && existing.StateUntil != nil && existing.StateUntil.After(now) && extraInt(extra, accountUnavailableCountExtraKey) > 0 {
		slog.Warn("scheduler_account_unavailable_degraded_skip_count",
			sdk.LogFieldAccountID, accountID,
			"count", extraInt(extra, accountUnavailableCountExtraKey),
			"until", existing.StateUntil,
			sdk.LogFieldReason, reason,
		)
		return
	}

	count := extraInt(extra, accountUnavailableCountExtraKey) + 1
	extra[accountUnavailableCountExtraKey] = count

	if count >= accountUnavailableThreshold {
		delete(extra, accountUnavailableCountExtraKey)
		err = sm.db.Account.UpdateOneID(accountID).
			SetState(account.StateDisabled).
			ClearStateUntil().
			SetErrorMsg(truncateReason(reason)).
			SetExtra(extra).
			Exec(dbCtx)
		if err != nil {
			slog.Error("scheduler_account_unavailable_disable_failed",
				sdk.LogFieldAccountID, accountID, sdk.LogFieldError, err)
			return
		}
		slog.Warn("scheduler_account_unavailable_escalated",
			sdk.LogFieldAccountID, accountID,
			"count", count,
			sdk.LogFieldReason, reason,
		)
		sm.notifyCritical()
		return
	}

	until := now.Add(degradedDefault)
	err = sm.db.Account.UpdateOneID(accountID).
		SetState(account.StateDegraded).
		SetStateUntil(until).
		SetErrorMsg(truncateReason(reason)).
		SetExtra(extra).
		Exec(dbCtx)
	if err != nil {
		slog.Error("scheduler_account_unavailable_degrade_failed",
			sdk.LogFieldAccountID, accountID, sdk.LogFieldError, err)
		return
	}
	slog.Warn("scheduler_account_unavailable_degraded",
		sdk.LogFieldAccountID, accountID,
		"count", count,
		"until", until,
		sdk.LogFieldReason, reason,
	)
}

// transitionActive 成功时回到 active：清 state_until、清 reason、清失败计数、更新 last_used_at。
//
// disabled 状态受保护：只有管理员操作（ManualRecover / ToggleScheduling）才能解除，
// forwarder 的 Success 判决不会覆盖它——防止在飞请求的成功回调把手动禁用的账号重新激活。
func (sm *StateMachine) transitionActive(ctx context.Context, accountID int) {
	now := time.Now()
	dbCtx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	prevState := account.StateActive
	existing, getErr := sm.db.Account.Get(dbCtx, accountID)
	if getErr == nil {
		prevState = existing.State
	}

	if prevState == account.StateDisabled {
		_ = sm.db.Account.UpdateOneID(accountID).
			SetLastUsedAt(now).
			Exec(dbCtx)
		return
	}

	upd := sm.db.Account.UpdateOneID(accountID).
		SetState(account.StateActive).
		ClearStateUntil().
		SetErrorMsg("").
		SetLastUsedAt(now)
	if getErr == nil {
		if extraInt(existing.Extra, accountUnavailableCountExtraKey) > 0 {
			extra := cloneExtra(existing.Extra)
			delete(extra, accountUnavailableCountExtraKey)
			upd = upd.SetExtra(extra)
		}
	}

	err := upd.Exec(dbCtx)
	if err != nil {
		slog.Warn("scheduler_state_active_failed",
			sdk.LogFieldAccountID, accountID, sdk.LogFieldError, err)
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
		slog.Error("scheduler_state_transition_failed",
			sdk.LogFieldAccountID, accountID,
			"target_state", newState,
			sdk.LogFieldError, err,
		)
		return
	}
	slog.Info("scheduler_state_transition",
		sdk.LogFieldAccountID, accountID,
		"state", newState,
		"until", stateUntil,
		sdk.LogFieldReason, reason,
	)

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

func cloneExtra(input map[string]interface{}) map[string]interface{} {
	if len(input) == 0 {
		return map[string]interface{}{}
	}
	out := make(map[string]interface{}, len(input))
	for k, v := range input {
		out[k] = v
	}
	return out
}

func extraInt(extra map[string]interface{}, key string) int {
	switch v := extra[key].(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float64:
		return int(v)
	case float32:
		return int(v)
	default:
		return 0
	}
}
