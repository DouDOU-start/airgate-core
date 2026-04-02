package store

import (
	"context"

	"github.com/DouDOU-start/airgate-core/ent"
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
		SetStatus(entuser.Status(input.Status))

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

// SetTOTPSecret 保存 TOTP 密钥。
func (s *AuthStore) SetTOTPSecret(ctx context.Context, id int, secret string) error {
	if err := s.db.User.UpdateOneID(id).SetTotpSecret(secret).Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return appauth.ErrUserNotFound
		}
		return err
	}
	return nil
}

// ClearTOTPSecret 清空 TOTP 密钥。
func (s *AuthStore) ClearTOTPSecret(ctx context.Context, id int) error {
	if err := s.db.User.UpdateOneID(id).ClearTotpSecret().Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return appauth.ErrUserNotFound
		}
		return err
	}
	return nil
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
		TOTPSecret:     item.TotpSecret,
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
