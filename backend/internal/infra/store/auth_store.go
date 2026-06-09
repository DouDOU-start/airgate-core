package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/DouDOU-start/airgate-core/ent"
	entapikey "github.com/DouDOU-start/airgate-core/ent/apikey"
	entuser "github.com/DouDOU-start/airgate-core/ent/user"
	appauth "github.com/DouDOU-start/airgate-core/internal/app/auth"
)

// AuthStore 使用 Ent 实现认证仓储。
type AuthStore struct {
	db *ent.Client
}

// NewAuthStore 创建认证仓储。
func NewAuthStore(db *ent.Client) *AuthStore {
	return &AuthStore{db: db}
}

// FindByEmail 按邮箱查询用户。
func (s *AuthStore) FindByEmail(ctx context.Context, email string) (appauth.User, error) {
	item, err := s.db.User.Query().
		Where(entuser.EmailEQ(email)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return appauth.User{}, appauth.ErrUserNotFound
		}
		return appauth.User{}, err
	}
	return mapAuthUser(item), nil
}

// EmailExists 检查邮箱是否已存在。
func (s *AuthStore) EmailExists(ctx context.Context, email string) (bool, error) {
	return s.db.User.Query().Where(entuser.EmailEQ(email)).Exist(ctx)
}

// Create 创建用户。
func (s *AuthStore) Create(ctx context.Context, input appauth.CreateUserInput) (appauth.User, error) {
	builder := s.db.User.Create().
		SetEmail(input.Email).
		SetPasswordHash(input.PasswordHash).
		SetUsername(input.Username).
		SetRole(entuser.Role(input.Role)).
		SetStatus(entuser.Status(input.Status)).
		SetBalance(input.Balance).
		SetMaxConcurrency(input.MaxConcurrency)

	item, err := builder.Save(ctx)
	if err != nil {
		return appauth.User{}, err
	}
	return mapAuthUser(item), nil
}

// FindByID 按 ID 查询用户。
func (s *AuthStore) FindByID(ctx context.Context, id int, withAllowedGroups bool) (appauth.User, error) {
	query := s.db.User.Query().Where(entuser.IDEQ(id))
	if withAllowedGroups {
		query = query.WithAllowedGroups()
	}

	item, err := query.Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return appauth.User{}, appauth.ErrUserNotFound
		}
		return appauth.User{}, err
	}
	return mapAuthUser(item), nil
}

// ValidateAPIKeySession 校验 API Key scoped JWT 仍然对应一把有效的 Key 和 active 用户。
func (s *AuthStore) ValidateAPIKeySession(ctx context.Context, userID, keyID int) (appauth.User, error) {
	ak, err := s.db.APIKey.Query().
		Where(
			entapikey.IDEQ(keyID),
			entapikey.StatusEQ(entapikey.StatusActive),
			entapikey.HasUserWith(entuser.IDEQ(userID)),
		).
		WithUser().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return appauth.User{}, appauth.ErrInvalidAPIKeySession
		}
		return appauth.User{}, err
	}
	if ak.ExpiresAt != nil && ak.ExpiresAt.Before(time.Now()) {
		return appauth.User{}, appauth.ErrInvalidAPIKeySession
	}

	user, err := ak.Edges.UserOrErr()
	if err != nil {
		return appauth.User{}, appauth.ErrInvalidAPIKeySession
	}
	if user.Status != entuser.StatusActive {
		return appauth.User{}, appauth.ErrUserDisabled
	}
	return mapAuthUser(user), nil
}

// ValidateAPIKeyForLogin 验证 API Key 用于 Web 登录（不要求绑定分组）。
func (s *AuthStore) ValidateAPIKeyForLogin(ctx context.Context, key string) (appauth.APIKeyLoginInfo, error) {
	hash := hashAPIKey(key)

	ak, err := s.db.APIKey.Query().
		Where(
			entapikey.KeyHash(hash),
			entapikey.StatusEQ(entapikey.StatusActive),
		).
		WithUser().
		Only(ctx)
	if err != nil {
		return appauth.APIKeyLoginInfo{}, appauth.ErrInvalidAPIKey
	}

	if ak.ExpiresAt != nil && ak.ExpiresAt.Before(time.Now()) {
		return appauth.APIKeyLoginInfo{}, appauth.ErrAPIKeyExpired
	}

	u, err := ak.Edges.UserOrErr()
	if err != nil {
		return appauth.APIKeyLoginInfo{}, appauth.ErrInvalidAPIKey
	}

	return appauth.APIKeyLoginInfo{
		KeyID:   ak.ID,
		KeyName: ak.Name,
		UserID:  u.ID,
	}, nil
}

// GetAPIKeyBrief 获取 API Key 概要信息。
func (s *AuthStore) GetAPIKeyBrief(ctx context.Context, keyID int) (appauth.APIKeyBrief, error) {
	ak, err := s.db.APIKey.Query().
		Where(entapikey.IDEQ(keyID)).
		WithGroup().
		Only(ctx)
	if err != nil {
		return appauth.APIKeyBrief{}, err
	}

	brief := appauth.APIKeyBrief{
		QuotaUSD:  ak.QuotaUsd,
		UsedQuota: ak.UsedQuota,
		ExpiresAt: ak.ExpiresAt,
		SellRate:  ak.SellRate,
	}
	if g := ak.Edges.Group; g != nil {
		brief.GroupRate = g.RateMultiplier
	}
	return brief, nil
}

// hashAPIKey 对 API Key 进行 SHA256 哈希（与 auth 包的 HashAPIKey 逻辑一致）。
func hashAPIKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

func mapAuthUser(item *ent.User) appauth.User {
	result := appauth.User{
		ID:             item.ID,
		Email:          item.Email,
		Username:       item.Username,
		PasswordHash:   item.PasswordHash,
		Balance:        item.Balance,
		Role:           string(item.Role),
		MaxConcurrency: item.MaxConcurrency,
		GroupRates:     cloneAuthGroupRates(item.GroupRates),
		Status:         string(item.Status),
		CreatedAt:      item.CreatedAt,
		UpdatedAt:      item.UpdatedAt,
	}

	if groups := item.Edges.AllowedGroups; groups != nil {
		result.AllowedGroupIDs = make([]int64, 0, len(groups))
		for _, group := range groups {
			result.AllowedGroupIDs = append(result.AllowedGroupIDs, int64(group.ID))
		}
	}

	return result
}

func cloneAuthGroupRates(input map[int64]float64) map[int64]float64 {
	if input == nil {
		return nil
	}
	cloned := make(map[int64]float64, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

var _ appauth.Repository = (*AuthStore)(nil)
