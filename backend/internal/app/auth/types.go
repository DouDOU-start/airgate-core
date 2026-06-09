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

// LoginByAPIKeyInput API Key 登录输入。
type LoginByAPIKeyInput struct {
	Key string
}

// LoginByAPIKeyResult API Key 登录结果。
type LoginByAPIKeyResult struct {
	Token      string
	User       User
	APIKeyID   int
	APIKeyName string
	// API Key 维度字段（额度/已用/到期/倍率）
	QuotaUSD  float64
	UsedQuota float64
	Rate      float64
	ExpiresAt *time.Time
}

// RegisterInput 注册输入。
type RegisterInput struct {
	Email      string
	Password   string
	Username   string
	VerifyCode string
}

// SendVerifyCodeInput 发送验证码输入。
type SendVerifyCodeInput struct {
	Email string
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

// APIKeyLoginInfo API Key 登录验证后的基本信息。
type APIKeyLoginInfo struct {
	KeyID   int
	KeyName string
	UserID  int
}

// APIKeyBrief API Key 概要（额度/已用/到期/倍率）。
type APIKeyBrief struct {
	QuotaUSD  float64
	UsedQuota float64
	ExpiresAt *time.Time
	SellRate  float64
	GroupRate float64
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

// Setting 设置键值对（从设置服务透传）。
type Setting struct {
	Key   string
	Value string
}

// Repository 认证域仓储接口。
type Repository interface {
	FindByEmail(context.Context, string) (User, error)
	EmailExists(context.Context, string) (bool, error)
	Create(context.Context, CreateUserInput) (User, error)
	FindByID(context.Context, int, bool) (User, error)
	ValidateAPIKeySession(context.Context, int, int) (User, error)
	// ValidateAPIKeyForLogin 验证 API Key 用于 Web 登录（不要求绑定分组）。
	ValidateAPIKeyForLogin(ctx context.Context, key string) (APIKeyLoginInfo, error)
	// GetAPIKeyBrief 获取 API Key 概要信息（额度/已用/到期/倍率）。
	GetAPIKeyBrief(ctx context.Context, keyID int) (APIKeyBrief, error)
}
