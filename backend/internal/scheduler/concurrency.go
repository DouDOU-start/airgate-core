package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrConcurrencyLimit = errors.New("并发槽位已满")

const (
	// defaultSlotTTL 单个请求槽位的默认过期时间，防止异常未释放
	defaultSlotTTL       = 5 * time.Minute
	capacitySignalBuffer = 4096
)

// acquireSlotScript 是 apikey / user / channel / group 各类并发槽共用的原子 Lua 脚本。
//
// 用 ZSET 存储，score = 该 slot 的过期时刻（acquire 时刻 + 各自 slotTTL），
// member = requestID。score 记 deadline 而非加入时刻，使同一 key 上不同 TTL 的
// slot（流式 30min / 非流式 5min）互不误伤——按加入时刻清理时，短 TTL 请求的
// acquire 会把仍活跃的长流 slot 当僵尸清掉，渠道并发上限形同虚设。
//
// 每次 acquire 前顺手用 ZREMRANGEBYSCORE 把"到期还没 release 的僵尸 slot"
// 清理掉：进程 panic / OOM / 重启导致 Release 没跑时，slot 也不会永久泄漏。
//
// 参数：
//
//	KEYS[1] = 槽位 key
//	ARGV[1] = 当前 unix 秒
//	ARGV[2] = max_concurrency（<= 0 表示不限制，但仍记录 slot——观测口径）
//	ARGV[3] = requestID
//	ARGV[4] = slotTTL 秒（单个 slot 的存活上限；整 key 的兜底 TTL 取历次 acquire 的最大值）
//
// 注：各类槽用不同前缀的 key 隔离；上游凭证使用
// concurrency:v3:credential:<id>，与旧端点级 v2 计数彻底隔离。
var acquireSlotScript = redis.NewScript(`
	local now = tonumber(ARGV[1])
	local max = tonumber(ARGV[2])
	local requestID = ARGV[3]
	local ttl = tonumber(ARGV[4])

	-- 清理僵尸 slot：score（过期时刻）早于当前时刻视为泄漏
	redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now)

	local current = redis.call('ZCARD', KEYS[1])
	if max <= 0 or current < max then
		redis.call('ZADD', KEYS[1], now + ttl, requestID)
		-- 整 key TTL 只增不减：短 TTL 的 acquire 不得缩短长流 slot 的存活窗口
		if redis.call('TTL', KEYS[1]) < ttl then
			redis.call('EXPIRE', KEYS[1], ttl)
		end
		return 1
	end
	return 0
`)

// ConcurrencyManager 分布式并发槽位管理。
// 基于 Redis ZSET 实现，按渠道/用户/API Key/分组维度各一个 ZSET，
// 成员为 request_id，score 为该 slot 的过期时刻。
type ConcurrencyManager struct {
	rdb              *redis.Client
	capacityReleased chan struct{}
	capacityWaiters  atomic.Int64
}

// NewConcurrencyManager 创建并发管理器
func NewConcurrencyManager(rdb *redis.Client) *ConcurrencyManager {
	return &ConcurrencyManager{rdb: rdb, capacityReleased: make(chan struct{}, capacitySignalBuffer)}
}

// WaitForCapacity 阻塞等待本实例上游槽位释放，或等待兜底时间结束。
// 兜底定时器用于保证跨实例场景下仍能继续重试。
func (cm *ConcurrencyManager) WaitForCapacity(ctx context.Context, fallback time.Duration) bool {
	if fallback <= 0 {
		return true
	}
	if cm == nil || cm.capacityReleased == nil {
		timer := time.NewTimer(fallback)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return true
		}
	}
	timer := time.NewTimer(fallback)
	defer timer.Stop()
	cm.capacityWaiters.Add(1)
	defer cm.capacityWaiters.Add(-1)
	select {
	case <-ctx.Done():
		return false
	case <-cm.capacityReleased:
		return true
	case <-timer.C:
		return true
	}
}

