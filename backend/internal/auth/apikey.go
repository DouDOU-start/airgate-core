package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"

	"github.com/DouDOU-start/airgate-core/internal/pkg/logx"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/apikey"
	entsetting "github.com/DouDOU-start/airgate-core/ent/setting"
	"github.com/DouDOU-start/airgate-core/ent/user"
)

// API Key 缓存。
//
// 高并发下同一个 key 会被反复验证（每次 forward 都要走一次）。缓存后：
//   - 命中：零 DB 查询，O(1) 内存访问
//   - 未命中：1 次 DB 查询 + 写缓存
//
// TTL 设短（默认 5s）是个折衷：key 被禁用 / 配额耗尽 / 过期等动态变化能快速传播到
// 网关。运维手动禁用 key 后最多 5s 用户会看到 401，可接受。
//
// 失败结果（invalid / expired / quota / group_unbound）进"有界"本地负缓存 +
// Redis 共享缓存：Redis 故障窗口内被拒 key 的重试不至于直穿 DB；
// 本地负缓存用 maxNegativeCacheEntries 设上限，防止攻击者用随机 sk- key
// （每次哈希都不同）无界撑大进程内存，超限后新负条目只进带 TTL 的 Redis。
const apiKeyCacheTTL = 5 * time.Second

// maxNegativeCacheEntries 本地负缓存条目上限。达到上限后新负条目不再写入
// 本地 map（正条目不受影响）；负条目过期删除或被正条目覆盖时释放名额。
const maxNegativeCacheEntries = 4096

type apiKeyCacheEntry struct {
	info      *APIKeyInfo // 成功结果；失败时为 nil
	err       error       // 失败原因；成功时为 nil
	expiresAt time.Time
}

var (
	apiKeyCache sync.Map // map[hash] → apiKeyCacheEntry（成功结果 + 有界负结果）
	apiKeyRedis *redis.Client

	// negativeCacheCount 本地负缓存当前条目数（有界闸门，CAS 维护）。
	negativeCacheCount atomic.Int64

	// apiKeyLoadGroup DB 加载 singleflight：本地与 Redis 缓存 TTL 相同、会同时过期，
	// 热 key 过期瞬间的并发未命中只放一个去打 DB，其余共享同一结果（防缓存击穿）。
	apiKeyLoadGroup singleflight.Group
)

// apiKeyLoadTimeout 单次 DB 加载超时：加载用剥离取消的独立 ctx，
// 领跑请求中途断连不应让 singleflight 共乘的其余请求全部失败。
const apiKeyLoadTimeout = 5 * time.Second

var (
	ErrInvalidAPIKey      = errors.New("无效的 API Key")
	ErrAPIKeyExpired      = errors.New("API Key 已过期")
	ErrAPIKeyQuota        = errors.New("API Key 配额已用尽")
	ErrAPIKeyGroupUnbound = errors.New("API Key 未绑定分组，请联系管理员重新绑定")
	// ErrAPIKeyGroupExclusive 分组创建/绑定时并非专属分组，管理员事后才设为专属，
	// 且未把该用户加入白名单：已签发的 key 不再享有"创建时校验过就一直放行"的豁免，
	// 每次请求都按分组的最新专属状态重新判定（与 apikey_store.go GetGroupAccess 同一套判定口径）。
	ErrAPIKeyGroupExclusive = errors.New("绑定的分组已设为专属分组，且未开通访问权限，请联系管理员")
	// ErrUserDisabled 账户被禁用（手动禁用或风控自动封禁）：已签发的 key 一并失效。
	// 封禁/解封经 5s 验证缓存 TTL 自然传播，无需主动失效。
	ErrUserDisabled = errors.New("账户已被禁用，请联系管理员")
)

const apiKeyPrefix = "sk-"
const adminKeyPrefix = "admin-"
const apiKeyRedisCacheTTL = apiKeyCacheTTL

type apiKeyRedisEntry struct {
	Info *APIKeyInfo `json:"info,omitempty"`
	Err  string      `json:"err,omitempty"`
}

