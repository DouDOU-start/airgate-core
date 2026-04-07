package auth

import (
	"context"
	"time"
)

// User 认证域用户对象。
type User struct {
	ID              int
	Email           string
	Username        string
	PasswordHash    string
	Balance         float64
	Role            string
	MaxConcurrency  int
	GroupRates      map[int64]float64
	AllowedGroupIDs []int64
	Status          string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// LoginInput 登录输入。
type LoginInput struct {
	Email    string
	Password string
}

// RegisterInput 注册输入。
type RegisterInput struct {
	Email          string
	Password       string
	Username       string
	Balance        float64
	MaxConcurrency int
}

// AuthIdentity 表示当前登录身份。
type AuthIdentity struct {
	UserID   int
	Role     string
	Email    string
	APIKeyID int // >0 表示 API Key 登录
}

// LoginResult 登录/注册结果。
type LoginResult struct {
	Token string
	User  User
}

// CreateUserInput 创建用户输入。
type CreateUserInput struct {
	Email          string
	PasswordHash   string
	Username       string
	Role           string
	Status         string
	Balance        float64
	MaxConcurrency int
}

// Repository 认证域仓储接口。
type Repository interface {
	FindByEmail(context.Context, string) (User, error)
	EmailExists(context.Context, string) (bool, error)
	Create(context.Context, CreateUserInput) (User, error)
	FindByID(context.Context, int, bool) (User, error)
}
