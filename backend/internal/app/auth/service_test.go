package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"

	corauth "github.com/DouDOU-start/airgate-core/internal/auth"
)

func TestLoginRequiresTOTPCode(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error = %v", err)
	}
	secret := "JBSWY3DPEHPK3PXP"
	service := NewService(authStubRepository{
		findByEmail: func() (User, error) {
			return User{
				ID:           1,
				Email:        "u@test.com",
				PasswordHash: string(hash),
				Role:         "user",
				Status:       "active",
				TOTPSecret:   &secret,
			}, nil
		},
	}, corauth.NewJWTManager("secret", 24))

	_, err = service.Login(t.Context(), LoginInput{
		Email:    "u@test.com",
		Password: "password123",
	})
	if !errors.Is(err, ErrTOTPCodeRequired) {
		t.Fatalf("Login() error = %v, want %v", err, ErrTOTPCodeRequired)
	}
}

func TestRegisterRejectsDuplicateEmail(t *testing.T) {
	service := NewService(authStubRepository{
		emailExists: func() (bool, error) { return true, nil },
	}, corauth.NewJWTManager("secret", 24))

	_, err := service.Register(t.Context(), RegisterInput{
		Email:    "u@test.com",
		Password: "password123",
		Username: "u",
	})
	if !errors.Is(err, ErrEmailAlreadyExists) {
		t.Fatalf("Register() error = %v, want %v", err, ErrEmailAlreadyExists)
	}
}

func TestTOTPDisableClearsSecret(t *testing.T) {
	cleared := false
	secret := "JBSWY3DPEHPK3PXP"
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode() error = %v", err)
	}

	service := NewService(authStubRepository{
		findByID: func() (User, error) {
			return User{
				ID:         1,
				TOTPSecret: &secret,
				Status:     "active",
			}, nil
		},
		clearTOTPSecret: func() error {
			cleared = true
			return nil
		},
	}, corauth.NewJWTManager("secret", 24))

	if err := service.TOTPDisable(t.Context(), 1, code); err != nil {
		t.Fatalf("TOTPDisable() error = %v", err)
	}
	if !cleared {
		t.Fatalf("TOTPDisable() did not clear secret")
	}
}

type authStubRepository struct {
	findByEmail     func() (User, error)
	emailExists     func() (bool, error)
	create          func(CreateUserInput) (User, error)
	findByID        func() (User, error)
	setTOTPSecret   func(string) error
	clearTOTPSecret func() error
}

func (s authStubRepository) FindByEmail(_ context.Context, _ string) (User, error) {
	if s.findByEmail == nil {
		return User{}, ErrUserNotFound
	}
	return s.findByEmail()
}

func (s authStubRepository) EmailExists(_ context.Context, _ string) (bool, error) {
	if s.emailExists == nil {
		return false, nil
	}
	return s.emailExists()
}

func (s authStubRepository) Create(_ context.Context, input CreateUserInput) (User, error) {
	if s.create == nil {
		return User{
			ID:           1,
			Email:        input.Email,
			Username:     input.Username,
			PasswordHash: input.PasswordHash,
			Role:         input.Role,
			Status:       input.Status,
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		}, nil
	}
	return s.create(input)
}

func (s authStubRepository) FindByID(_ context.Context, _ int, _ bool) (User, error) {
	if s.findByID == nil {
		return User{}, ErrUserNotFound
	}
	return s.findByID()
}

func (s authStubRepository) SetTOTPSecret(_ context.Context, _ int, secret string) error {
	if s.setTOTPSecret == nil {
		_ = secret
		return nil
	}
	return s.setTOTPSecret(secret)
}

func (s authStubRepository) ClearTOTPSecret(_ context.Context, _ int) error {
	if s.clearTOTPSecret == nil {
		return nil
	}
	return s.clearTOTPSecret()
}
