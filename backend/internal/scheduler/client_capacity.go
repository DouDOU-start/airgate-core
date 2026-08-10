package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	ErrUserConcurrencyLimit   = errors.New("已达到用户并发上限")
	ErrAPIKeyConcurrencyLimit = errors.New("已达到 API Key 并发上限")
)

var acquireClientCapacityScript = redis.NewScript(`
	local now = tonumber(ARGV[1])
	local userMax = tonumber(ARGV[2])
	local keyMax = tonumber(ARGV[3])
	local groupEnabled = tonumber(ARGV[4])
	local requestID = ARGV[5]
	local ttl = tonumber(ARGV[6])

	if userMax > 0 then
		redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now)
		if redis.call('ZCARD', KEYS[1]) >= userMax then
			return -1
		end
	end
	if keyMax > 0 then
		redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', now)
		if redis.call('ZCARD', KEYS[2]) >= keyMax then
			return -2
		end
	end

	local deadline = now + ttl
	if userMax > 0 then
		redis.call('ZADD', KEYS[1], deadline, requestID)
		if redis.call('TTL', KEYS[1]) < ttl then redis.call('EXPIRE', KEYS[1], ttl) end
	end
	if keyMax > 0 then
		redis.call('ZADD', KEYS[2], deadline, requestID)
		if redis.call('TTL', KEYS[2]) < ttl then redis.call('EXPIRE', KEYS[2], ttl) end
	end
	if groupEnabled > 0 then
		redis.call('ZREMRANGEBYSCORE', KEYS[3], '-inf', now)
		redis.call('ZADD', KEYS[3], deadline, requestID)
		if redis.call('TTL', KEYS[3]) < ttl then redis.call('EXPIRE', KEYS[3], ttl) end
	end
	return 1
`)

var releaseClientCapacityScript = redis.NewScript(`
	if tonumber(ARGV[2]) > 0 then redis.call('ZREM', KEYS[1], ARGV[1]) end
	if tonumber(ARGV[3]) > 0 then redis.call('ZREM', KEYS[2], ARGV[1]) end
	if tonumber(ARGV[4]) > 0 then redis.call('ZREM', KEYS[3], ARGV[1]) end
	return 1
`)

// AcquireClientCapacity 在一次 Redis 往返中原子检查用户和 API Key 并发限制，
// 并记录分组在途请求。
func (cm *ConcurrencyManager) AcquireClientCapacity(
	ctx context.Context,
	userID, keyID, groupID int,
	requestID string,
	userMax, keyMax int,
	slotTTL time.Duration,
) error {
	if cm == nil || cm.rdb == nil {
		return nil
	}
	if slotTTL <= 0 {
		slotTTL = defaultSlotTTL
	}
	result, err := acquireClientCapacityScript.Run(ctx, cm.rdb, []string{
		userConcurrencyKey(userID), apiKeyConcurrencyKey(keyID), groupConcurrencyKey(groupID),
	}, time.Now().Unix(), userMax, keyMax, boolInt(groupID > 0), requestID, int(slotTTL.Seconds())).Int()
	if err != nil {
		if concurrencyFailOpenLog.allow() {
			slog.Warn("client_capacity_limit_fail_open", "user_id", userID, "key_id", keyID, "reason", err)
		}
		return nil
	}
	switch result {
	case -1:
		return ErrUserConcurrencyLimit
	case -2:
		return ErrAPIKeyConcurrencyLimit
	default:
		return nil
	}
}

// ReleaseClientCapacity 在一次 Redis 往返中释放全部客户端侧槽位。
func (cm *ConcurrencyManager) ReleaseClientCapacity(
	ctx context.Context,
	userID, keyID, groupID int,
	requestID string,
	userTracked, keyTracked bool,
) {
	if cm == nil || cm.rdb == nil {
		return
	}
	_ = releaseClientCapacityScript.Run(ctx, cm.rdb, []string{
		userConcurrencyKey(userID), apiKeyConcurrencyKey(keyID), groupConcurrencyKey(groupID),
	}, requestID, boolInt(userTracked), boolInt(keyTracked), boolInt(groupID > 0)).Err()
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
