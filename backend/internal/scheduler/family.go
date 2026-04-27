package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// ModelFamily 把 (platform, model) 折叠成上游限流共享的"家族"键。
//
// 用于把"账号-家族"维度的限流冷却隔离开 —— 例如 gpt-image 撞 4000/min
// 不应该影响同账号上 chat 模型的可用性。OpenAI 的限流维度大多是 per-model 或
// per-family（同系列共享一个池），所以把限流冷却按家族打而不是按账号，更贴近上游真实行为。
//
// 当前规则：
//   - openai 平台下，gpt-image-* 系列共享 "gpt-image"
//   - 其它情况：直接用 model 本身作为家族键（每个 model 独立冷却）
//   - model 为空：用 platform 兜底，保持后向兼容
//
// 后续若发现有更多上游限流共享组（例如 gpt-5 家族共享 IPM），在此扩展即可。
func ModelFamily(platform, model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if strings.HasPrefix(m, "gpt-image") {
		return "gpt-image"
	}
	if m != "" {
		return m
	}
	return strings.ToLower(strings.TrimSpace(platform))
}

// FamilyCooldown 维护"账号 × 模型家族"的限流冷却，落 Redis、按 TTL 自然恢复。
//
// 与 DB 上的 Account.State 区别：
//   - DB state（rate_limited / disabled / degraded）是账号级，影响整账号所有调用。
//   - FamilyCooldown 是 (account, family) 级；撞 gpt-image 不会让 chat 流量受牵连。
//
// 短时高频（200ms~60s）的限流非常适合放 Redis：写读都廉价、过期由 TTL 兜底，
// 重启即清不影响业务（重启后再撞一次 429 又会重新写入）。
//
// Redis 不可用时退化为"不冷却"（fail-open），保证主链路可用性。
type FamilyCooldown struct {
	rdb *redis.Client
}

// NewFamilyCooldown 构造家族冷却管理器。rdb=nil 时所有方法 no-op。
func NewFamilyCooldown(rdb *redis.Client) *FamilyCooldown {
	return &FamilyCooldown{rdb: rdb}
}

// familyCooldownKey 与 concurrency.go / sticky 等命名风格保持一致：
// `<purpose>:v<version>:<id>:<sub>`，便于运维快速摸 key pattern。
func familyCooldownKey(accountID int, family string) string {
	return fmt.Sprintf("family-cooldown:v1:%d:%s", accountID, family)
}

// Mark 把 (account, family) 写入冷却，TTL = until - now（最少 1ms）。
// 旧的 cooldown 直接被覆盖：上游每次给的 Retry-After 都视为最新建议，无须保留历史。
func (fc *FamilyCooldown) Mark(ctx context.Context, accountID int, family string, until time.Time, reason string) {
	if fc == nil || fc.rdb == nil || family == "" {
		return
	}
	ttl := time.Until(until)
	if ttl <= 0 {
		ttl = time.Millisecond
	}
	if err := fc.rdb.Set(ctx, familyCooldownKey(accountID, family), reason, ttl).Err(); err != nil {
		slog.Debug("写入家族冷却失败",
			"account_id", accountID, "family", family, "ttl_ms", ttl.Milliseconds(), "error", err)
	}
}

// Until 查询 (account, family) 的冷却到期时间。
// 没有冷却返回 (zero, false)；Redis 不可用时也返回 (zero, false) —— 失败开放，
// 宁可让一次请求撞墙，也不能因为 Redis 抖动让整池账号不可用。
func (fc *FamilyCooldown) Until(ctx context.Context, accountID int, family string) (time.Time, bool) {
	if fc == nil || fc.rdb == nil || family == "" {
		return time.Time{}, false
	}
	ttl, err := fc.rdb.TTL(ctx, familyCooldownKey(accountID, family)).Result()
	if err != nil || ttl <= 0 {
		return time.Time{}, false
	}
	return time.Now().Add(ttl), true
}

// Clear 清除指定家族的冷却。管理员强制解封 / 测试场景使用。
// 业务正常路径不需要主动清，TTL 到期自动清掉。
func (fc *FamilyCooldown) Clear(ctx context.Context, accountID int, family string) {
	if fc == nil || fc.rdb == nil || family == "" {
		return
	}
	_ = fc.rdb.Del(ctx, familyCooldownKey(accountID, family)).Err()
}

// FamilyCooldownEntry 描述一条仍在生效的家族冷却。给后台展示用。
type FamilyCooldownEntry struct {
	Family string
	Until  time.Time
	Reason string
}

// List 列出指定账号当前所有家族冷却。供后台账号管理页展示用。
//
// 实现：用 SCAN 走一遍 `family-cooldown:v1:<acc>:*` 模式，对每个 key 取 TTL + GET 拿原因。
// 一个账号通常只有 0~3 个家族在冷却，COUNT=32 一轮就回。Redis 不可用 / 报错时返回部分结果，
// 后台只用来展示，不要求完整。
func (fc *FamilyCooldown) List(ctx context.Context, accountID int) []FamilyCooldownEntry {
	if fc == nil || fc.rdb == nil {
		return nil
	}
	prefix := familyCooldownKey(accountID, "")
	pattern := prefix + "*"
	var entries []FamilyCooldownEntry
	var cursor uint64
	for {
		keys, next, err := fc.rdb.Scan(ctx, cursor, pattern, 32).Result()
		if err != nil {
			return entries
		}
		for _, key := range keys {
			ttl, err := fc.rdb.TTL(ctx, key).Result()
			if err != nil || ttl <= 0 {
				continue
			}
			reason, _ := fc.rdb.Get(ctx, key).Result()
			entries = append(entries, FamilyCooldownEntry{
				Family: strings.TrimPrefix(key, prefix),
				Until:  time.Now().Add(ttl),
				Reason: reason,
			})
		}
		if next == 0 {
			break
		}
		cursor = next
	}
	return entries
}
