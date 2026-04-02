// Package handler 提供 HTTP 请求处理器。
package handler

import (
	"errors"
	"log/slog"

	"github.com/gin-gonic/gin"

	appauth "github.com/DouDOU-start/airgate-core/internal/app/auth"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
)

// AuthHandler 认证相关 Handler。
type AuthHandler struct {
	service *appauth.Service
}

// NewAuthHandler 创建认证 Handler。
func NewAuthHandler(service *appauth.Service) *AuthHandler {
	return &AuthHandler{service: service}
}

func (h *AuthHandler) handleLoginError(err error) (int, string, bool) {
	switch {
	case errors.Is(err, appauth.ErrInvalidCredentials):
		return 401, err.Error(), true
	case errors.Is(err, appauth.ErrUserDisabled):
		return 403, err.Error(), true
	case errors.Is(err, appauth.ErrTOTPCodeRequired):
		return 400, err.Error(), true
	case errors.Is(err, appauth.ErrInvalidTOTPCode):
		return 401, err.Error(), true
	default:
		slog.Error("登录失败", "error", err)
		return 500, "登录失败", false
	}
}

func (h *AuthHandler) handleRegisterError(err error) (int, string) {
	switch {
	case errors.Is(err, appauth.ErrEmailAlreadyExists):
		return 400, err.Error()
	default:
		slog.Error("注册失败", "error", err)
		return 500, "注册失败"
	}
}

func (h *AuthHandler) handleTOTPSetupError(err error) (int, string) {
	switch {
	case errors.Is(err, appauth.ErrTOTPAlreadyEnabled):
		return 400, err.Error()
	case errors.Is(err, appauth.ErrGenerateTOTPSecretFailed):
		return 500, appauth.ErrGenerateTOTPSecretFailed.Error()
	case errors.Is(err, appauth.ErrSaveTOTPSecretFailed):
		return 500, appauth.ErrSaveTOTPSecretFailed.Error()
	case appauth.IsUserMissing(err):
		return 500, "获取用户信息失败"
	default:
		slog.Error("启用 TOTP 失败", "error", err)
		return 500, "保存 TOTP 密钥失败"
	}
}

func (h *AuthHandler) handleTOTPVerifyError(err error) (int, string) {
	switch {
	case errors.Is(err, appauth.ErrTOTPNotSetup),
		errors.Is(err, appauth.ErrVerificationCodeInvalid):
		return 400, err.Error()
	case appauth.IsUserMissing(err):
		return 500, "获取用户信息失败"
	default:
		slog.Error("TOTP 验证失败", "error", err)
		return 500, "获取用户信息失败"
	}
}

func (h *AuthHandler) handleTOTPDisableError(err error) (int, string) {
	switch {
	case errors.Is(err, appauth.ErrTOTPNotEnabled),
		errors.Is(err, appauth.ErrVerificationCodeInvalid):
		return 400, err.Error()
	case appauth.IsUserMissing(err):
		return 500, "获取用户信息失败"
	default:
		slog.Error("禁用 TOTP 失败", "error", err)
		return 500, "禁用 TOTP 失败"
	}
}

func authIdentityFromContext(c *gin.Context) (appauth.AuthIdentity, bool) {
	userIDRaw, exists := c.Get(middleware.CtxKeyUserID)
	if !exists {
		return appauth.AuthIdentity{}, false
	}
	roleRaw, exists := c.Get(middleware.CtxKeyRole)
	if !exists {
		return appauth.AuthIdentity{}, false
	}
	emailRaw, exists := c.Get(middleware.CtxKeyEmail)
	if !exists {
		return appauth.AuthIdentity{}, false
	}

	userID, ok := userIDRaw.(int)
	if !ok {
		return appauth.AuthIdentity{}, false
	}
	role, ok := roleRaw.(string)
	if !ok {
		return appauth.AuthIdentity{}, false
	}
	email, ok := emailRaw.(string)
	if !ok {
		return appauth.AuthIdentity{}, false
	}

	return appauth.AuthIdentity{
		UserID: userID,
		Role:   role,
		Email:  email,
	}, true
}
