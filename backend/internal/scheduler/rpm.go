package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

const rpmKeyTTL = 120 * time.Second

// RPMCounter 渠道/用户/分组维度的 RPM 计数器。
// 基于 Redis STRING + 分钟粒度 key 实现：渠道维度做限流闸门，
// 用户/分组维度做管理端观测计数。
type RPMCounter struct {
	rdb *redis.Client
}

// NewRPMCounter 创建 RPM 计数器
func NewRPMCounter(rdb *redis.Client) *RPMCounter {
	return &RPMCounter{rdb: rdb}
}

// currentMinute 返回当前分钟窗口序号（unix 秒 / 60）。
// 使用本地时间：分钟级 key 粒度下各机器时钟偏差（通常 <1s）完全可接受，
// 省去每次调用一次 Redis TIME 命令的额外 RTT。
func currentMinute() int64 {
	return time.Now().Unix() / 60
}

// keyMinuteKey 生成密钥端点维度指定分钟窗口的 Redis key。
// 前缀带 chkey: 段，与用户/分组/渠道维度 key 的 ID 空间隔离。
func keyMinuteKey(channelKeyID int, minute int64) string {
	return fmt.Sprintf("rpm:chkey:%d:%d", channelKeyID, minute)
}

// userMinuteKey 生成用户维度指定分钟窗口的 Redis key。
// 前缀带 user: 段，与渠道/分组维度 key 的 ID 空间隔离。
func userMinuteKey(userID int, minute int64) string {
	return fmt.Sprintf("rpm:user:%d:%d", userID, minute)
}

// groupMinuteKey 生成分组维度指定分钟窗口的 Redis key。
func groupMinuteKey(groupID int, minute int64) string {
	return fmt.Sprintf("rpm:group:%d:%d", groupID, minute)
}

// incrementByKey 原子递增指定 key 的计数并续期，返回递增后的值。
func (r *RPMCounter) incrementByKey(ctx context.Context, key string) (int, error) {
	pipe := r.rdb.TxPipeline()
	incrCmd := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, rpmKeyTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, err
	}
	return int(incrCmd.Val()), nil
}

// IncrementUserGroupRPM 单 pipeline 合并递增用户与分组当前分钟的请求计数
// （管理端观测口径，不做限流；已鉴权进入转发链路即计入，失败请求不回退）。
// groupID <= 0 时只计用户维度。合并成一次 Redis RTT：这是转发热路径每请求必经的调用。
func (r *RPMCounter) IncrementUserGroupRPM(ctx context.Context, userID, groupID int) {
	if r.rdb == nil {
		return
	}
	minute := currentMinute()
	pipe := r.rdb.TxPipeline()
	userKey := userMinuteKey(userID, minute)
	pipe.Incr(ctx, userKey)
	pipe.Expire(ctx, userKey, rpmKeyTTL)
	if groupID > 0 {
		groupKey := groupMinuteKey(groupID, minute)
		pipe.Incr(ctx, groupKey)
		pipe.Expire(ctx, groupKey, rpmKeyTTL)
	}
	_, _ = pipe.Exec(ctx)
}

// GetGroupRPMs 批量获取多个分组当前分钟的请求计数（管理端观测用）。
func (r *RPMCounter) GetGroupRPMs(ctx context.Context, groupIDs []int) map[int]int {
	result := make(map[int]int, len(groupIDs))
	if r.rdb == nil {
		return result
	}
	minute := currentMinute()
	pipe := r.rdb.Pipeline()
	cmds := make(map[int]*redis.StringCmd, len(groupIDs))
	for _, id := range groupIDs {
		cmds[id] = pipe.Get(ctx, groupMinuteKey(id, minute))
	}
	_, _ = pipe.Exec(ctx)
	for id, cmd := range cmds {
		if n, err := cmd.Int(); err == nil {
			result[id] = n
		}
	}
	return result
}