func (cm *ConcurrencyManager) signalCapacityReleased() {
	if cm == nil || cm.capacityReleased == nil || cm.capacityWaiters.Load() == 0 {
		return
	}
	select {
	case cm.capacityReleased <- struct{}{}:
	default:
	}
}

// apiKeyConcurrencyKey 生成 API Key 级 Redis Key。
func apiKeyConcurrencyKey(keyID int) string {
	return fmt.Sprintf("concurrency:v2:apikey:%d", keyID)
}

// userConcurrencyKey 生成用户级 Redis Key。
// 用户 A 下的所有 API Key 共享同一个 ZSET，实现"用户总并发"语义。
func userConcurrencyKey(userID int) string {
	return fmt.Sprintf("concurrency:v2:user:%d", userID)
}

// keyConcurrencyKey 生成上游物理凭证级 Redis Key。函数名保留以兼容现有调用接口，
// 入参已改为 credential_id，同一 API Key 的请求共享一个槽位集合。
func keyConcurrencyKey(credentialID int) string {
	return fmt.Sprintf("concurrency:v3:credential:%d", credentialID)
}

// groupConcurrencyKey 生成分组级 Redis Key（纯管理端观测，无限流语义）。
func groupConcurrencyKey(groupID int) string {
	return fmt.Sprintf("concurrency:v2:group:%d", groupID)
}

// accountConcurrencyKey 账号级并发槽 Redis Key。
func accountConcurrencyKey(accountID int) string {
	return fmt.Sprintf("concurrency:v2:account:%d", accountID)
}

// acquireSlotByKey 通用并发槽获取：给定 Redis key 和上限，原子性的
// 清理僵尸 slot + 检查上限 + ZADD 加入新 slot（score = 过期时刻）。
// maxConcurrency <= 0 时视为不限制，直接放行（不记录，热路径零 Redis 开销）。
// Redis 不可用时也直接放行，避免影响主链路可用性。
func (cm *ConcurrencyManager) acquireSlotByKey(ctx context.Context, key, requestID string, maxConcurrency int, slotTTL time.Duration) error {
	if cm.rdb == nil || maxConcurrency <= 0 {
		return nil
	}
	return cm.runAcquireSlot(ctx, key, requestID, maxConcurrency, slotTTL)
}

// runAcquireSlot 执行原子获取脚本。maxConcurrency <= 0 时脚本仍记录 slot 但不设
// 上限（观测口径，供管理端读取实时并发）；> 0 时超限返回 ErrConcurrencyLimit。
func (cm *ConcurrencyManager) runAcquireSlot(ctx context.Context, key, requestID string, maxConcurrency int, slotTTL time.Duration) error {
	if slotTTL <= 0 {
		slotTTL = defaultSlotTTL
	}

	now := time.Now().Unix()
	result, err := acquireSlotScript.Run(ctx, cm.rdb, []string{key},
		now,
		maxConcurrency,
		requestID,
		int(slotTTL.Seconds()),
	).Int()

	if err != nil {
		// fail-open：Redis 不可用时放行，避免影响主链路可用性；
		// 放行须留痕——静默放行会让并发闸门失效在故障期间不可见。
		// 限频（每 30s 最多一条）：故障期间每请求每闸门都会走到这里，不限频即日志风暴。
		if concurrencyFailOpenLog.allow() {
			slog.Warn("concurrency_limit_fail_open", "key", key, "reason", err, "log_suppressed", failOpenLogInterval.String())
		}
		return nil
	}

	if result == 0 {
		return ErrConcurrencyLimit
	}
	return nil
}

// AcquireAPIKeySlot 获取 API Key 级并发槽位。
// maxConcurrency <= 0 时直接放行（表示该 key 不限制并发）。
// 与用户级并发独立，两层闸门各自计数，调用方需要分别 release。
func (cm *ConcurrencyManager) AcquireAPIKeySlot(ctx context.Context, keyID int, requestID string, maxConcurrency int, slotTTL time.Duration) error {
	return cm.acquireSlotByKey(ctx, apiKeyConcurrencyKey(keyID), requestID, maxConcurrency, slotTTL)
}

