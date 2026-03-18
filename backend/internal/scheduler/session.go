package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const defaultSessionIdleTimeout = 30 * time.Minute

// SessionManager 账户级会话管理器
// 基于 Redis ZSET 实现，member=sessionUUID, score=unix timestamp
type SessionManager struct {
	rdb *redis.Client
}

// NewSessionManager 创建会话管理器
func NewSessionManager(rdb *redis.Client) *SessionManager {
	return &SessionManager{rdb: rdb}
}

// sessionLimitKey 生成 Redis key
func sessionLimitKey(accountID int) string {
	return fmt.Sprintf("session_limit:account:%d", accountID)
}

// registerSessionScript Lua 脚本：原子注册会话
// KEYS[1] = session_limit:account:{accountID}
// ARGV[1] = sessionUUID
// ARGV[2] = maxSessions
// ARGV[3] = idleTimeoutSeconds
// 返回: 1=允许, 0=拒绝
var registerSessionScript = redis.NewScript(`
	local key = KEYS[1]
	local sessionUUID = ARGV[1]
	local maxSessions = tonumber(ARGV[2])
	local idleTimeout = tonumber(ARGV[3])

	-- 使用 Redis 服务器时间
	local now = redis.call('TIME')
	local nowSec = tonumber(now[1])
	local expireBefore = nowSec - idleTimeout

	-- 清理过期会话
	redis.call('ZREMRANGEBYSCORE', key, '-inf', expireBefore)

	-- 检查会话是否已存在
	local score = redis.call('ZSCORE', key, sessionUUID)
	if score then
		-- 已存在：刷新时间戳
		redis.call('ZADD', key, nowSec, sessionUUID)
		redis.call('EXPIRE', key, idleTimeout + 60)
		return 1
	end

	-- 检查是否超过限制
	local count = redis.call('ZCARD', key)
	if count >= maxSessions then
		return 0
	end

	-- 添加新会话
	redis.call('ZADD', key, nowSec, sessionUUID)
	redis.call('EXPIRE', key, idleTimeout + 60)
	return 1
`)

// refreshSessionScript Lua 脚本：刷新会话时间戳
var refreshSessionScript = redis.NewScript(`
	local key = KEYS[1]
	local sessionUUID = ARGV[1]
	local idleTimeout = tonumber(ARGV[2])

	local now = redis.call('TIME')
	local nowSec = tonumber(now[1])

	local score = redis.call('ZSCORE', key, sessionUUID)
	if score then
		redis.call('ZADD', key, nowSec, sessionUUID)
		redis.call('EXPIRE', key, idleTimeout + 60)
	end
	return 1
`)

// getActiveSessionCountScript Lua 脚本：获取活跃会话数
var getActiveSessionCountScript = redis.NewScript(`
	local key = KEYS[1]
	local idleTimeout = tonumber(ARGV[1])

	local now = redis.call('TIME')
	local nowSec = tonumber(now[1])
	local expireBefore = nowSec - idleTimeout

	redis.call('ZREMRANGEBYSCORE', key, '-inf', expireBefore)
	return redis.call('ZCARD', key)
`)

// RegisterSession 注册会话，返回是否允许
func (s *SessionManager) RegisterSession(ctx context.Context, accountID int, sessionUUID string, maxSessions int, idleTimeout time.Duration) (bool, error) {
	if s.rdb == nil {
		return true, nil
	}

	key := sessionLimitKey(accountID)
	result, err := registerSessionScript.Run(ctx, s.rdb, []string{key},
		sessionUUID,
		maxSessions,
		int(idleTimeout.Seconds()),
	).Int()

	if err != nil {
		return true, nil // fail-open
	}
	return result == 1, nil
}

// RefreshSession 刷新会话时间戳
func (s *SessionManager) RefreshSession(ctx context.Context, accountID int, sessionUUID string, idleTimeout time.Duration) error {
	if s.rdb == nil {
		return nil
	}

	key := sessionLimitKey(accountID)
	_, err := refreshSessionScript.Run(ctx, s.rdb, []string{key},
		sessionUUID,
		int(idleTimeout.Seconds()),
	).Result()

	return err
}

// GetActiveSessionCount 获取活跃会话数
func (s *SessionManager) GetActiveSessionCount(ctx context.Context, accountID int, idleTimeout time.Duration) (int, error) {
	if s.rdb == nil {
		return 0, nil
	}

	key := sessionLimitKey(accountID)
	result, err := getActiveSessionCountScript.Run(ctx, s.rdb, []string{key},
		int(idleTimeout.Seconds()),
	).Int()

	if err != nil {
		return 0, err
	}
	return result, nil
}

// IsSessionActive 检查指定会话是否仍然活跃（存在且未过期）
func (s *SessionManager) IsSessionActive(ctx context.Context, accountID int, sessionID string, idleTimeout time.Duration) (bool, error) {
	if s.rdb == nil {
		return true, nil
	}

	key := sessionLimitKey(accountID)
	cutoff := float64(time.Now().Add(-idleTimeout).Unix())
	score, err := s.rdb.ZScore(ctx, key, sessionID).Result()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return true, err // fail-open
	}
	return score >= cutoff, nil
}

// GetSchedulability 检查会话数调度状态
// maxSessions <= 0 表示不限制
func (s *SessionManager) GetSchedulability(ctx context.Context, accountID int, extra map[string]interface{}) Schedulability {
	maxSessions := ExtraInt(extra, "max_sessions")
	if maxSessions <= 0 {
		return Normal
	}

	idleTimeout := time.Duration(ExtraInt(extra, "session_idle_timeout")) * time.Second
	if idleTimeout <= 0 {
		idleTimeout = defaultSessionIdleTimeout
	}

	count, err := s.GetActiveSessionCount(ctx, accountID, idleTimeout)
	if err != nil {
		return Normal // fail-open
	}

	if count >= maxSessions {
		return StickyOnly // 会话已满，仅允许已有会话
	}
	return Normal
}