// GetUserRPMs 批量获取多个用户当前分钟的请求计数（管理端观测用）。
func (r *RPMCounter) GetUserRPMs(ctx context.Context, userIDs []int) map[int]int {
	result := make(map[int]int, len(userIDs))
	if r.rdb == nil {
		return result
	}
	minute := currentMinute()
	pipe := r.rdb.Pipeline()
	cmds := make(map[int]*redis.StringCmd, len(userIDs))
	for _, id := range userIDs {
		cmds[id] = pipe.Get(ctx, userMinuteKey(id, minute))
	}
	_, _ = pipe.Exec(ctx)
	for id, cmd := range cmds {
		if n, err := cmd.Int(); err == nil {
			result[id] = n
		}
	}
	return result
}

// GetKeyRPMs 批量获取多个密钥端点当前分钟的请求计数（管理端观测用）。
func (r *RPMCounter) GetKeyRPMs(ctx context.Context, channelKeyIDs []int) map[int]int {
	result := make(map[int]int, len(channelKeyIDs))
	if r.rdb == nil {
		return result
	}
	minute := currentMinute()
	pipe := r.rdb.Pipeline()
	cmds := make(map[int]*redis.StringCmd, len(channelKeyIDs))
	for _, id := range channelKeyIDs {
		cmds[id] = pipe.Get(ctx, keyMinuteKey(id, minute))
	}
	_, _ = pipe.Exec(ctx)
	for id, cmd := range cmds {
		if n, err := cmd.Int(); err == nil {
			result[id] = n
		}
	}
	return result
}

// decrementRPMScript 仅当 key 存在时递减，避免创建无 TTL 的 key
var decrementRPMScript = redis.NewScript(`
	if redis.call('EXISTS', KEYS[1]) == 1 then
		return redis.call('DECR', KEYS[1])
	end
	return 0
`)

// DecrementKeyRPM 回退密钥端点维度 RPM 计数（请求失败时撤销预递增）。
// minute 须传 TryIncrementKeyRPM 返回的分钟窗口：请求跨分钟边界失败时
// 仍撤销原窗口的预递增，而不是扣穿新窗口/漏撤旧窗口。
func (r *RPMCounter) DecrementKeyRPM(ctx context.Context, channelKeyID int, minute int64) {
	if r.rdb == nil {
		return
	}
	decrementRPMScript.Run(ctx, r.rdb, []string{keyMinuteKey(channelKeyID, minute)})
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

// tryIncrementByKey 原子检查指定 key 的 RPM 限制并递增。
// maxRPM <= 0 不限制直接递增；Redis 不可用时 fail-open 放行。
func (r *RPMCounter) tryIncrementByKey(ctx context.Context, key string, maxRPM int) (bool, error) {
	if r.rdb == nil {
		return true, nil
	}

	// 不限制时直接递增
	if maxRPM <= 0 {
		_, err := r.incrementByKey(ctx, key)
		return true, err
	}

	result, err := tryIncrementScript.Run(ctx, r.rdb, []string{key}, maxRPM).Int()
	if err != nil {
		// fail-open：Redis 不可用时允许通过并尝试普通递增；
		// 放行须留痕——静默放行会让限流失效在故障期间不可见。
		// 限频（每 30s 最多一条）：故障期间每请求都会走到这里，不限频即日志风暴。
		if rpmFailOpenLog.allow() {
			slog.Warn("rpm_limit_fail_open", "key", key, "reason", err, "log_suppressed", failOpenLogInterval.String())
		}
		_, _ = r.incrementByKey(ctx, key)
		return true, nil
	}
	return result >= 0, nil
}

// TryIncrementKeyRPM 原子检查密钥端点维度 RPM 限制并递增。
// 返回本次计数所用的分钟窗口，供失败回退 DecrementKeyRPM 对同一窗口撤销。
func (r *RPMCounter) TryIncrementKeyRPM(ctx context.Context, channelKeyID int, maxRPM int) (bool, int64, error) {
	minute := currentMinute()
	if r.rdb == nil {
		return true, minute, nil
	}
	ok, err := r.tryIncrementByKey(ctx, keyMinuteKey(channelKeyID, minute), maxRPM)
	return ok, minute, err
}