// APIKeyInfo API Key 验证后的信息
type APIKeyInfo struct {
	KeyID     int
	UserID    int
	UserEmail string
	GroupID   int

	// SellRate Reseller 设置的销售倍率（>0 时启用 markup，独立于平台计费）
	SellRate float64

	// MaxRate 密钥可接受的最高计费倍率，0 表示不限制。
	// >0 时若实际扣费倍率（ResolveBillingRate）超过该值，转发预检直接拒绝，
	// 防止管理员临时上调分组/用户倍率后下游不知情按新价扣费。
	MaxRate float64

	// KeyMaxConcurrency API Key 级并发上限，0 表示不限制。
	// 转发管线用 Redis ZSET 按 key_id 维度争抢槽位。
	KeyMaxConcurrency int

	// UserMaxConcurrency 用户级并发上限，0 表示不限制。
	// 同一个 user 下所有 API Key 共享这个配额——无论创建多少把 key，
	// 加起来同时在途的请求数不能超过这个值。与 KeyMaxConcurrency 是 AND 关系。
	UserMaxConcurrency int

	// 预加载字段，避免转发管线重复查询
	UserBalance         float64           // 用户余额
	UserGroupRates      map[int64]float64 // 用户级专属倍率（按 group_id），用于 ResolveBillingRate 优先级链
	TierGroupRates      map[int64]float64 // 用户等级倍率（按 group_id），优先级低于 UserGroupRates
	GroupRateMultiplier float64           // 分组倍率
	// GroupAlphaSearchPrice 分组对 codex 联网搜索的按次覆盖价（USD/次）；
	// nil=沿用全局 gateway 设置，非 nil（含 0）=覆盖全局。见 Group.alpha_search_price。
	GroupAlphaSearchPrice *float64

	// GroupAllowedClients 分组客户端白名单；空=不限制。
	GroupAllowedClients []string
	// GroupFallbackID 客户端不匹配时降级到的分组 ID；nil=直接拒绝。
	GroupFallbackID *int
}

// GenerateAPIKey 生成 API Key 和对应的哈希值
// 返回明文密钥（仅展示一次）和用于存储的哈希
func GenerateAPIKey() (key string, hash string, err error) {
	return generatePrefixedAPIKey(apiKeyPrefix)
}

// GenerateAdminAPIKey 生成管理员 API Key，返回明文密钥和哈希。
func GenerateAdminAPIKey() (key string, hash string, err error) {
	return generatePrefixedAPIKey(adminKeyPrefix)
}

func generatePrefixedAPIKey(prefix string) (key string, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	key = prefix + hex.EncodeToString(b)
	hash = HashAPIKey(key)
	return key, hash, nil
}

// HashAPIKey 对 API Key 进行 SHA256 哈希
func HashAPIKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

// AdminKeyHint 生成管理员 API Key 的显示提示（前缀 + 前4位...后4位）。
func AdminKeyHint(key string) string {
	if len(key) <= 12 {
		return key
	}
	return key[:10] + "..." + key[len(key)-4:]
}

// IsAdminAPIKey 判断是否为管理员 API Key 格式。
func IsAdminAPIKey(key string) bool {
	return len(key) > len(adminKeyPrefix) && key[:len(adminKeyPrefix)] == adminKeyPrefix
}

// ValidateAPIKey 验证 API Key 并返回关联信息。带 5s TTL 内存缓存，
// 高并发下同一个 key 300 req → 1 次 DB 查询 + 299 次缓存命中。
//
// 错误语义：
//   - ent.IsNotFound(err)：真的"key 不存在或已禁用" → ErrInvalidAPIKey（客户端 401）
//   - 其它 DB 错误（超时 / 连接池满 / ctx 取消）：原样返回 → middleware 按 5xx 处理
//
// DB 错误不缓存（下次请求立即重试，加快从瞬时故障中恢复）。
func ValidateAPIKey(ctx context.Context, db *ent.Client, key string) (*APIKeyInfo, error) {
	hash := HashAPIKey(key)

	// 读缓存
	if cached, ok := apiKeyCache.Load(hash); ok {
		if e := cached.(apiKeyCacheEntry); time.Now().Before(e.expiresAt) {
			if e.info != nil {
				slog.Debug("api_key_cache_hit", logx.LogFieldAPIKeyID, e.info.KeyID)
			} else {
				slog.Debug("api_key_cache_hit_negative", logx.LogFieldError, e.err)
			}
			return e.info, e.err
		}
		evictAPIKeyLocalCache(hash)
	}
	if info, err, ok := loadAPIKeyCacheFromRedis(ctx, hash); ok {
		if info != nil {
			slog.Debug("api_key_cache_hit_shared", logx.LogFieldAPIKeyID, info.KeyID)
		} else {
			slog.Debug("api_key_cache_hit_negative_shared", logx.LogFieldError, err)
		}
		storeAPIKeyLocalCache(hash, info, err)
		return info, err
	}
	slog.Debug("api_key_cache_miss")

	// 缓存未命中，经 singleflight 查 DB（同 hash 并发未命中合并为一次查询）。
	v, err, _ := apiKeyLoadGroup.Do(hash, func() (any, error) {
		loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), apiKeyLoadTimeout)
		defer cancel()
		return loadAndCacheAPIKey(loadCtx, db, hash)
	})
	if err != nil {
		return nil, err
	}
	return v.(*APIKeyInfo), nil
}

