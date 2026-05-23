package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/account"
)

// SelectAccount 选一个可用账户。流程：
//
//	模型路由 → 状态过滤 → 软约束过滤（RPM / window / session）→
//	粘性会话 → 负载均衡。
//
// excludeIDs 为 failover 时已尝试过的账户。
func (s *Scheduler) SelectAccount(ctx context.Context, platform, model string, userID, groupID int, sessionID string, excludeIDs ...int) (*ent.Account, error) {
	candidates, err := s.routeAccounts(ctx, platform, model, groupID)
	if err != nil {
		return nil, err
	}
	if candidates = excludeAccounts(candidates, excludeIDs); len(candidates) == 0 {
		return nil, ErrNoAvailableAccount
	}
	if fn, ok := s.accountFilters[platform]; ok {
		if candidates = fn(candidates, model); len(candidates) == 0 {
			return nil, ErrNoAvailableAccount
		}
	}

	now := time.Now()
	normalCandidates := make([]*ent.Account, 0, len(candidates))
	stickyCandidates := make([]*ent.Account, 0, len(candidates))
	for _, acc := range candidates {
		switch s.checkSchedulability(ctx, acc, model, now) {
		case Normal:
			normalCandidates = append(normalCandidates, acc)
			stickyCandidates = append(stickyCandidates, acc)
		case StickyOnly:
			stickyCandidates = append(stickyCandidates, acc)
		case NotSchedulable:
			// 跳过
		}
	}

	// 粘性会话优先（可命中 StickyOnly + Normal）
	if sessionID != "" {
		if accountID, found := s.sticky.Get(ctx, userID, platform, sessionID); found {
			for _, acc := range stickyCandidates {
				if acc.ID == accountID {
					s.sticky.Set(ctx, userID, platform, sessionID, accountID)
					return acc, nil
				}
			}
		}
	}

	if len(normalCandidates) == 0 {
		// 没有 Normal 但可能有 StickyOnly 兜底（如 degraded 账号）
		if len(stickyCandidates) == 0 {
			return nil, ErrNoAvailableAccount
		}
		selected := s.selectByLoadBalance(ctx, stickyCandidates, now)
		if selected == nil {
			return nil, ErrNoAvailableAccount
		}
		slog.Warn("scheduler_fallback_degraded_account",
			sdk.LogFieldAccountID, selected.ID,
			sdk.LogFieldPlatform, platform,
			sdk.LogFieldModel, model,
		)
		return s.maybeRegisterSession(ctx, selected, userID, platform, sessionID, stickyCandidates, now)
	}

	selected := s.selectByLoadBalance(ctx, normalCandidates, now)
	if selected == nil {
		return nil, ErrNoAvailableAccount
	}
	return s.maybeRegisterSession(ctx, selected, userID, platform, sessionID, normalCandidates, now)
}

// excludeAccounts 过滤掉 excludeIDs 中的账号（failover 已尝试过的）。
func excludeAccounts(candidates []*ent.Account, excludeIDs []int) []*ent.Account {
	if len(excludeIDs) == 0 {
		return candidates
	}
	excludeSet := make(map[int]struct{}, len(excludeIDs))
	for _, id := range excludeIDs {
		excludeSet[id] = struct{}{}
	}
	filtered := make([]*ent.Account, 0, len(candidates))
	for _, acc := range candidates {
		if _, excluded := excludeSet[acc.ID]; !excluded {
			filtered = append(filtered, acc)
		}
	}
	return filtered
}

// maybeRegisterSession 有 sessionID 时登记会话；session 数超限换一个候选重试。
func (s *Scheduler) maybeRegisterSession(ctx context.Context, selected *ent.Account, userID int, platform, sessionID string, pool []*ent.Account, now time.Time) (*ent.Account, error) {
	if sessionID == "" {
		return selected, nil
	}
	if s.RegisterSession(ctx, selected.ID, sessionID, selected.Extra) {
		s.sticky.Set(ctx, userID, platform, sessionID, selected.ID)
		return selected, nil
	}
	retry := pool[:0]
	for _, acc := range pool {
		if acc.ID != selected.ID {
			retry = append(retry, acc)
		}
	}
	if len(retry) == 0 {
		return nil, ErrNoAvailableAccount
	}
	selected = s.selectByLoadBalance(ctx, retry, now)
	if selected == nil || !s.RegisterSession(ctx, selected.ID, sessionID, selected.Extra) {
		return nil, ErrNoAvailableAccount
	}
	s.sticky.Set(ctx, userID, platform, sessionID, selected.ID)
	return selected, nil
}

