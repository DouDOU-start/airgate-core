package auth

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"

	corauth "github.com/DouDOU-start/airgate-core/internal/auth"
)

// Service 提供认证域用例编排。
type Service struct {
	repo   Repository
	jwtMgr *corauth.JWTManager
}

// NewService 创建认证服务。
func NewService(repo Repository, jwtMgr *corauth.JWTManager) *Service {
	return &Service{
		repo:   repo,
		jwtMgr: jwtMgr,
	}
}

// Login 用户登录。
func (s *Service) Login(ctx context.Context, input LoginInput) (LoginResult, error) {
	user, err := s.repo.FindByEmail(ctx, input.Email)
	if err != nil {
		return LoginResult{}, ErrInvalidCredentials
	}

	if user.Status != "active" {
		return LoginResult{}, ErrUserDisabled
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(input.Password)); err != nil {
		return LoginResult{}, ErrInvalidCredentials
	}

	if hasTOTP(user) {
		if input.TOTPCode == "" {
			return LoginResult{}, ErrTOTPCodeRequired
		}
		if !corauth.ValidateCode(totpSecret(user), input.TOTPCode) {
			return LoginResult{}, ErrInvalidTOTPCode
		}
	}

	token, err := s.jwtMgr.GenerateToken(user.ID, user.Role, user.Email)
	if err != nil {
		return LoginResult{}, err
	}

	return LoginResult{
		Token: token,
		User:  user,
	}, nil
}

// Register 用户注册。
func (s *Service) Register(ctx context.Context, input RegisterInput) (LoginResult, error) {
	exists, err := s.repo.EmailExists(ctx, input.Email)
	if err != nil {
		return LoginResult{}, err
	}
	if exists {
		return LoginResult{}, ErrEmailAlreadyExists
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return LoginResult{}, err
	}

	user, err := s.repo.Create(ctx, CreateUserInput{
		Email:        input.Email,
		PasswordHash: string(hash),
		Username:     input.Username,
		Role:         "user",
		Status:       "active",
	})
	if err != nil {
		return LoginResult{}, err
	}

	token, err := s.jwtMgr.GenerateToken(user.ID, user.Role, user.Email)
	if err != nil {
		return LoginResult{}, err
	}

	return LoginResult{
		Token: token,
		User:  user,
	}, nil
}

// TOTPSetup 生成并保存 TOTP 密钥。
func (s *Service) TOTPSetup(ctx context.Context, identity AuthIdentity) (TOTPSetupResult, error) {
	user, err := s.repo.FindByID(ctx, identity.UserID, false)
	if err != nil {
		return TOTPSetupResult{}, err
	}
	if hasTOTP(user) {
		return TOTPSetupResult{}, ErrTOTPAlreadyEnabled
	}

	secret, uri, err := corauth.GenerateSecret(identity.Email)
	if err != nil {
		return TOTPSetupResult{}, fmt.Errorf("%w: %v", ErrGenerateTOTPSecretFailed, err)
	}

	if err := s.repo.SetTOTPSecret(ctx, identity.UserID, secret); err != nil {
		return TOTPSetupResult{}, fmt.Errorf("%w: %v", ErrSaveTOTPSecretFailed, err)
	}

	return TOTPSetupResult{
		Secret: secret,
		URI:    uri,
	}, nil
}

// TOTPVerify 验证 TOTP 验证码。
func (s *Service) TOTPVerify(ctx context.Context, userID int, code string) error {
	user, err := s.repo.FindByID(ctx, userID, false)
	if err != nil {
		return err
	}
	if !hasTOTP(user) {
		return ErrTOTPNotSetup
	}
	if !corauth.ValidateCode(totpSecret(user), code) {
		return ErrVerificationCodeInvalid
	}
	return nil
}

// TOTPDisable 禁用 TOTP。
func (s *Service) TOTPDisable(ctx context.Context, userID int, code string) error {
	user, err := s.repo.FindByID(ctx, userID, false)
	if err != nil {
		return err
	}
	if !hasTOTP(user) {
		return ErrTOTPNotEnabled
	}
	if !corauth.ValidateCode(totpSecret(user), code) {
		return ErrVerificationCodeInvalid
	}
	return s.repo.ClearTOTPSecret(ctx, userID)
}

// RefreshToken 刷新 JWT。
func (s *Service) RefreshToken(identity AuthIdentity) (string, error) {
	return s.jwtMgr.GenerateToken(identity.UserID, identity.Role, identity.Email)
}

// IsUserMissing 判断错误是否为用户不存在。
func IsUserMissing(err error) bool {
	return errors.Is(err, ErrUserNotFound)
}