// ReleaseAPIKeySlot 释放 API Key 级并发槽位
func (cm *ConcurrencyManager) ReleaseAPIKeySlot(ctx context.Context, keyID int, requestID string) {
	if cm.rdb == nil {
		return
	}
	cm.rdb.ZRem(ctx, apiKeyConcurrencyKey(keyID), requestID)
}

// AcquireKeySlot 获取上游物理凭证级并发槽位。
// maxConcurrency <= 0 时不限制但仍记录 slot（管理端实时并发观测口径）；
// 释放侧 ReleaseKeySlot 由 pipeline 无条件 defer 执行，两侧恒配对。
func (cm *ConcurrencyManager) AcquireKeySlot(ctx context.Context, channelKeyID int, requestID string, maxConcurrency int, slotTTL time.Duration) error {
	if cm.rdb == nil {
		return nil
	}
	return cm.runAcquireSlot(ctx, keyConcurrencyKey(channelKeyID), requestID, maxConcurrency, slotTTL)
}

// ReleaseKeySlot 释放密钥端点级并发槽位
func (cm *ConcurrencyManager) ReleaseKeySlot(ctx context.Context, channelKeyID int, requestID string) {
	if cm.rdb == nil {
		return
	}
	cm.rdb.ZRem(ctx, keyConcurrencyKey(channelKeyID), requestID)
	cm.signalCapacityReleased()
}

// TrackGroupSlot 记录分组级在途请求槽位（纯观测口径：不限流、不拒绝，
// 供管理端展示分组实时并发）。与 ReleaseGroupSlot 配对。
func (cm *ConcurrencyManager) TrackGroupSlot(ctx context.Context, groupID int, requestID string, slotTTL time.Duration) {
	if cm.rdb == nil {
		return
	}
	_ = cm.runAcquireSlot(ctx, groupConcurrencyKey(groupID), requestID, 0, slotTTL)
}

// ReleaseGroupSlot 释放分组级槽位。
func (cm *ConcurrencyManager) ReleaseGroupSlot(ctx context.Context, groupID int, requestID string) {
	if cm.rdb == nil {
		return
	}
	cm.rdb.ZRem(ctx, groupConcurrencyKey(groupID), requestID)
}

// AcquireUserSlot 获取用户级并发槽位。
// maxConcurrency <= 0 时直接放行（表示该用户不限制总并发）。
// 与 apikey / 渠道 两级槽位独立，调用方需要分别 release。
func (cm *ConcurrencyManager) AcquireUserSlot(ctx context.Context, userID int, requestID string, maxConcurrency int, slotTTL time.Duration) error {
	return cm.acquireSlotByKey(ctx, userConcurrencyKey(userID), requestID, maxConcurrency, slotTTL)
}

// ReleaseUserSlot 释放用户级并发槽位
func (cm *ConcurrencyManager) ReleaseUserSlot(ctx context.Context, userID int, requestID string) {
	if cm.rdb == nil {
		return
	}
	cm.rdb.ZRem(ctx, userConcurrencyKey(userID), requestID)
}

// GetUserCurrentCounts 批量获取多个用户的当前在途并发数（管理端观测用）。
// score 记 slot 过期时刻：用 ZCount 只统计"未过期的 slot"（score > now，开区间），
// 展示层不把僵尸 slot 算进去，即使 acquire 还没来得及清理它们。
func (cm *ConcurrencyManager) GetUserCurrentCounts(ctx context.Context, userIDs []int) map[int]int {
	result := make(map[int]int, len(userIDs))
	if cm.rdb == nil {
		return result
	}
	min := "(" + strconv.FormatInt(time.Now().Unix(), 10)
	pipe := cm.rdb.Pipeline()
	cmds := make(map[int]*redis.IntCmd, len(userIDs))
	for _, id := range userIDs {
		cmds[id] = pipe.ZCount(ctx, userConcurrencyKey(id), min, "+inf")
	}
	_, _ = pipe.Exec(ctx)
	for id, cmd := range cmds {
		if n, err := cmd.Result(); err == nil {
			result[id] = int(n)
		}
	}
	return result
}

