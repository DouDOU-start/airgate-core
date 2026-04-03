package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	corauth "github.com/DouDOU-start/airgate-core/internal/auth"
)

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

type authStubRepository struct {
	findByEmail func() (User, error)
	emailExists func() (bool, error)
	create      func(CreateUserInput) (User, error)
	findByID    func() (User, error)
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
