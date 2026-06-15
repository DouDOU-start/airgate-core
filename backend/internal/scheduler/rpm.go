package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	rpmKeyTTL    = 120 * time.Second
	rpmThreshold = 0.8 // 80% 进入 StickyOnly
)

// RPMCounter 账户级 RPM 计数器
// 基于 Redis STRING + 分钟粒度 key 实现
type RPMCounter struct {
	rdb *redis.Client
}

// NewRPMCounter 创建 RPM 计数器
func NewRPMCounter(rdb *redis.Client) *RPMCounter {
	return &RPMCounter{rdb: rdb}
}

// getMinuteKey 生成分钟粒度的 Redis key。
// 使用本地时间：分钟级 key 粒度下各机器时钟偏差（通常 <1s）完全可接受，
// 省去每次调用一次 Redis TIME 命令的额外 RTT。
func (r *RPMCounter) getMinuteKey(_ context.Context, accountID int) string {
	minute := time.Now().Unix() / 60
	return fmt.Sprintf("rpm:%d:%d", accountID, minute)
}

// IncrementRPM 原子递增当前分钟的请求计数，返回递增后的值
func (r *RPMCounter) IncrementRPM(ctx context.Context, accountID int) (int, error) {
	if r.rdb == nil {
		return 0, nil
	}

	key := r.getMinuteKey(ctx, accountID)
	pipe := r.rdb.TxPipeline()
	incrCmd := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, rpmKeyTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, err
	}
	return int(incrCmd.Val()), nil
}

// GetRPM 获取当前分钟的请求计数
func (r *RPMCounter) GetRPM(ctx context.Context, accountID int) (int, error) {
	if r.rdb == nil {
		return 0, nil
	}

	key := r.getMinuteKey(ctx, accountID)
	val, err := r.rdb.Get(ctx, key).Int()
	if err == redis.Nil {
		return 0, nil
	}
	return val, err
}

// decrementRPMScript 仅当 key 存在时递减，避免创建无 TTL 的 key
var decrementRPMScript = redis.NewScript(`
	if redis.call('EXISTS', KEYS[1]) == 1 then
		return redis.call('DECR', KEYS[1])
	end
	return 0
`)

// DecrementRPM 回退 RPM 计数（请求失败时撤销预递增）
// 仅当 key 存在时递减，避免分钟窗口切换后创建值为 -1 的无 TTL key
func (r *RPMCounter) DecrementRPM(ctx context.Context, accountID int) {
	if r.rdb == nil {
		return
	}
	key := r.getMinuteKey(ctx, accountID)
	decrementRPMScript.Run(ctx, r.rdb, []string{key})
}

// tryIncrementScript 原子检查 RPM 限制并递增
// ARGV[1] = maxRPM
// 返回: -1 = 已达上限（拒绝），>= 0 = 递增后的值（允许）
var tryIncrementScript = redis.NewScript(`
	local key = KEYS[1]
	local maxRPM = tonumber(ARGV[1])
	local current = tonumber(redis.call('GET', key) or '0')
	if current >= maxRPM then
		return -1
	end
	local newVal = redis.call('INCR', key)
	if redis.call('TTL', key) < 0 then
		redis.call('EXPIRE', key, 120)
	end
	return newVal
`)

// TryIncrementRPM 原子检查 RPM 限制并递增
// 如果当前 RPM 已达 maxRPM，返回 false 不递增；否则递增并返回 true
// maxRPM <= 0 表示不限制，直接递增
func (r *RPMCounter) TryIncrementRPM(ctx context.Context, accountID int, maxRPM int) (bool, error) {
	if r.rdb == nil {
		return true, nil
	}

	// 不限制时直接递增
	if maxRPM <= 0 {
		_, err := r.IncrementRPM(ctx, accountID)
		return true, err
	}

	key := r.getMinuteKey(ctx, accountID)
	result, err := tryIncrementScript.Run(ctx, r.rdb, []string{key}, maxRPM).Int()
	if err != nil {
		// fail-open：Redis 不可用时允许通过并尝试普通递增
		_, _ = r.IncrementRPM(ctx, accountID)
		return true, nil
	}
	return result >= 0, nil
}

// GetSchedulability 根据 RPM 使用率返回调度状态
// maxRPM <= 0 表示不限制
func (r *RPMCounter) GetSchedulability(ctx context.Context, accountID int, maxRPM int) Schedulability {
	if maxRPM <= 0 {
		return Normal
	}

	current, err := r.GetRPM(ctx, accountID)
	if err != nil {
		return Normal // fail-open
	}

	ratio := float64(current) / float64(maxRPM)
	if ratio >= 1.0 {
		return NotSchedulable
	}
	if ratio >= rpmThreshold {
		return StickyOnly
	}
	return Normal
}
