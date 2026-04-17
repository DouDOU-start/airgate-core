package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/apikey"
	entsetting "github.com/DouDOU-start/airgate-core/ent/setting"
)

var (
	ErrInvalidAPIKey      = errors.New("无效的 API Key")
	ErrAPIKeyExpired      = errors.New("API Key 已过期")
	ErrAPIKeyQuota        = errors.New("API Key 配额已用尽")
	ErrAPIKeyGroupUnbound = errors.New("API Key 未绑定分组，请联系管理员重新绑定")
)

const apiKeyPrefix = "sk-"
const adminKeyPrefix = "admin-"

// APIKeyInfo API Key 验证后的信息
type APIKeyInfo struct {
	KeyID         int
	KeyName       string
	UserID        int
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
	// 生成 32 字节随机数据
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	key = apiKeyPrefix + hex.EncodeToString(b)
	hash = HashAPIKey(key)
	return key, hash, nil
}

// HashAPIKey 对 API Key 进行 SHA256 哈希
func HashAPIKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

// GenerateAdminAPIKey 生成管理员 API Key，返回明文密钥和哈希。
func GenerateAdminAPIKey() (key string, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	key = adminKeyPrefix + hex.EncodeToString(b)
	hash = HashAPIKey(key)
	return key, hash, nil
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
		KeyID:   ak.ID,
		KeyName: ak.Name,
		UserID:  u.ID,
	}, nil
}

// ValidateAPIKey 验证 API Key 并返回关联信息
func ValidateAPIKey(ctx context.Context, db *ent.Client, key string) (*APIKeyInfo, error) {
	hash := HashAPIKey(key)

	// 查询 API Key，同时加载关联的 user 和 group
	ak, err := db.APIKey.Query().
		Where(
			apikey.KeyHash(hash),
			apikey.StatusEQ(apikey.StatusActive),
		).
		WithUser().
		WithGroup().
		Only(ctx)
	if err != nil {
		return nil, ErrInvalidAPIKey
	}

	// 检查过期时间
	if ak.ExpiresAt != nil && ak.ExpiresAt.Before(time.Now()) {
		return nil, ErrAPIKeyExpired
	}

	// 检查配额（quota_usd > 0 时才检查）
	if ak.QuotaUsd > 0 && ak.UsedQuota >= ak.QuotaUsd {
		return nil, ErrAPIKeyQuota
	}

	// 获取关联的 user 和 group ID
	u, err := ak.Edges.UserOrErr()
	if err != nil {
		return nil, ErrInvalidAPIKey
	}
	g := ak.Edges.Group
	if g == nil {
		return nil, ErrAPIKeyGroupUnbound
	}

	return &APIKeyInfo{
		KeyID:              ak.ID,
		KeyName:            ak.Name,
		UserID:             u.ID,
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
	}, nil
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
