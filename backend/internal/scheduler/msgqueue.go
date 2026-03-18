package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	defaultLockTTL  = 6 * time.Minute // 需大于 HTTP 超时（5min），预留 1min 缓冲
	defaultMinDelay = 200 * time.Millisecond
	defaultMaxDelay = 2000 * time.Millisecond
	defaultBaseRPM  = 60
	jitterFactor    = 0.15 // ±15% 抖动
)

// MessageQueue 用户消息串行队列
// 通过分布式锁实现账户级请求串行化，配合 RPM 自适应延迟
type MessageQueue struct {
	rdb *redis.Client
	rpm *RPMCounter
}

// NewMessageQueue 创建消息队列
func NewMessageQueue(rdb *redis.Client, rpm *RPMCounter) *MessageQueue {
	return &MessageQueue{rdb: rdb, rpm: rpm}
}

// msgQueueLockKey 锁 key
func msgQueueLockKey(accountID int) string {
	return fmt.Sprintf("umq:{%d}:lock", accountID)
}

// msgQueueLastKey 上次完成时间 key
func msgQueueLastKey(accountID int) string {
	return fmt.Sprintf("umq:{%d}:last", accountID)
}

// acquireLockScript 可重入锁获取 Lua 脚本
var acquireLockScript = redis.NewScript(`
	local key = KEYS[1]
	local requestID = ARGV[1]
	local ttlMs = tonumber(ARGV[2])

	local current = redis.call('GET', key)
	if current == false then
		redis.call('SET', key, requestID, 'PX', ttlMs)
		return 1
	end
	if current == requestID then
		redis.call('PEXPIRE', key, ttlMs)
		return 1
	end
	return 0
`)

// releaseLockScript 释放锁并记录完成时间
var releaseLockScript = redis.NewScript(`
	local lockKey = KEYS[1]
	local lastKey = KEYS[2]
	local requestID = ARGV[1]

	local current = redis.call('GET', lockKey)
	if current == requestID then
		redis.call('DEL', lockKey)
		local now = redis.call('TIME')
		local nowMs = tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)
		redis.call('SET', lastKey, nowMs, 'EX', 60)
		return 1
	end
	return 0
`)

// TryAcquire 尝试获取账户级消息锁
// 返回是否获取成功。如果获取失败，调用方应等待重试或放弃
func (m *MessageQueue) TryAcquire(ctx context.Context, accountID int, requestID string, lockTTL time.Duration) (bool, error) {
	if m.rdb == nil {
		return true, nil
	}
	if lockTTL <= 0 {
		lockTTL = defaultLockTTL
	}

	key := msgQueueLockKey(accountID)
	result, err := acquireLockScript.Run(ctx, m.rdb, []string{key},
		requestID,
		lockTTL.Milliseconds(),
	).Int()

	if err != nil {
		return true, nil // fail-open
	}
	return result == 1, nil
}