// routeAccounts 取分组下匹配模型路由的账号；状态过滤延到 checkSchedulability。
//
// 不按 state 过滤的原因：新账号刚解除 disabled 后可立即被调度，不用等缓存失效。
//
// 首层命中 routeCache（key = (groupID, platform)）；miss 才查 DB。Model routing
// 规则与账号列表一起缓存，按 model 过滤的动作每次都重新跑——避免"不同 model 复用同一条缓存"
// 带来的错配。
func (s *Scheduler) routeAccounts(ctx context.Context, platform, model string, groupID int) ([]*ent.Account, error) {
	if accounts, routing, ok := s.routeCache.Get(groupID, platform); ok {
		return applyModelRouting(accounts, routing, model), nil
	}

	grp, err := s.db.Group.Get(ctx, groupID)
	if err != nil {
		return nil, normalizeGroupLookupError(err)
	}

	accounts, err := grp.QueryAccounts().
		Where(account.PlatformEQ(platform)).
		WithProxy().
		All(ctx)
	if err != nil {
		return nil, normalizeGroupAccountsLookupError(err)
	}

	// 缓存全量 platform 账号（包含所有 state）+ group 的 ModelRouting
	s.routeCache.Set(groupID, platform, accounts, grp.ModelRouting)

	return applyModelRouting(accounts, grp.ModelRouting, model), nil
}

func normalizeGroupLookupError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if ent.IsNotFound(err) {
		return fmt.Errorf("%w: %v", ErrGroupNotFound, err)
	}
	return fmt.Errorf("查询分组失败: %w", err)
}

func normalizeGroupAccountsLookupError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("查询分组账户失败: %w", err)
}

// applyModelRouting 按 model 过滤候选账号。routing 为 nil/空时原样返回；有规则但未命中时无候选。
func applyModelRouting(accounts []*ent.Account, routing map[string][]int64, model string) []*ent.Account {
	if len(routing) == 0 {
		return accounts
	}
	allowedIDs := matchModelRouting(routing, model)
	if allowedIDs == nil {
		return nil
	}
	if len(allowedIDs) == 0 {
		return nil
	}
	idSet := make(map[int64]bool, len(allowedIDs))
	for _, id := range allowedIDs {
		idSet[id] = true
	}
	// 不能原地复用 accounts slice：那是缓存共享的底层数组，别处还在读
	filtered := make([]*ent.Account, 0, len(accounts))
	for _, acc := range accounts {
		if idSet[int64(acc.ID)] {
			filtered = append(filtered, acc)
		}
	}
	return filtered
}

// matchModelRouting 匹配模型路由规则，返回允许的账号 ID 列表。nil 或空表示不限制。
func matchModelRouting(routing map[string][]int64, model string) []int64 {
	if ids, ok := routing[model]; ok {
		return ids
	}
	for pattern, ids := range routing {
		if matched, _ := filepath.Match(pattern, model); matched {
			return ids
		}
	}
	return nil
}

// checkSchedulability 先看状态（state + state_until），再叠加软约束（并发 / windowCost / RPM / session），取最严格者。
// model 用于推导请求所属的家族（gpt-image / chat 各算一个池），仅当该家族正在
// 冷却时才把账号当作 NotSchedulable —— 别的家族不受影响。
func (s *Scheduler) checkSchedulability(ctx context.Context, acc *ent.Account, model string, now time.Time) Schedulability {
	base := SchedulabilityOf(acc, now)
	if base == NotSchedulable {
		return NotSchedulable
	}
	worst := base

	// 家族级冷却：撞过这个 family 的账号在冷却期内对该 family 不可调度，
	// 但对其它 family 仍可用。Redis 不可用时退化为不冷却，不阻断主链路。
	if family := ModelFamily(acc.Platform, model); family != "" && s.familyCooldown != nil {
		if _, inCooldown := s.familyCooldown.Until(ctx, acc.ID, family); inCooldown {
			return NotSchedulable
		}
	}

	if sched := s.concurrencySchedulability(ctx, acc); sched > worst {
		worst = sched
	}
	if worst == NotSchedulable {
		return worst
	}
	if sched := s.windowCost.GetSchedulability(ctx, acc.ID, acc.Extra); sched > worst {
		worst = sched
	}
	if worst == NotSchedulable {
		return worst
	}
	if sched := s.rpm.GetSchedulability(ctx, acc.ID, ExtraInt(acc.Extra, "max_rpm")); sched > worst {
		worst = sched
	}
	if worst == NotSchedulable {
		return worst
	}
	if sched := s.session.GetSchedulability(ctx, acc.ID, acc.Extra); sched > worst {
		worst = sched
	}
	return worst
}

