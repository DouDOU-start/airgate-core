package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/apikey"
	entsetting "github.com/DouDOU-start/airgate-core/ent/setting"
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
// 失败结果（invalid / expired / quota / group_unbound）同样缓存——避免被拒的 key
// 反复打 DB 制造压力。
const apiKeyCacheTTL = 5 * time.Second

type apiKeyCacheEntry struct {
	info      *APIKeyInfo // 成功结果；失败时为 nil
	err       error       // 失败原因；成功时为 nil
	expiresAt time.Time
}

var (
	apiKeyCache   sync.Map // map[hash] → apiKeyCacheEntry
	apiKeyCacheMu sync.Mutex
	apiKeyRedis   *redis.Client
)

var (
	ErrInvalidAPIKey      = errors.New("无效的 API Key")
	ErrAPIKeyExpired      = errors.New("API Key 已过期")
	ErrAPIKeyQuota        = errors.New("API Key 配额已用尽")
	ErrAPIKeyGroupUnbound = errors.New("API Key 未绑定分组，请联系管理员重新绑定")
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
	KeyID         int
	KeyName       string
	UserID        int
	UserEmail     string
	GroupID       int
	GroupPlatform string
	QuotaUSD      float64
	UsedQuota     float64

	// SellRate Reseller 设置的销售倍率（>0 时启用 markup，独立于平台计费）
	SellRate float64

	// KeyMaxConcurrency API Key 级并发上限，0 表示不限制。
	// 在 forwarder 路径里会用 Redis 原子 SET 按 key_id 维度争抢槽位。
	KeyMaxConcurrency int

	// UserMaxConcurrency 用户级并发上限，0 表示不限制。
	// 同一个 user 下所有 API Key 共享这个配额——无论创建多少把 key，
	// 加起来同时在途的请求数不能超过这个值。与 KeyMaxConcurrency 是 AND 关系。
	UserMaxConcurrency int

	// 预加载字段，避免 forwarder 重复查询
	UserBalance            float64                      // 用户余额
	UserGroupRates         map[int64]float64            // 用户级专属倍率（按 group_id），用于 ResolveBillingRate 优先级链
	GroupRateMultiplier    float64                      // 分组倍率
	GroupServiceTier       string                       // 分组 service tier
	GroupForceInstructions string                       // 分组强制 instructions
	GroupPluginSettings    map[string]map[string]string // 分组插件级开关（claude_code_only 等）
}

