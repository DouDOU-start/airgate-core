package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	appoauth "github.com/DouDOU-start/airgate-core/internal/app/oauth"
)

const (
	oauthCodeKeyPrefix  = "oauth:code:"
	oauthTokenKeyPrefix = "oauth:token:"
)

// OAuthGrantStore 使用 Redis 实现授权码与访问令牌的短期存储（TTL 到期自动失效）。
type OAuthGrantStore struct {
	rdb *redis.Client
}

// NewOAuthGrantStore 创建授权存储。
func NewOAuthGrantStore(rdb *redis.Client) *OAuthGrantStore {
	return &OAuthGrantStore{rdb: rdb}
}

// SaveCode 保存授权码。
func (s *OAuthGrantStore) SaveCode(ctx context.Context, code string, grant appoauth.CodeGrant, ttl time.Duration) error {
	data, err := json.Marshal(grant)
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, oauthCodeKeyPrefix+code, data, ttl).Err()
}

// TakeCode 原子取出并删除授权码（GETDEL 保证一次性使用）。
func (s *OAuthGrantStore) TakeCode(ctx context.Context, code string) (appoauth.CodeGrant, bool, error) {
	data, err := s.rdb.GetDel(ctx, oauthCodeKeyPrefix+code).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return appoauth.CodeGrant{}, false, nil
		}
		return appoauth.CodeGrant{}, false, err
	}
	var grant appoauth.CodeGrant
	if err := json.Unmarshal(data, &grant); err != nil {
		return appoauth.CodeGrant{}, false, err
	}
	return grant, true, nil
}

// SaveToken 保存访问令牌。
func (s *OAuthGrantStore) SaveToken(ctx context.Context, token string, grant appoauth.TokenGrant, ttl time.Duration) error {
	data, err := json.Marshal(grant)
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, oauthTokenKeyPrefix+token, data, ttl).Err()
}

// GetToken 读取访问令牌。
func (s *OAuthGrantStore) GetToken(ctx context.Context, token string) (appoauth.TokenGrant, bool, error) {
	data, err := s.rdb.Get(ctx, oauthTokenKeyPrefix+token).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return appoauth.TokenGrant{}, false, nil
		}
		return appoauth.TokenGrant{}, false, err
	}
	var grant appoauth.TokenGrant
	if err := json.Unmarshal(data, &grant); err != nil {
		return appoauth.TokenGrant{}, false, err
	}
	return grant, true, nil
}

var _ appoauth.GrantStore = (*OAuthGrantStore)(nil)