// loadAndCacheAPIKey 查 DB 加载 key 信息并写入缓存（ValidateAPIKey 的未命中路径，
// 经 singleflight 调用；错误语义与缓存策略见 ValidateAPIKey 注释）。
func loadAndCacheAPIKey(ctx context.Context, db *ent.Client, hash string) (*APIKeyInfo, error) {
	ak, err := db.APIKey.Query().
		Where(
			apikey.KeyHash(hash),
			apikey.StatusEQ(apikey.StatusActive),
		).
		WithUser(func(q *ent.UserQuery) {
			q.WithTier()
		}).
		WithGroup(func(q *ent.GroupQuery) {
			q.WithAllowedUsers()
		}).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			// 真"key 不存在"：缓存负结果，避免被拒的 key 反复打 DB
			cacheAPIKeyResult(hash, nil, ErrInvalidAPIKey)
			return nil, ErrInvalidAPIKey
		}
		// DB 瞬时故障：不缓存，下次请求重试
		slog.Error("api_key_lookup_failed", logx.LogFieldError, err)
		return nil, fmt.Errorf("查询 API Key 失败: %w", err)
	}

	// 检查过期时间
	if ak.ExpiresAt != nil && ak.ExpiresAt.Before(time.Now()) {
		cacheAPIKeyResult(hash, nil, ErrAPIKeyExpired)
		return nil, ErrAPIKeyExpired
	}

	// 检查配额（quota_usd > 0 时才检查）
	if ak.QuotaUsd > 0 && ak.UsedQuota >= ak.QuotaUsd {
		cacheAPIKeyResult(hash, nil, ErrAPIKeyQuota)
		return nil, ErrAPIKeyQuota
	}

	// 获取关联的 user 和 group ID
	u, err := ak.Edges.UserOrErr()
	if err != nil {
		cacheAPIKeyResult(hash, nil, ErrInvalidAPIKey)
		return nil, ErrInvalidAPIKey
	}
	if u.Status == user.StatusDisabled {
		cacheAPIKeyResult(hash, nil, ErrUserDisabled)
		return nil, ErrUserDisabled
	}
	g := ak.Edges.Group
	if g == nil {
		cacheAPIKeyResult(hash, nil, ErrAPIKeyGroupUnbound)
		return nil, ErrAPIKeyGroupUnbound
	}
	if g.IsExclusive && !userAllowedForGroup(g, u.ID) {
		cacheAPIKeyResult(hash, nil, ErrAPIKeyGroupExclusive)
		return nil, ErrAPIKeyGroupExclusive
	}

	info := &APIKeyInfo{
		KeyID:              ak.ID,
		UserID:             u.ID,
		UserEmail:          u.Email,
		GroupID:            g.ID,
		SellRate:           ak.SellRate,
		MaxRate:            ak.MaxRate,
		KeyMaxConcurrency:  ak.MaxConcurrency,
		UserMaxConcurrency: u.MaxConcurrency,

		UserBalance:           u.Balance,
		UserGroupRates:        u.GroupRates,
		GroupRateMultiplier:   g.RateMultiplier,
		GroupAlphaSearchPrice: g.AlphaSearchPrice,
		GroupAllowedClients:   g.AllowedClients,
		GroupFallbackID:       g.FallbackGroupID,
	}
	if tier := u.Edges.Tier; tier != nil {
		info.TierGroupRates = tier.Rates
	}
	cacheAPIKeyResult(hash, info, nil)
	return info, nil
}

// userAllowedForGroup 判断用户是否在专属分组的白名单内。
func userAllowedForGroup(g *ent.Group, userID int) bool {
	for _, u := range g.Edges.AllowedUsers {
		if u.ID == userID {
			return true
		}
	}
	return false
}

// cacheAPIKeyResult 把验证结果（成功或已知失败）写入缓存。
// 成功结果的 UserBalance / UsedQuota 会在 TTL 内"陈旧"，但缓存 TTL 很短（5s），
// 用户主流程不会明显感知到；balance 余额在并发扣费时的准确性由别处的数据库事务保证。
func cacheAPIKeyResult(hash string, info *APIKeyInfo, err error) {
	storeAPIKeyLocalCache(hash, info, err)
	storeAPIKeyRedisCache(hash, info, err)
}

func storeAPIKeyLocalCache(hash string, info *APIKeyInfo, err error) {
	if info == nil && err == nil {
		return
	}
	// 负结果进有界本地缓存：Redis 故障窗口内被拒 key 的重试不直穿 DB；
	// 超限后不再写入新负条目（内存有界），此时仅靠 Redis 负缓存兜底。
	if info == nil && !tryAcquireNegativeCacheSlot() {
		return
	}
	prev, loaded := apiKeyCache.Swap(hash, apiKeyCacheEntry{
		info:      info,
		err:       err,
		expiresAt: time.Now().Add(apiKeyCacheTTL),
	})
	// 覆盖了旧负条目（负→负 / 负→正）：释放旧条目占用的名额。
	if loaded {
		if e, ok := prev.(apiKeyCacheEntry); ok && e.info == nil {
			releaseNegativeCacheSlot()
		}
	}
}

