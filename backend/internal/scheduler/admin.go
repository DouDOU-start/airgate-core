package scheduler

import (
	"context"
	"time"

	"github.com/DouDOU-start/airgate-core/ent/account"
)

// 管理员 / 配额巡检的状态写入口。这些调用不经过 Apply —— 它们是"外部已知事实"
// 的直接落库，不需要 RPM 回退、失败计数等逻辑。

// ManualRecover 运维手动把账号恢复到 active：清状态、清到期、清原因。
func (s *Scheduler) ManualRecover(ctx context.Context, accountID int) error {
	dbCtx, cancel := context.WithTimeout(ctx, dbTimeout)
	defer cancel()
	return s.db.Account.UpdateOneID(accountID).
		SetState(account.StateActive).
		ClearStateUntil().
		SetErrorMsg("").
		Exec(dbCtx)
}

// ManualDisable 运维手动禁用账号（语义等同自动 disabled，需要再次 ManualRecover 才能恢复）。
func (s *Scheduler) ManualDisable(ctx context.Context, accountID int, reason string) error {
	dbCtx, cancel := context.WithTimeout(ctx, dbTimeout)
	defer cancel()
	return s.db.Account.UpdateOneID(accountID).
		SetState(account.StateDisabled).
		ClearStateUntil().
		SetErrorMsg(truncateReason(reason)).
		Exec(dbCtx)
}

// MarkRateLimited 配额巡检发现额度窗口已满时打入 rate_limited 直到 until。
func (s *Scheduler) MarkRateLimited(ctx context.Context, accountID int, until time.Time, reason string) {
	s.state.transition(ctx, accountID, account.StateRateLimited, &until, reason)
}

// ClearRateLimited 配额巡检发现已恢复时清限流态回到 active。
func (s *Scheduler) ClearRateLimited(ctx context.Context, accountID int) {
	s.state.transitionActive(ctx, accountID)
}

// ClearRateLimitMarkers 清除账号上的临时限流标记，不会恢复手动禁用的账号。
func (s *Scheduler) ClearRateLimitMarkers(ctx context.Context, accountID int) int {
	cleared := s.ClearFamilyCooldowns(ctx, accountID)
	dbCtx, cancel := context.WithTimeout(ctx, dbTimeout)
	defer cancel()
	item, err := s.db.Account.Get(dbCtx, accountID)
	if err != nil {
		return cleared
	}
	if item.State == account.StateRateLimited || item.State == account.StateDegraded {
		s.state.transitionActive(ctx, accountID)
		cleared++
	}
	return cleared
}

// MarkDisabled 把账号标记为 disabled（凭证失效等确定性错误）。
func (s *Scheduler) MarkDisabled(ctx context.Context, accountID int, reason string) {
	s.state.transition(ctx, accountID, account.StateDisabled, nil, reason)
}
