package scheduler

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrConcurrencyLimit = errors.New("并发槽位已满")

const (
	// defaultSlotTTL 单个请求槽位的默认过期时间，防止异常未释放
	defaultSlotTTL = 5 * time.Minute
)

// acquireSlotScript 是 account / apikey / user 三种并发槽共用的原子 Lua 脚本。
//
// 用 ZSET 存储，score = 加入时的 unix 时间戳，member = requestID。
// 每次 acquire 前顺手用 ZREMRANGEBYSCORE 把"超过 slotTTL 还没 release 的
// 僵尸 slot" 清理掉——彻底解决因进程 panic / OOM / 重启导致 Release 没跑
// 从而 slot 永远泄漏的历史坑（旧实现用 SET + EXPIRE 整 key，key 的 TTL 又
// 会被后续 acquire 重置，导致只要持续有流量僵尸 slot 就永远清不掉）。
//
// 参数：
//
//	KEYS[1] = 槽位 key
//	ARGV[1] = 当前 unix 秒
//	ARGV[2] = max_concurrency
//	ARGV[3] = requestID
//	ARGV[4] = slotTTL 秒（既是单个 slot 的存活上限，也是整 key 的兜底 TTL）
//
// 注：三类槽用不同前缀的 key 隔离（concurrency:v2:<id> / concurrency:v2:apikey:<id> /
// concurrency:v2:user:<id>），所以同一个脚本可以服务三方而不互相干扰。
// v2 前缀是为了和旧的 SET 数据区分——升级后旧 key 继续按自己的 TTL 自然消亡，
// 新 key 从零开始，不会因为 Redis type mismatch (WRONGTYPE) 冲突。
var acquireSlotScript = redis.NewScript(`
	local now = tonumber(ARGV[1])
	local max = tonumber(ARGV[2])
	local requestID = ARGV[3]
	local ttl = tonumber(ARGV[4])
	local staleBefore = now - ttl

	-- 清理僵尸 slot：score 早于 (now - ttl) 视为泄漏
	redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', staleBefore)

	local current = redis.call('ZCARD', KEYS[1])
	if current < max then
		redis.call('ZADD', KEYS[1], now, requestID)
		redis.call('EXPIRE', KEYS[1], ttl)
		return 1
	end
	return 0
`)

// ConcurrencyManager 分布式并发槽位管理
// 基于 Redis SET 实现，每个账户一个 SET，成员为 request_id
type ConcurrencyManager struct {
	rdb *redis.Client
}

// NewConcurrencyManager 创建并发管理器
func NewConcurrencyManager(rdb *redis.Client) *ConcurrencyManager {
	return &ConcurrencyManager{rdb: rdb}
}

// concurrencyKey 生成账号级 Redis Key。
// v2 前缀：旧版用 SET 实现无法清理僵尸 slot，升级后新 key 用 ZSET + 成员级
// 时间戳；旧 key 继续按自己的 TTL 自然消亡，不冲突。
func concurrencyKey(accountID int) string {
	return fmt.Sprintf("concurrency:v2:%d", accountID)
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

// acquireSlotByKey 通用并发槽获取：给定 Redis key 和上限，原子性的
// 清理僵尸 slot + 检查上限 + ZADD 加入新 slot（score = 当前时间）。
// maxConcurrency <= 0 时视为不限制，直接放行。
// Redis 不可用时也直接放行，避免影响主链路可用性。
func (cm *ConcurrencyManager) acquireSlotByKey(ctx context.Context, key, requestID string, maxConcurrency int, slotTTL time.Duration) error {
	if cm.rdb == nil || maxConcurrency <= 0 {
		return nil
	}
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
		// Redis 不可用时放行
		return nil
	}

	if result == 0 {
		return ErrConcurrencyLimit
	}
	return nil
}

// AcquireSlot 获取账号级并发槽位。
// 检查当前 SET 大小 < maxConcurrency，若未满则 SADD。
// slotTTL 为槽位过期时间，<= 0 时使用默认值（5 分钟）。
func (cm *ConcurrencyManager) AcquireSlot(ctx context.Context, accountID int, requestID string, maxConcurrency int, slotTTL time.Duration) error {
	return cm.acquireSlotByKey(ctx, concurrencyKey(accountID), requestID, maxConcurrency, slotTTL)
}

// ReleaseSlot 释放账号级并发槽位
func (cm *ConcurrencyManager) ReleaseSlot(ctx context.Context, accountID int, requestID string) {
	if cm.rdb == nil {
		return
	}

	key := concurrencyKey(accountID)
	cm.rdb.ZRem(ctx, key, requestID)
}

// AcquireAPIKeySlot 获取 API Key 级并发槽位。
// maxConcurrency <= 0 时直接放行（表示该 key 不限制并发）。
// 与账号级并发独立，两层闸门各自计数，调用方需要分别 release。
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

// AcquireUserSlot 获取用户级并发槽位。
// maxConcurrency <= 0 时直接放行（表示该用户不限制总并发）。
// 与 apikey / 账号 两级槽位独立，调用方需要分别 release。
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

// GetCurrentCount 获取账户当前并发数。
// 用 ZCount 只统计"未过期的 slot"（score >= now - defaultSlotTTL），
// 展示层不把僵尸 slot 算进去，即使 acquire 还没来得及清理它们。
func (cm *ConcurrencyManager) GetCurrentCount(ctx context.Context, accountID int) int {
	if cm.rdb == nil {
		return 0
	}
	cutoff := time.Now().Add(-defaultSlotTTL).Unix()
	min := "(" + strconv.FormatInt(cutoff, 10) // 开区间：严格大于 cutoff
	n, err := cm.rdb.ZCount(ctx, concurrencyKey(accountID), min, "+inf").Result()
	if err != nil {
		return 0
	}
	return int(n)
}

// GetCurrentCounts 批量获取多个账户的当前并发数
func (cm *ConcurrencyManager) GetCurrentCounts(ctx context.Context, accountIDs []int) map[int]int {
	result := make(map[int]int, len(accountIDs))
	if cm.rdb == nil {
		return result
	}
	cutoff := time.Now().Add(-defaultSlotTTL).Unix()
	min := "(" + strconv.FormatInt(cutoff, 10)
	pipe := cm.rdb.Pipeline()
	cmds := make(map[int]*redis.IntCmd, len(accountIDs))
	for _, id := range accountIDs {
		cmds[id] = pipe.ZCount(ctx, concurrencyKey(id), min, "+inf")
	}
	_, _ = pipe.Exec(ctx)
	for id, cmd := range cmds {
		if n, err := cmd.Result(); err == nil {
			result[id] = int(n)
		}
	}
	return result
}