// evictAPIKeyLocalCache 删除本地缓存条目并维护负条目计数（过期驱逐路径）。
func evictAPIKeyLocalCache(hash string) {
	prev, loaded := apiKeyCache.LoadAndDelete(hash)
	if !loaded {
		return
	}
	if e, ok := prev.(apiKeyCacheEntry); ok && e.info == nil {
		releaseNegativeCacheSlot()
	}
}

// tryAcquireNegativeCacheSlot 以 CAS 抢占一个负缓存名额；已达上限返回 false。
func tryAcquireNegativeCacheSlot() bool {
	for {
		n := negativeCacheCount.Load()
		if n >= maxNegativeCacheEntries {
			return false
		}
		if negativeCacheCount.CompareAndSwap(n, n+1) {
			return true
		}
	}
}

// releaseNegativeCacheSlot 释放一个负缓存名额（负条目被删除/覆盖时调用）。
func releaseNegativeCacheSlot() {
	negativeCacheCount.Add(-1)
}

func storeAPIKeyRedisCache(hash string, info *APIKeyInfo, err error) {
	if apiKeyRedis == nil {
		return
	}
	entry := apiKeyRedisEntry{}
	if info != nil {
		entry.Info = info
	} else if err != nil {
		entry.Err = apiKeyCacheErrorCode(err)
	}
	if entry.Info == nil && entry.Err == "" {
		return
	}
	raw, marshalErr := json.Marshal(entry)
	if marshalErr != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = apiKeyRedis.Set(ctx, apiKeyRedisCacheKey(hash), raw, apiKeyRedisCacheTTL).Err()
}

func loadAPIKeyCacheFromRedis(ctx context.Context, hash string) (*APIKeyInfo, error, bool) {
	if apiKeyRedis == nil {
		return nil, nil, false
	}
	raw, err := apiKeyRedis.Get(ctx, apiKeyRedisCacheKey(hash)).Bytes()
	if err != nil {
		return nil, nil, false
	}
	var entry apiKeyRedisEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		_ = apiKeyRedis.Del(ctx, apiKeyRedisCacheKey(hash)).Err()
		return nil, nil, false
	}
	if entry.Info != nil {
		return entry.Info, nil, true
	}
	if entry.Err != "" {
		if cacheErr := apiKeyCacheErrorFromCode(entry.Err); cacheErr != nil {
			return nil, cacheErr, true
		}
	}
	return nil, nil, false
}

func apiKeyRedisCacheKey(hash string) string {
	return "airgate:auth:v1:apikey:" + hash
}

func apiKeyCacheErrorCode(err error) string {
	switch err {
	case ErrInvalidAPIKey:
		return "invalid"
	case ErrAPIKeyExpired:
		return "expired"
	case ErrAPIKeyQuota:
		return "quota"
	case ErrAPIKeyGroupUnbound:
		return "group_unbound"
	case ErrAPIKeyGroupExclusive:
		return "group_exclusive"
	case ErrUserDisabled:
		return "user_disabled"
	default:
		return ""
	}
}

func apiKeyCacheErrorFromCode(code string) error {
	switch code {
	case "invalid":
		return ErrInvalidAPIKey
	case "expired":
		return ErrAPIKeyExpired
	case "quota":
		return ErrAPIKeyQuota
	case "group_unbound":
		return ErrAPIKeyGroupUnbound
	case "group_exclusive":
		return ErrAPIKeyGroupExclusive
	case "user_disabled":
		return ErrUserDisabled
	default:
		return nil
	}
}

// SetAPIKeyCacheRedis 配置跨进程 API Key 验证缓存。
func SetAPIKeyCacheRedis(rdb *redis.Client) {
	apiKeyRedis = rdb
}

// ValidateAdminAPIKey 验证管理员 API Key，返回 nil 表示验证通过。
func ValidateAdminAPIKey(ctx context.Context, db *ent.Client, key string) error {
	hash := HashAPIKey(key)

	// 从 settings 表查询 admin_api_key_hash
	s, err := db.Setting.Query().
		Where(
			entsetting.KeyEQ("admin_api_key_hash"),
			entsetting.GroupEQ("security"),
		).
		Only(ctx)
	if err != nil {
		return ErrInvalidAPIKey
	}
	// 常数时间比较：虽然比较对象是哈希（时序侧信道价值有限），仍统一防御口径。
	if s.Value == "" || subtle.ConstantTimeCompare([]byte(s.Value), []byte(hash)) != 1 {
		return ErrInvalidAPIKey
	}
	return nil
}
