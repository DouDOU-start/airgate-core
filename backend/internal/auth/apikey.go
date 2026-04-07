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
)

var (
	ErrInvalidAPIKey      = errors.New("无效的 API Key")
	ErrAPIKeyExpired      = errors.New("API Key 已过期")
	ErrAPIKeyQuota        = errors.New("API Key 配额已用尽")
	ErrAPIKeyGroupUnbound = errors.New("API Key 未绑定分组，请联系管理员重新绑定")
)

const apiKeyPrefix = "sk-"

// APIKeyInfo API Key 验证后的信息
type APIKeyInfo struct {
	KeyID         int
	KeyName       string
	UserID        int
	GroupID       int
	GroupPlatform string
	QuotaUSD      float64
	UsedQuota     float64

	// 预加载字段，避免 forwarder 重复查询
	UserBalance         float64 // 用户余额
	GroupRateMultiplier float64 // 分组倍率
	GroupServiceTier    string  // 分组 service tier
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
		KeyID:         ak.ID,
		KeyName:       ak.Name,
		UserID:        u.ID,
		GroupID:       g.ID,
		GroupPlatform: g.Platform,
		QuotaUSD:      ak.QuotaUsd,
		UsedQuota:     ak.UsedQuota,

		UserBalance:         u.Balance,
		GroupRateMultiplier: g.RateMultiplier,
		GroupServiceTier:    g.ServiceTier,
	}, nil
}
