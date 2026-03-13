// Package scheduler 提供模型路由和负载感知的账户调度
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/account"
)

var (
	ErrNoAvailableAccount = errors.New("无可用账户")
	ErrGroupNotFound      = errors.New("分组不存在")
)

// Scheduler 账户调度器
type Scheduler struct {
	db     *ent.Client
	rdb    *redis.Client
	sticky *StickySession

	// 连续失败计数器（accountID → 连续失败次数）
	failCounts sync.Map
	// 连续失败阈值，超过则标记账户为 error
	maxFailCount int
}

// NewScheduler 创建调度器
func NewScheduler(db *ent.Client, rdb *redis.Client) *Scheduler {
	return &Scheduler{
		db:           db,
		rdb:          rdb,
		sticky:       NewStickySession(rdb),
		maxFailCount: 3,
	}
}

// SelectAccount 选择一个可用账户
// 完整调度流程：模型路由 → 粘性会话 → 负载均衡
func (s *Scheduler) SelectAccount(ctx context.Context, platform, model string, userID, groupID int, sessionID string) (*ent.Account, error) {
	// 第一层：模型路由，获取候选账户列表
	candidates, err := s.routeAccounts(ctx, platform, model, groupID)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, ErrNoAvailableAccount
	}

	// 第二层：粘性会话
	if sessionID != "" {
		accountID, found := s.sticky.Get(ctx, userID, platform, sessionID)
		if found {
			// 验证粘性账户是否仍在候选列表中
			for _, acc := range candidates {
				if acc.ID == accountID {
					// 续期 TTL
					s.sticky.Set(ctx, userID, platform, sessionID, accountID)
					return acc, nil
				}
			}
			// 粘性账户不在候选列表中，忽略粘性
		}
	}

	// 第三层：负载均衡
	selected := s.selectByLoadBalance(ctx, candidates)
	if selected == nil {
		return nil, ErrNoAvailableAccount
	}

	// 设置粘性会话
	if sessionID != "" {
		s.sticky.Set(ctx, userID, platform, sessionID, selected.ID)
	}

	return selected, nil
}

// routeAccounts 根据分组的 model_routing 配置筛选候选账户
func (s *Scheduler) routeAccounts(ctx context.Context, platform, model string, groupID int) ([]*ent.Account, error) {
	// 查询分组及其关联的账户
	grp, err := s.db.Group.Get(ctx, groupID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrGroupNotFound, err)
	}

	// 查询分组关联的所有 active 账户
	accounts, err := grp.QueryAccounts().
		Where(
			account.PlatformEQ(platform),
			account.StatusEQ(account.StatusActive),
		).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询分组账户失败: %w", err)
	}

	// 如果没有模型路由配置，返回所有账户
	if len(grp.ModelRouting) == 0 {
		return accounts, nil
	}

	// 匹配模型路由规则
	allowedIDs := s.matchModelRouting(grp.ModelRouting, model)

	// allowedIDs 为 nil 表示使用所有账户（空切片 or 通配符匹配到空列表）
	if allowedIDs == nil {
		return accounts, nil
	}

	// 过滤候选账户
	if len(allowedIDs) == 0 {
		return accounts, nil
	}

	idSet := make(map[int64]bool, len(allowedIDs))
	for _, id := range allowedIDs {
		idSet[id] = true
	}

	var filtered []*ent.Account
	for _, acc := range accounts {
		if idSet[int64(acc.ID)] {
			filtered = append(filtered, acc)
		}
	}
	return filtered, nil
}

// matchModelRouting 匹配模型路由规则，返回允许的账户 ID 列表
// 返回 nil 表示不限制
func (s *Scheduler) matchModelRouting(routing map[string][]int64, model string) []int64 {
	// 精确匹配优先
	if ids, ok := routing[model]; ok {
		if len(ids) == 0 {
			return nil // 空列表表示所有账户
		}
		return ids
	}

	// 通配符匹配
	for pattern, ids := range routing {
		if matched, _ := filepath.Match(pattern, model); matched {
			if len(ids) == 0 {
				return nil
			}
			return ids
		}
	}

	// 没有匹配到任何规则，不限制
	return nil
}

// selectByLoadBalance 基于负载均衡选择最优账户
// 排序权重 = priority * 1000 + (1 - load_rate) * 100 + lru_score
func (s *Scheduler) selectByLoadBalance(ctx context.Context, candidates []*ent.Account) *ent.Account {
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

	now := time.Now()
	items := make([]scored, 0, len(candidates))

	for _, acc := range candidates {
		// 负载率：当前并发 / 最大并发
		currentLoad := s.getCurrentLoad(ctx, acc.ID)
		maxConc := acc.MaxConcurrency
		if maxConc <= 0 {
			maxConc = 5
		}
		loadRate := float64(currentLoad) / float64(maxConc)
		if loadRate > 1 {
			loadRate = 1
		}

		// LRU 评分：距离上次使用越久分数越高（0~100）
		var lruScore float64
		if acc.LastUsedAt != nil {
			elapsed := now.Sub(*acc.LastUsedAt).Minutes()
			lruScore = elapsed
			if lruScore > 100 {
				lruScore = 100
			}
		} else {
			lruScore = 100 // 从未使用过，优先级最高
		}

		score := float64(acc.Priority)*1000 + (1-loadRate)*100 + lruScore

		items = append(items, scored{acc: acc, score: score})
	}

	// 按分数降序排列
	sort.Slice(items, func(i, j int) bool {
		return items[i].score > items[j].score
	})

	return items[0].acc
}

// getCurrentLoad 获取账户当前并发数（从 Redis SET 大小获取）
func (s *Scheduler) getCurrentLoad(ctx context.Context, accountID int) int {
	if s.rdb == nil {
		return 0
	}
	key := fmt.Sprintf("concurrency:%d", accountID)
	n, err := s.rdb.SCard(ctx, key).Result()
	if err != nil {
		return 0
	}
	return int(n)
}

// ReportResult 上报调度结果，用于动态调整
func (s *Scheduler) ReportResult(accountID int, success bool, latency time.Duration) {
	if success {
		// 成功时清零失败计数
		s.failCounts.Delete(accountID)

		// 更新 last_used_at
		now := time.Now()
		_ = s.db.Account.UpdateOneID(accountID).
			SetLastUsedAt(now).
			Exec(context.Background())
		return
	}

	// 失败时增加计数
	val, _ := s.failCounts.LoadOrStore(accountID, 0)
	count := val.(int) + 1
	s.failCounts.Store(accountID, count)

	slog.Warn("账户请求失败",
		"account_id", accountID,
		"consecutive_failures", count,
		"latency", latency,
	)

	// 连续失败 N 次，标记账户为 error
	if count >= s.maxFailCount {
		slog.Error("账户连续失败次数超限，标记为 error",
			"account_id", accountID,
			"max_fail_count", s.maxFailCount,
		)
		_ = s.db.Account.UpdateOneID(accountID).
			SetStatus(account.StatusError).
			SetErrorMsg(fmt.Sprintf("连续失败 %d 次，自动停用", count)).
			Exec(context.Background())
		s.failCounts.Delete(accountID)
	}
}
