package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	rpmKeyTTL      = 120 * time.Second
	rpmThreshold   = 0.8 // 80% 进入 StickyOnly
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

// getMinuteKey 使用 Redis 服务器时间生成分钟粒度的 Redis key，避免分布式时钟偏差
func (r *RPMCounter) getMinuteKey(ctx context.Context, accountID int) string {
	t, err := r.rdb.Time(ctx).Result()
	if err != nil {
		// fallback to local time
		t = time.Now()
	}
	minute := t.Unix() / 60
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
