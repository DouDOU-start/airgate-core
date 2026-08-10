package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrRPMLimit = errors.New("已达到 RPM 上限")

// acquireCapacityScript 在一次 Redis 往返中检查并占用 RPM 与并发容量。
// 返回值：1 表示占用成功，0 表示并发已满，-1 表示 RPM 已满。
var acquireCapacityScript = redis.NewScript(`
	local now = tonumber(ARGV[1])
	local maxConcurrency = tonumber(ARGV[2])
	local requestID = ARGV[3]
	local slotTTL = tonumber(ARGV[4])
	local maxRPM = tonumber(ARGV[5])

	redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now)
	if maxConcurrency > 0 and redis.call('ZCARD', KEYS[1]) >= maxConcurrency then
		return 0
	end

	local currentRPM = tonumber(redis.call('GET', KEYS[2]) or '0')
	if maxRPM > 0 and currentRPM >= maxRPM then
		return -1
	end
	redis.call('INCR', KEYS[2])
	if redis.call('TTL', KEYS[2]) < 0 then
		redis.call('EXPIRE', KEYS[2], 120)
	end

	redis.call('ZADD', KEYS[1], now + slotTTL, requestID)
	if redis.call('TTL', KEYS[1]) < slotTTL then
		redis.call('EXPIRE', KEYS[1], slotTTL)
	end
	return 1
`)

// AcquireKeyCapacity 为一个物理渠道凭证原子占用 RPM 计数和并发槽位。
// minute 返回 0 表示 Redis 故障放行，且没有记录 RPM 占用。
func (cm *ConcurrencyManager) AcquireKeyCapacity(
	ctx context.Context,
	credentialID int,
	requestID string,
	maxRPM, maxConcurrency int,
	slotTTL time.Duration,
) (int64, error) {
	minute := currentMinute()
	return cm.acquireCapacity(ctx, keyConcurrencyKey(credentialID), keyMinuteKey(credentialID, minute), requestID, minute, maxRPM, maxConcurrency, slotTTL)
}

// AcquireAccountCapacity 是账号池对应的原子容量占用方法。
func (cm *ConcurrencyManager) AcquireAccountCapacity(
	ctx context.Context,
	accountID int,
	requestID string,
	maxRPM, maxConcurrency int,
	slotTTL time.Duration,
) (int64, error) {
	minute := currentMinute()
	return cm.acquireCapacity(ctx, accountConcurrencyKey(accountID), accountMinuteKey(accountID, minute), requestID, minute, maxRPM, maxConcurrency, slotTTL)
}

func (cm *ConcurrencyManager) acquireCapacity(
	ctx context.Context,
	slotKey, rpmKey, requestID string,
	minute int64,
	maxRPM, maxConcurrency int,
	slotTTL time.Duration,
) (int64, error) {
	if cm == nil || cm.rdb == nil {
		return 0, nil
	}
	if slotTTL <= 0 {
		slotTTL = defaultSlotTTL
	}
	result, err := acquireCapacityScript.Run(ctx, cm.rdb, []string{slotKey, rpmKey},
		time.Now().Unix(), maxConcurrency, requestID, int(slotTTL.Seconds()), maxRPM,
	).Int()
	if err != nil {
		if concurrencyFailOpenLog.allow() {
			slog.Warn("capacity_limit_fail_open", "slot_key", slotKey, "rpm_key", rpmKey, "reason", err)
		}
		return 0, nil
	}
	switch result {
	case 0:
		return 0, ErrConcurrencyLimit
	case -1:
		return 0, ErrRPMLimit
	default:
		return minute, nil
	}
}