// UserGroupRate 返回当前 key 所属分组在 user.group_rates 中的倍率（若存在）。
// 用于 ResolveBillingRate 的优先级链：用户级专属 > 分组档位。
func (i *APIKeyInfo) UserGroupRate() (float64, bool) {
	if i == nil || i.UserGroupRates == nil {
		return 0, false
	}
	r, ok := i.UserGroupRates[int64(i.GroupID)]
	if !ok || r <= 0 {
		return 0, false
	}
	return r, true
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

// ValidateAPIKeyForLogin 验证 API Key 用于 Web 登录（不要求绑定分组）。
// 返回 KeyID、KeyName、UserID 等基本信息。
func ValidateAPIKeyForLogin(ctx context.Context, db *ent.Client, key string) (*APIKeyInfo, error) {
	hash := HashAPIKey(key)

	ak, err := db.APIKey.Query().
		Where(
			apikey.KeyHash(hash),
			apikey.StatusEQ(apikey.StatusActive),
		).
		WithUser().
		Only(ctx)
	if err != nil {
		return nil, ErrInvalidAPIKey
	}

	if ak.ExpiresAt != nil && ak.ExpiresAt.Before(time.Now()) {
		return nil, ErrAPIKeyExpired
	}

	u, err := ak.Edges.UserOrErr()
	if err != nil {
		return nil, ErrInvalidAPIKey
	}

	return &APIKeyInfo{
		KeyID:     ak.ID,
		KeyName:   ak.Name,
		UserID:    u.ID,
		UserEmail: u.Email,
	}, nil
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
				slog.Debug("api_key_cache_hit", sdk.LogFieldAPIKeyID, e.info.KeyID)
			} else {
				slog.Debug("api_key_cache_hit_negative", sdk.LogFieldError, e.err)
			}
			return e.info, e.err
		}
		apiKeyCache.Delete(hash)
	}
	if info, err, ok := loadAPIKeyCacheFromRedis(ctx, hash); ok {
		if info != nil {
			slog.Debug("api_key_cache_hit_shared", sdk.LogFieldAPIKeyID, info.KeyID)
		} else {
			slog.Debug("api_key_cache_hit_negative_shared", sdk.LogFieldError, err)
		}
		storeAPIKeyLocalCache(hash, info, err)
		return info, err
	}
	slog.Debug("api_key_cache_miss")

	// 缓存未命中，查 DB
	ak, err := db.APIKey.Query().
		Where(
			apikey.KeyHash(hash),
			apikey.StatusEQ(apikey.StatusActive),
		).
		WithUser().
		WithGroup().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			// 真"key 不存在"：缓存负结果，避免被拒的 key 反复打 DB
			cacheAPIKeyResult(hash, nil, ErrInvalidAPIKey)
			return nil, ErrInvalidAPIKey
		}
		// DB 瞬时故障：不缓存，下次请求重试
		slog.Error("api_key_lookup_failed", sdk.LogFieldError, err)
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
	g := ak.Edges.Group
	if g == nil {
		cacheAPIKeyResult(hash, nil, ErrAPIKeyGroupUnbound)
		return nil, ErrAPIKeyGroupUnbound
	}

	info := &APIKeyInfo{
		KeyID:              ak.ID,
		KeyName:            ak.Name,
		UserID:             u.ID,
		UserEmail:          u.Email,
		GroupID:            g.ID,
		GroupPlatform:      g.Platform,
		QuotaUSD:           ak.QuotaUsd,
		UsedQuota:          ak.UsedQuota,
		SellRate:           ak.SellRate,
		KeyMaxConcurrency:  ak.MaxConcurrency,
		UserMaxConcurrency: u.MaxConcurrency,

		UserBalance:            u.Balance,
		UserGroupRates:         u.GroupRates,
		GroupRateMultiplier:    g.RateMultiplier,
		GroupServiceTier:       g.ServiceTier,
		GroupForceInstructions: g.ForceInstructions,
		GroupPluginSettings:    g.PluginSettings,
	}
	cacheAPIKeyResult(hash, info, nil)
	return info, nil
}

// cacheAPIKeyResult 把验证结果（成功或已知失败）写入缓存。
// 成功结果的 UserBalance / UsedQuota 会在 TTL 内"陈旧"，但缓存 TTL 很短（5s），
// 用户主流程不会明显感知到；balance 余额在并发扣费时的准确性由别处的数据库事务保证。
func cacheAPIKeyResult(hash string, info *APIKeyInfo, err error) {
	storeAPIKeyLocalCache(hash, info, err)
	storeAPIKeyRedisCache(hash, info, err)
}

func storeAPIKeyLocalCache(hash string, info *APIKeyInfo, err error) {
	apiKeyCache.Store(hash, apiKeyCacheEntry{
		info:      info,
		err:       err,
		expiresAt: time.Now().Add(apiKeyCacheTTL),
	})
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
	default:
		return nil
	}
}

// SetAPIKeyCacheRedis 配置跨进程 API Key 验证缓存。
func SetAPIKeyCacheRedis(rdb *redis.Client) {
	apiKeyRedis = rdb
}

// InvalidateAPIKeyCache 清除指定 key 的缓存（用于运维手动禁用 / 改配额等场景）。
// 传空字符串清除所有缓存。
func InvalidateAPIKeyCache(key string) {
	if key == "" {
		apiKeyCacheMu.Lock()
		apiKeyCache.Range(func(k, _ any) bool {
			apiKeyCache.Delete(k)
			return true
		})
		apiKeyCacheMu.Unlock()
		deleteAllAPIKeyRedisCache()
		return
	}
	hash := HashAPIKey(key)
	apiKeyCache.Delete(hash)
	if apiKeyRedis != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_, _ = apiKeyRedis.Del(ctx, apiKeyRedisCacheKey(hash)).Result()
	}
}

func deleteAllAPIKeyRedisCache() {
	if apiKeyRedis == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var cursor uint64
	for {
		keys, next, err := apiKeyRedis.Scan(ctx, cursor, "airgate:auth:v1:apikey:*", 100).Result()
		if err != nil {
			return
		}
		if len(keys) > 0 {
			_, _ = apiKeyRedis.Del(ctx, keys...).Result()
		}
		if next == 0 {
			return
		}
		cursor = next
	}
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
	if s.Value == "" || s.Value != hash {
		return ErrInvalidAPIKey
	}
	return nil
}