// GetKeyCurrentCounts 批量获取多个上游物理凭证的当前在途并发数（管理端观测用）。
// 与 GetUserCurrentCounts 同口径：只统计未过期的 slot，僵尸 slot 不计入。
func (cm *ConcurrencyManager) GetKeyCurrentCounts(ctx context.Context, channelKeyIDs []int) map[int]int {
	result := make(map[int]int, len(channelKeyIDs))
	if cm.rdb == nil {
		return result
	}
	min := "(" + strconv.FormatInt(time.Now().Unix(), 10)
	pipe := cm.rdb.Pipeline()
	cmds := make(map[int]*redis.IntCmd, len(channelKeyIDs))
	for _, id := range channelKeyIDs {
		cmds[id] = pipe.ZCount(ctx, keyConcurrencyKey(id), min, "+inf")
	}
	_, _ = pipe.Exec(ctx)
	for id, cmd := range cmds {
		if n, err := cmd.Result(); err == nil {
			result[id] = int(n)
		}
	}
	return result
}

// GetGroupCurrentCounts 批量获取多个分组的当前在途并发数（管理端观测用）。
// 与 GetUserCurrentCounts 同口径：只统计未过期的 slot，僵尸 slot 不计入。
func (cm *ConcurrencyManager) GetGroupCurrentCounts(ctx context.Context, groupIDs []int) map[int]int {
	result := make(map[int]int, len(groupIDs))
	if cm.rdb == nil {
		return result
	}
	min := "(" + strconv.FormatInt(time.Now().Unix(), 10)
	pipe := cm.rdb.Pipeline()
	cmds := make(map[int]*redis.IntCmd, len(groupIDs))
	for _, id := range groupIDs {
		cmds[id] = pipe.ZCount(ctx, groupConcurrencyKey(id), min, "+inf")
	}
	_, _ = pipe.Exec(ctx)
	for id, cmd := range cmds {
		if n, err := cmd.Result(); err == nil {
			result[id] = int(n)
		}
	}
	return result
}

// AcquireAccountSlot 获取账号级并发槽位（语义同 AcquireKeySlot）。
func (cm *ConcurrencyManager) AcquireAccountSlot(ctx context.Context, accountID int, requestID string, maxConcurrency int, slotTTL time.Duration) error {
	if cm.rdb == nil {
		return nil
	}
	return cm.runAcquireSlot(ctx, accountConcurrencyKey(accountID), requestID, maxConcurrency, slotTTL)
}

// ReleaseAccountSlot 释放账号级并发槽位。
func (cm *ConcurrencyManager) ReleaseAccountSlot(ctx context.Context, accountID int, requestID string) {
	if cm.rdb == nil {
		return
	}
	cm.rdb.ZRem(ctx, accountConcurrencyKey(accountID), requestID)
	cm.signalCapacityReleased()
}

// GetAccountCurrentCounts 批量获取多个账号的当前在途并发数（管理端观测用）。
// 与 GetKeyCurrentCounts 同口径：只统计未过期的 slot。
func (cm *ConcurrencyManager) GetAccountCurrentCounts(ctx context.Context, accountIDs []int) map[int]int {
	result := make(map[int]int, len(accountIDs))
	if cm.rdb == nil {
		return result
	}
	min := "(" + strconv.FormatInt(time.Now().Unix(), 10)
	pipe := cm.rdb.Pipeline()
	cmds := make(map[int]*redis.IntCmd, len(accountIDs))
	for _, id := range accountIDs {
		cmds[id] = pipe.ZCount(ctx, accountConcurrencyKey(id), min, "+inf")
	}
	_, _ = pipe.Exec(ctx)
	for id, cmd := range cmds {
		if n, err := cmd.Result(); err == nil {
			result[id] = int(n)
		}
	}
	return result
}
