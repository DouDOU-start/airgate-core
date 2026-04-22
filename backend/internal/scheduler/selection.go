package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"path/filepath"
	"sort"
	"strconv"
	"time"

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

	now := time.Now()
	var normalCandidates, stickyCandidates []*ent.Account
	for _, acc := range candidates {
		switch s.checkSchedulability(ctx, acc, now) {
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
		slog.Warn("无正常账号，兜底使用降级账号", "account_id", selected.ID)
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
	excludeSet := make(map[int]bool, len(excludeIDs))
	for _, id := range excludeIDs {
		excludeSet[id] = true
	}
	filtered := candidates[:0]
	for _, acc := range candidates {
		if !excludeSet[acc.ID] {
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
		return nil, fmt.Errorf("%w: %v", ErrGroupNotFound, err)
	}

	accounts, err := grp.QueryAccounts().
		Where(account.PlatformEQ(platform)).
		WithProxy().
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询分组账户失败: %w", err)
	}

	// 缓存全量 platform 账号（包含所有 state）+ group 的 ModelRouting
	s.routeCache.Set(groupID, platform, accounts, grp.ModelRouting)

	return applyModelRouting(accounts, grp.ModelRouting, model), nil
}

// applyModelRouting 按 model 过滤候选账号。routing 为 nil/空或未命中时原样返回。
func applyModelRouting(accounts []*ent.Account, routing map[string][]int64, model string) []*ent.Account {
	if len(routing) == 0 {
		return accounts
	}
	allowedIDs := matchModelRouting(routing, model)
	if len(allowedIDs) == 0 {
		return accounts
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
func (s *Scheduler) checkSchedulability(ctx context.Context, acc *ent.Account, now time.Time) Schedulability {
	base := SchedulabilityOf(acc, now)
	if base == NotSchedulable {
		return NotSchedulable
	}
	worst := base

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
		maxConc = 5
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

// selectByLoadBalance 按 priority × 1000 + (1-load)×100 + lru_score 打分。
//
// 从 top-N 里随机选一个（而不是固定 argmax），解决高并发下所有 pickAccount 同时
// 看到一致的 Redis 状态 → 全部选中同一账号 → AcquireSlot race → 大量 failover 浪费
// 的问题。N 取 min(len, 8)，既能分散压力又保留"倾向好账号"的语义。
func (s *Scheduler) selectByLoadBalance(ctx context.Context, candidates []*ent.Account, now time.Time) *ent.Account {
	if len(candidates) == 0 {
		return nil
	}
	if len(candidates) == 1 {
		return candidates[0]
	}

	type scored struct {
		acc   *ent.Account
		score float64
	}
	items := make([]scored, 0, len(candidates))

	for _, acc := range candidates {
		maxConc := acc.MaxConcurrency
		if maxConc <= 0 {
			maxConc = 5
		}
		loadRate := float64(s.getCurrentLoad(ctx, acc.ID)) / float64(maxConc)
		if loadRate > 1 {
			loadRate = 1
		}

		lruScore := 100.0 // 从未使用过 → 最高
		if acc.LastUsedAt != nil {
			if elapsed := now.Sub(*acc.LastUsedAt).Minutes(); elapsed < 100 {
				lruScore = elapsed
			}
		}
		items = append(items, scored{
			acc:   acc,
			score: float64(acc.Priority)*1000 + (1-loadRate)*100 + lruScore,
		})
	}

	sort.Slice(items, func(i, j int) bool { return items[i].score > items[j].score })

	// 高并发热点打散：从 top-N 随机选。
	//
	// N 的取值决定了"多宽的账号池能参与分流"：
	//   - 号池很小时：N 接近 len，几乎等于均匀随机
	//   - 号池很大（百级）时：封顶一个较大的值，既保留"偏好 priority/LRU 高分"的倾向，
	//     又让 failover 每一轮都能分散到足够多的候选（max 成功数 ≈ N × failoverAttempts）
	//
	// 以前 N=8 对 124 号池显得太窄：150 并发最多只用到 8×3=24 个账号，剩 100 个完全闲置。
	// 提高到 32：3 次 failover 可覆盖 96 个账号，基本打满常见号池。
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
