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

	// 可选的调度约束检查器
	windowCost *WindowCostChecker
	rpm        *RPMCounter
	session    *SessionManager
	msgQueue   *MessageQueue

	// 连续失败计数器（accountID → 连续失败次数）
	failCounts sync.Map
	// 连续失败阈值，超过则标记账户为 error
	maxFailCount int
}

// NewScheduler 创建调度器
func NewScheduler(db *ent.Client, rdb *redis.Client) *Scheduler {
	rpm := NewRPMCounter(rdb)
	return &Scheduler{
		db:           db,
		rdb:          rdb,
		sticky:       NewStickySession(rdb),
		windowCost:   NewWindowCostChecker(db, rdb),
		rpm:          rpm,
		session:      NewSessionManager(rdb),
		msgQueue:     NewMessageQueue(rdb, rpm),
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

	// 第二层：调度约束过滤（窗口费用、RPM、会话数）
	var normalCandidates, stickyCandidates []*ent.Account
	for _, acc := range candidates {
		sched := s.checkSchedulability(ctx, acc)
		switch sched {
		case Normal:
			normalCandidates = append(normalCandidates, acc)
			stickyCandidates = append(stickyCandidates, acc)
		case StickyOnly:
			stickyCandidates = append(stickyCandidates, acc)
		case NotSchedulable:
			// 跳过
		}
	}

	// 第三层：粘性会话（可使用 StickyOnly + Normal 账户）
	if sessionID != "" {
		accountID, found := s.sticky.Get(ctx, userID, platform, sessionID)
		if found {
			for _, acc := range stickyCandidates {
				if acc.ID == accountID {
					s.sticky.Set(ctx, userID, platform, sessionID, accountID)
					return acc, nil
				}
			}
		}
	}

	// 第四层：负载均衡（仅 Normal 账户）
	if len(normalCandidates) == 0 {
		return nil, ErrNoAvailableAccount
	}

	selected := s.selectByLoadBalance(ctx, normalCandidates)
	if selected == nil {
		return nil, ErrNoAvailableAccount
	}

	// 注册会话（首次分配账户时）
	if sessionID != "" {
		if !s.RegisterSession(ctx, selected.ID, sessionID, selected.Extra) {
			// 会话数已满，从候选中移除此账户后重试
			var retry []*ent.Account
			for _, acc := range normalCandidates {
				if acc.ID != selected.ID {
					retry = append(retry, acc)
				}
			}
			if len(retry) == 0 {
				return nil, ErrNoAvailableAccount
			}
			selected = s.selectByLoadBalance(ctx, retry)
			if selected == nil {
				return nil, ErrNoAvailableAccount
			}
			if !s.RegisterSession(ctx, selected.ID, sessionID, selected.Extra) {
				return nil, ErrNoAvailableAccount
			}
		}
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

	// 查询分组关联的所有 active 账户（预加载代理信息，避免 forwarder 额外查询）
	accounts, err := grp.QueryAccounts().
		Where(
			account.PlatformEQ(platform),
			account.StatusEQ(account.StatusActive),
		).
		WithProxy().
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

// checkSchedulability 综合检查账户调度约束（窗口费用、RPM、会话数）
// 返回最严格的约束状态
func (s *Scheduler) checkSchedulability(ctx context.Context, acc *ent.Account) Schedulability {
	worst := Normal

	// 窗口费用检查
	if sched := s.windowCost.GetSchedulability(ctx, acc.ID, acc.Extra); sched > worst {
		worst = sched
	}
	if worst == NotSchedulable {
		return worst
	}

	// RPM 检查
	maxRPM := ExtraInt(acc.Extra, "max_rpm")
	if sched := s.rpm.GetSchedulability(ctx, acc.ID, maxRPM); sched > worst {
		worst = sched
	}
	if worst == NotSchedulable {
		return worst
	}

	// 会话数检查
	if sched := s.session.GetSchedulability(ctx, acc.ID, acc.Extra); sched > worst {
		worst = sched
	}

	return worst
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

// IncrementRPM 递增账户 RPM 计数（转发成功后调用）
func (s *Scheduler) IncrementRPM(ctx context.Context, accountID int) {
	if _, err := s.rpm.IncrementRPM(ctx, accountID); err != nil {
		slog.Debug("递增 RPM 计数失败", "account_id", accountID, "error", err)
	}
}

// DecrementRPM 回退 RPM 计数（请求未实际消耗上游配额时调用）
func (s *Scheduler) DecrementRPM(ctx context.Context, accountID int) {
	s.rpm.DecrementRPM(ctx, accountID)
}

// RefreshSession 刷新账户会话时间戳（转发成功后调用）
func (s *Scheduler) RefreshSession(ctx context.Context, accountID int, sessionID string, extra map[string]interface{}) {
	if sessionID == "" {
		return
	}
	idleTimeout := time.Duration(ExtraInt(extra, "session_idle_timeout")) * time.Second
	if idleTimeout <= 0 {
		idleTimeout = defaultSessionIdleTimeout
	}
	if err := s.session.RefreshSession(ctx, accountID, sessionID, idleTimeout); err != nil {
		slog.Debug("刷新会话时间戳失败", "account_id", accountID, "error", err)
	}
}

// RegisterSession 注册会话（调度选中账户后调用）
func (s *Scheduler) RegisterSession(ctx context.Context, accountID int, sessionID string, extra map[string]interface{}) bool {
	if sessionID == "" {
		return true
	}
	maxSessions := ExtraInt(extra, "max_sessions")
	if maxSessions <= 0 {
		return true // 不限制
	}
	idleTimeout := time.Duration(ExtraInt(extra, "session_idle_timeout")) * time.Second
	if idleTimeout <= 0 {
		idleTimeout = defaultSessionIdleTimeout
	}
	allowed, _ := s.session.RegisterSession(ctx, accountID, sessionID, maxSessions, idleTimeout)
	return allowed
}

// AcquireMessageLock 获取消息锁（真实用户消息串行化）
func (s *Scheduler) AcquireMessageLock(ctx context.Context, accountID int, requestID string, extra map[string]interface{}) (bool, error) {
	return s.msgQueue.WaitAcquire(ctx, accountID, requestID, defaultLockTTL, 30*time.Second)
}

// ReleaseMessageLock 释放消息锁
func (s *Scheduler) ReleaseMessageLock(ctx context.Context, accountID int, requestID string) {
	if err := s.msgQueue.Release(ctx, accountID, requestID); err != nil {
		slog.Debug("释放消息锁失败", "account_id", accountID, "error", err)
	}
}

// EnforceMessageDelay 执行消息延迟
func (s *Scheduler) EnforceMessageDelay(ctx context.Context, accountID int, extra map[string]interface{}) {
	baseRPM := ExtraInt(extra, "base_rpm")
	if baseRPM <= 0 {
		baseRPM = ExtraInt(extra, "max_rpm")
	}
	if baseRPM <= 0 {
		return // 无 RPM 配置，不延迟
	}
	if err := s.msgQueue.EnforceDelay(ctx, accountID, baseRPM); err != nil {
		slog.Debug("消息延迟失败", "account_id", accountID, "error", err)
	}
}

// AddWindowCost 在请求计费后增量更新缓存的窗口费用
func (s *Scheduler) AddWindowCost(ctx context.Context, accountID int, cost float64) {
	s.windowCost.AddCost(ctx, accountID, cost)
}

// ReportAccountError 立即标记账号为 error（用于 401/403 等确定性凭证错误）
func (s *Scheduler) ReportAccountError(accountID int, reason string) {
	slog.Error("账户凭证错误，立即标记为 error", "account_id", accountID, "reason", reason)
	_ = s.db.Account.UpdateOneID(accountID).
		SetStatus(account.StatusError).
		SetErrorMsg(reason).
		Exec(context.Background())
	s.failCounts.Delete(accountID)
}

// ReportResult 上报调度结果，用于动态调整
// reason 为失败时的错误原因（可选），记录到账号 error_msg 便于排查
func (s *Scheduler) ReportResult(accountID int, success bool, latency time.Duration, reason ...string) {
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
		errMsg := fmt.Sprintf("连续失败 %d 次，自动停用", count)
		if len(reason) > 0 && reason[0] != "" {
			errMsg = reason[0]
		}
		_ = s.db.Account.UpdateOneID(accountID).
			SetStatus(account.StatusError).
			SetErrorMsg(errMsg).
			Exec(context.Background())
		s.failCounts.Delete(accountID)
	}
}