// WaitAcquire 等待获取锁，最多等待 timeout 时间
func (m *MessageQueue) WaitAcquire(ctx context.Context, accountID int, requestID string, lockTTL, timeout time.Duration) (bool, error) {
	if m.rdb == nil {
		return true, nil
	}

	deadline := time.Now().Add(timeout)
	for {
		acquired, err := m.TryAcquire(ctx, accountID, requestID, lockTTL)
		if err != nil {
			return true, nil // fail-open
		}
		if acquired {
			return true, nil
		}

		// 检测孤立锁：TTL <= 0 表示 key 没有设置过期时间（orphaned）
		lockKey := msgQueueLockKey(accountID)
		ttl, err := m.rdb.TTL(ctx, lockKey).Result()
		if err == nil && ttl <= 0 {
			// ttl == -1 表示无过期时间，ttl == -2 表示 key 不存在
			// 仅在 key 存在但无过期时间时强制删除
			if ttl == -1 {
				m.rdb.Del(ctx, lockKey)
			}
		}

		if time.Now().After(deadline) {
			return false, nil
		}

		// 短暂等待后重试
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// ForceRelease 无条件删除账户的消息队列锁（用于管理员清理孤立锁）
func (m *MessageQueue) ForceRelease(ctx context.Context, accountID int) error {
	key := fmt.Sprintf("umq:{%d}:lock", accountID)
	return m.rdb.Del(ctx, key).Err()
}

// Release 释放锁并记录完成时间
func (m *MessageQueue) Release(ctx context.Context, accountID int, requestID string) error {
	if m.rdb == nil {
		return nil
	}

	lockKey := msgQueueLockKey(accountID)
	lastKey := msgQueueLastKey(accountID)

	_, err := releaseLockScript.Run(ctx, m.rdb, []string{lockKey, lastKey},
		requestID,
	).Result()

	return err
}

// enforceDelayElapsedScript 使用 Redis 服务器时间计算距离上次完成的已过去时间（毫秒）
var enforceDelayElapsedScript = redis.NewScript(`
	local lastKey = KEYS[1]
	local lastMs = redis.call('GET', lastKey)
	if not lastMs then
		return -1
	end
	local now = redis.call('TIME')
	local nowMs = tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)
	return nowMs - tonumber(lastMs)
`)

// EnforceDelay 执行 RPM 自适应延迟
// 根据当前 RPM 使用率计算延迟时间，并等待
func (m *MessageQueue) EnforceDelay(ctx context.Context, accountID int, baseRPM int) error {
	delay := m.CalculateDelay(ctx, accountID, baseRPM)
	if delay <= 0 {
		return nil
	}

	// 使用 Redis 服务器时间计算距离上次完成的已过去时间
	lastKey := msgQueueLastKey(accountID)
	if m.rdb != nil {
		elapsedMs, err := enforceDelayElapsedScript.Run(ctx, m.rdb, []string{lastKey}).Int64()
		if err == nil && elapsedMs >= 0 {
			elapsed := time.Duration(elapsedMs) * time.Millisecond
			if elapsed >= delay {
				return nil // 已过去足够时间
			}
			delay -= elapsed
		}
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(delay):
		return nil
	}
}

// CalculateDelay 计算 RPM 自适应延迟
func (m *MessageQueue) CalculateDelay(ctx context.Context, accountID int, baseRPM int) time.Duration {
	if baseRPM <= 0 {
		baseRPM = defaultBaseRPM
	}

	currentRPM, err := m.rpm.GetRPM(ctx, accountID)
	if err != nil {
		return defaultMinDelay
	}

	ratio := float64(currentRPM) / float64(baseRPM)

	var delay time.Duration
	switch {
	case ratio < 0.5:
		delay = defaultMinDelay
	case ratio < 0.8:
		// 线性插值 0.5~0.8 → minDelay~maxDelay
		t := (ratio - 0.5) / 0.3
		delayMs := float64(defaultMinDelay.Milliseconds()) + t*float64(defaultMaxDelay.Milliseconds()-defaultMinDelay.Milliseconds())
		delay = time.Duration(delayMs) * time.Millisecond
	default:
		delay = defaultMaxDelay
	}

	// ±15% 抖动
	jitter := 1.0 + (rand.Float64()*2-1)*jitterFactor
	delay = time.Duration(math.Round(float64(delay) * jitter))

	return delay
}

// IsRealUserMessage 判断请求是否为真实用户消息
// 真实用户消息：最后一条消息 role=user 且不包含 tool_result
func IsRealUserMessage(body []byte) bool {
	// 简单的 JSON 解析判断
	type message struct {
		Role    string      `json:"role"`
		Content interface{} `json:"content"`
	}
	var parsed struct {
		Messages []message `json:"messages"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return false
	}
	if len(parsed.Messages) == 0 {
		return false
	}

	last := parsed.Messages[len(parsed.Messages)-1]
	if last.Role != "user" {
		return false
	}

	// 检查 content 是否包含 tool_result 类型
	if contentList, ok := last.Content.([]interface{}); ok {
		for _, item := range contentList {
			if itemMap, ok := item.(map[string]interface{}); ok {
				if t, ok := itemMap["type"].(string); ok {
					if t == "tool_result" || t == "tool_use_result" {
						return false
					}
				}
			}
		}
	}

	return true
}
