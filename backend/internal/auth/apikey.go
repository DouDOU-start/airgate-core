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
	ErrInvalidAPIKey = errors.New("无效的 API Key")
	ErrAPIKeyExpired = errors.New("API Key 已过期")
	ErrAPIKeyQuota   = errors.New("API Key 配额已用尽")
)

const apiKeyPrefix = "sk-"

// APIKeyInfo API Key 验证后的信息
type APIKeyInfo struct {
	KeyID     int
	UserID    int
	GroupID   int
	QuotaUSD  float64
	UsedQuota float64
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
	g, err := ak.Edges.GroupOrErr()
	if err != nil {
		return nil, ErrInvalidAPIKey
	}

	return &APIKeyInfo{
		KeyID:     ak.ID,
		UserID:    u.ID,
		GroupID:   g.ID,
		QuotaUSD:  ak.QuotaUsd,
		UsedQuota: ak.UsedQuota,
	}, nil
}
