// Package auth 提供认证相关功能（JWT、API Key）
package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrInvalidToken = errors.New("无效的 token")
	ErrTokenExpired = errors.New("token 已过期")
)

// Claims JWT 自定义声明
type Claims struct {
	UserID int    `json:"user_id"`
	Role   string `json:"role"`
	Email  string `json:"email"`
	jwt.RegisteredClaims
}

// JWTManager JWT 管理器
type JWTManager struct {
	secret     []byte
	expireHour int
}

// NewJWTManager 创建 JWT 管理器
func NewJWTManager(secret string, expireHour int) *JWTManager {
	if expireHour <= 0 {
		expireHour = 24
	}
	return &JWTManager{
		secret:     []byte(secret),
		expireHour: expireHour,
	}
}

// GenerateToken 签发 JWT Token
func (m *JWTManager) GenerateToken(userID int, role, email string) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID: userID,
		Role:   role,
		Email:  email,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Duration(m.expireHour) * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(now),
			Issuer:    "airgate",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(m.secret)
}

// ParseToken 验证并解析 JWT Token
func (m *JWTManager) ParseToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return m.secret, nil
	})
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, ErrInvalidToken
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// RefreshToken 刷新 Token（基于旧 Claims 签发新 Token）
func (m *JWTManager) RefreshToken(claims *Claims) (string, error) {
	return m.GenerateToken(claims.UserID, claims.Role, claims.Email)
}