// concurrencySchedulability 根据当前并发用量返回调度约束：
//
//	load >= 100% → NotSchedulable（调度器直接跳过，避免下游 acquireSlot 失败浪费 failover）
//	load >=  80% → StickyOnly（软降级：只有粘性会话能选中，新请求优先换账号）
//	否则         → Normal
//
// 存在 TOCTOU（这里看没满、下一瞬 acquireSlot 却满）：forwarder 会 failover 到下一个账号兜底。
func (s *Scheduler) concurrencySchedulability(ctx context.Context, acc *ent.Account) Schedulability {
	maxConc := acc.MaxConcurrency
	if maxConc <= 0 {
		maxConc = DefaultAccountMaxConcurrency
	}
	load := s.getCurrentLoad(ctx, acc.ID)
	if load >= maxConc {
		return NotSchedulable
	}
	if float64(load)/float64(maxConc) >= 0.8 {
		return StickyOnly
	}
	return Normal
}

// selectByLoadBalance 严格按优先级分层：只从最高优先级层选账号，
// 同层内按 (1-load)*100 + lru_score 打分做加权随机。
//
// 低优先级账号只有在高优先级全部被 checkSchedulability 过滤掉后才能被选中。
// 同层内从 top-N 随机选一个，避免高并发下全部命中同一账号。
func (s *Scheduler) selectByLoadBalance(ctx context.Context, candidates []*ent.Account, now time.Time) *ent.Account {
	if len(candidates) == 0 {
		return nil
	}
	if len(candidates) == 1 {
		return candidates[0]
	}

	// 找到最高优先级，只保留该层候选
	maxPriority := candidates[0].Priority
	for _, acc := range candidates[1:] {
		if acc.Priority > maxPriority {
			maxPriority = acc.Priority
		}
	}
	tier := make([]*ent.Account, 0, len(candidates))
	for _, acc := range candidates {
		if acc.Priority == maxPriority {
			tier = append(tier, acc)
		}
	}
	if len(tier) == 1 {
		return tier[0]
	}

	// 同优先级内按负载 + LRU 打分
	type scored struct {
		acc   *ent.Account
		score float64
	}
	items := make([]scored, 0, len(tier))

	for _, acc := range tier {
		maxConc := acc.MaxConcurrency
		if maxConc <= 0 {
			maxConc = DefaultAccountMaxConcurrency
		}
		loadRate := float64(s.getCurrentLoad(ctx, acc.ID)) / float64(maxConc)
		if loadRate > 1 {
			loadRate = 1
		}

		lruScore := 100.0
		if acc.LastUsedAt != nil {
			if elapsed := now.Sub(*acc.LastUsedAt).Minutes(); elapsed < 100 {
				lruScore = elapsed
			}
		}
		items = append(items, scored{
			acc:   acc,
			score: (1-loadRate)*100 + lruScore,
		})
	}

	sort.Slice(items, func(i, j int) bool { return items[i].score > items[j].score })

	const maxTopN = 32
	topN := len(items)
	if topN > maxTopN {
		topN = maxTopN
	}
	return items[rand.Intn(topN)].acc
}

// getCurrentLoad 从 Redis ZSET 读账号当前"有效"并发数（过滤僵尸 slot）。
//
// 用 ZCount + score > (now - slotTTL) 只计算未过期的 slot，避免 release 异常的
// 僵尸 slot 把账号一直标满（下次 acquire 会清理它们，但 selection 不能等）。
// key 必须与 concurrency.go 的 concurrencyKey 保持一致（`concurrency:v2:<id>`）。
func (s *Scheduler) getCurrentLoad(ctx context.Context, accountID int) int {
	if s.rdb == nil {
		return 0
	}
	cutoff := time.Now().Add(-defaultSlotTTL).Unix()
	min := "(" + strconv.FormatInt(cutoff, 10) // 开区间：严格 > cutoff
	n, err := s.rdb.ZCount(ctx, concurrencyKey(accountID), min, "+inf").Result()
	if err != nil {
		return 0
	}
	return int(n)
}
