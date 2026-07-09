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

// Schedulability 调度状态（三态）。
// 原定义于 schedulability.go（账号调度器），账号池下线后内联至此；
// P1 渠道调度沿用同一语义（渠道 RPM 接近上限时仅粘性会话可用）。
type Schedulability int

const (
	// Normal 可正常调度
	Normal Schedulability = iota
	// StickyOnly 仅允许粘性会话访问（如 RPM 接近上限）
	StickyOnly
	// NotSchedulable 不可调度（如 RPM 已满）
	NotSchedulable
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

// currentMinute 返回当前分钟窗口序号（unix 秒 / 60）。
func currentMinute() int64 {
	return time.Now().Unix() / 60
}

// channelMinuteKey 生成渠道维度指定分钟窗口的 Redis key。
// 前缀带 channel: 段，与账号级 rpm:<id>:<minute> 的 ID 空间隔离。
func channelMinuteKey(channelID int, minute int64) string {
	return fmt.Sprintf("rpm:channel:%d:%d", channelID, minute)
}

// userMinuteKey 生成用户维度指定分钟窗口的 Redis key。
// 前缀带 user: 段，与账号级 / 渠道级 key 的 ID 空间隔离。
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

// IncrementRPM 原子递增当前分钟的请求计数，返回递增后的值
func (r *RPMCounter) IncrementRPM(ctx context.Context, accountID int) (int, error) {
	if r.rdb == nil {
		return 0, nil
	}
	return r.incrementByKey(ctx, r.getMinuteKey(ctx, accountID))
}

// IncrementUserRPM 递增用户当前分钟的请求计数（管理端观测口径，不做限流；
// 计入所有已通过鉴权进入转发链路的请求，失败请求不回退）。
func (r *RPMCounter) IncrementUserRPM(ctx context.Context, userID int) {
	if r.rdb == nil {
		return
	}
	_, _ = r.incrementByKey(ctx, userMinuteKey(userID, currentMinute()))
}

// IncrementGroupRPM 递增分组当前分钟的请求计数（管理端观测口径，不做限流；
// 口径与 IncrementUserRPM 一致：已鉴权进入转发链路即计入，失败请求不回退）。
func (r *RPMCounter) IncrementGroupRPM(ctx context.Context, groupID int) {
	if r.rdb == nil {
		return
	}
	_, _ = r.incrementByKey(ctx, groupMinuteKey(groupID, currentMinute()))
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

// GetChannelRPMs 批量获取多个渠道当前分钟的请求计数（管理端观测用）。
func (r *RPMCounter) GetChannelRPMs(ctx context.Context, channelIDs []int) map[int]int {
	result := make(map[int]int, len(channelIDs))
	if r.rdb == nil {
		return result
	}
	minute := currentMinute()
	pipe := r.rdb.Pipeline()
	cmds := make(map[int]*redis.StringCmd, len(channelIDs))
	for _, id := range channelIDs {
		cmds[id] = pipe.Get(ctx, channelMinuteKey(id, minute))
	}
	_, _ = pipe.Exec(ctx)
	for id, cmd := range cmds {
		if n, err := cmd.Int(); err == nil {
			result[id] = n
		}
	}
	return result
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
	decrementRPMScript.Run(ctx, r.rdb, []string{r.getMinuteKey(ctx, accountID)})
}

// DecrementChannelRPM 回退渠道维度 RPM 计数（请求失败时撤销预递增）。
// minute 须传 TryIncrementChannelRPM 返回的分钟窗口：请求跨分钟边界失败时
// 仍撤销原窗口的预递增，而不是扣穿新窗口/漏撤旧窗口。
func (r *RPMCounter) DecrementChannelRPM(ctx context.Context, channelID int, minute int64) {
	if r.rdb == nil {
		return
	}
	decrementRPMScript.Run(ctx, r.rdb, []string{channelMinuteKey(channelID, minute)})
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
		// fail-open：Redis 不可用时允许通过并尝试普通递增
		_, _ = r.incrementByKey(ctx, key)
		return true, nil
	}
	return result >= 0, nil
}

// TryIncrementRPM 原子检查 RPM 限制并递增
// 如果当前 RPM 已达 maxRPM，返回 false 不递增；否则递增并返回 true
// maxRPM <= 0 表示不限制，直接递增
func (r *RPMCounter) TryIncrementRPM(ctx context.Context, accountID int, maxRPM int) (bool, error) {
	if r.rdb == nil {
		return true, nil
	}
	return r.tryIncrementByKey(ctx, r.getMinuteKey(ctx, accountID), maxRPM)
}

// TryIncrementChannelRPM 原子检查渠道维度 RPM 限制并递增。
// 返回本次计数所用的分钟窗口，供失败回退 DecrementChannelRPM 对同一窗口撤销。
func (r *RPMCounter) TryIncrementChannelRPM(ctx context.Context, channelID int, maxRPM int) (bool, int64, error) {
	minute := currentMinute()
	if r.rdb == nil {
		return true, minute, nil
	}
	ok, err := r.tryIncrementByKey(ctx, channelMinuteKey(channelID, minute), maxRPM)
	return ok, minute, err
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
