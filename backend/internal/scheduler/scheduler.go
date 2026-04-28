// Package scheduler 提供模型路由和负载感知的账户调度。
package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/DouDOU-start/airgate-core/ent"
)

var (
	ErrNoAvailableAccount = errors.New("无可用账户")
	ErrGroupNotFound      = errors.New("分组不存在")
)

// dbTimeout 后台 DB 操作超时，防止 goroutine 泄漏。
const dbTimeout = 10 * time.Second

// Scheduler 账户调度器。
//
// 两层职责清晰分离：
//   - 选号：SelectAccount（见 selection.go），基于 state + 软约束 + 负载均衡
//   - 判决：Apply（本文件），把 forwarder 的 Judgment 交给状态机执行转移
//
// 其它 method 都是对内部子组件（rpm/session/msgQueue/windowCost/state）的薄封装。
type Scheduler struct {
	db  *ent.Client
	rdb *redis.Client

	sticky         *StickySession
	windowCost     *WindowCostChecker
	rpm            *RPMCounter
	session        *SessionManager
	msgQueue       *MessageQueue
	state          *StateMachine
	familyCooldown *FamilyCooldown
	routeCache     *routeCache
}

// NewScheduler 构造调度器。
func NewScheduler(db *ent.Client, rdb *redis.Client) *Scheduler {
	rpm := NewRPMCounter(rdb)
	rc := newRouteCache(routeCacheTTL)
	fc := NewFamilyCooldown(rdb)
	s := &Scheduler{
		db:             db,
		rdb:            rdb,
		sticky:         NewStickySession(rdb),
		windowCost:     NewWindowCostChecker(db, rdb),
		rpm:            rpm,
		session:        NewSessionManager(rdb),
		msgQueue:       NewMessageQueue(rdb, rpm),
		state:          NewStateMachine(db, rdb, fc),
		familyCooldown: fc,
		routeCache:     rc,
	}
	s.state.onCriticalTransition = rc.InvalidateAll
	return s
}

// InvalidateRouteCache 清除指定分组的 route 缓存。admin 改分组 / 增删账号时调用。
// groupID <= 0 时清空所有缓存。
func (s *Scheduler) InvalidateRouteCache(groupID int) {
	if groupID <= 0 {
		s.routeCache.InvalidateAll()
		return
	}
	s.routeCache.InvalidateGroup(groupID)
}

// Apply 把 forwarder 的判决交给状态机。是 forwarder 与 scheduler 的唯一接触面。
// 非 Success 判决先回退 RPM 配额（上游没真正消耗），再施加状态转移。
func (s *Scheduler) Apply(ctx context.Context, accountID int, j Judgment) {
	if !j.Kind.IsSuccess() {
		s.DecrementRPM(ctx, accountID)
	}
	s.state.Apply(ctx, accountID, j)
}

// IncrementRPM 递增 RPM 计数。
func (s *Scheduler) IncrementRPM(ctx context.Context, accountID int) {
	if _, err := s.rpm.IncrementRPM(ctx, accountID); err != nil {
		slog.Debug("递增 RPM 计数失败", "account_id", accountID, "error", err)
	}
}

// TryIncrementRPM 原子检查上限并递增。已达上限返回 false（未递增）。
func (s *Scheduler) TryIncrementRPM(ctx context.Context, accountID int, maxRPM int) bool {
	allowed, err := s.rpm.TryIncrementRPM(ctx, accountID, maxRPM)
	if err != nil {
		slog.Debug("原子递增 RPM 失败", "account_id", accountID, "error", err)
		return true // fail-open
	}
	return allowed
}

// DecrementRPM 回退 RPM 计数（请求未实际消耗上游配额时调用）。
func (s *Scheduler) DecrementRPM(ctx context.Context, accountID int) {
	s.rpm.DecrementRPM(ctx, accountID)
}

// RefreshSession 刷新会话时间戳（成功时调用）。
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

// RegisterSession 登记会话（选中账号时调用）。
func (s *Scheduler) RegisterSession(ctx context.Context, accountID int, sessionID string, extra map[string]interface{}) bool {
	if sessionID == "" {
		return true
	}
	maxSessions := ExtraInt(extra, "max_sessions")
	if maxSessions <= 0 {
		return true
	}
	idleTimeout := time.Duration(ExtraInt(extra, "session_idle_timeout")) * time.Second
	if idleTimeout <= 0 {
		idleTimeout = defaultSessionIdleTimeout
	}
	allowed, _ := s.session.RegisterSession(ctx, accountID, sessionID, maxSessions, idleTimeout)
	return allowed
}

// defaultMsgLockWait msg lock 的默认 wait timeout。
// 号池大 / 每账号并发小的场景下，占用中的账号不如快速跳过让其它账号接住。
// 通过 account.extra.msg_lock_wait_seconds 可调整（例如号池小时设大）。
const defaultMsgLockWait = 3 * time.Second

// AcquireMessageLock 真实用户消息的账号级串行锁。
// wait timeout：extra.msg_lock_wait_seconds > 0 时用配置值，否则用 defaultMsgLockWait。
func (s *Scheduler) AcquireMessageLock(ctx context.Context, accountID int, requestID string, extra map[string]interface{}) (bool, error) {
	lockTTL := defaultLockTTL
	if ttlSec := ExtraInt(extra, "msg_lock_ttl_seconds"); ttlSec > 0 {
		lockTTL = time.Duration(ttlSec) * time.Second
	}
	wait := defaultMsgLockWait
	if waitSec := ExtraInt(extra, "msg_lock_wait_seconds"); waitSec > 0 {
		wait = time.Duration(waitSec) * time.Second
	}
	return s.msgQueue.WaitAcquire(ctx, accountID, requestID, lockTTL, wait)
}

// ReleaseMessageLock 释放消息锁。
func (s *Scheduler) ReleaseMessageLock(ctx context.Context, accountID int, requestID string) {
	if err := s.msgQueue.Release(ctx, accountID, requestID); err != nil {
		slog.Debug("释放消息锁失败", "account_id", accountID, "error", err)
	}
}

// EnforceMessageDelay 按 RPM 均摊延迟。
func (s *Scheduler) EnforceMessageDelay(ctx context.Context, accountID int, extra map[string]interface{}) {
	baseRPM := ExtraInt(extra, "base_rpm")
	if baseRPM <= 0 {
		baseRPM = ExtraInt(extra, "max_rpm")
	}
	if baseRPM <= 0 {
		return
	}
	if err := s.msgQueue.EnforceDelay(ctx, accountID, baseRPM); err != nil {
		slog.Debug("消息延迟失败", "account_id", accountID, "error", err)
	}
}

// AddWindowCost 请求计费后增量更新窗口费用。
func (s *Scheduler) AddWindowCost(ctx context.Context, accountID int, cost float64) {
	s.windowCost.AddCost(ctx, accountID, cost)
}

// ListFamilyCooldowns 返回指定账号当前生效中的所有家族级限流冷却。
// 后台管理页用来展示"哪个账号的哪个家族被限流了，还剩多久"。
func (s *Scheduler) ListFamilyCooldowns(ctx context.Context, accountID int) []FamilyCooldownEntry {
	if s.familyCooldown == nil {
		return nil
	}
	return s.familyCooldown.List(ctx, accountID)
}

// ClearFamilyCooldowns 清除指定账号当前所有家族级限流冷却。
func (s *Scheduler) ClearFamilyCooldowns(ctx context.Context, accountID int) int {
	if s.familyCooldown == nil {
		return 0
	}
	return s.familyCooldown.ClearAccount(ctx, accountID)
}
