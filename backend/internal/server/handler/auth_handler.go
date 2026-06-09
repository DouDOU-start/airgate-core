// Package handler 提供 HTTP 请求处理器。
package handler

import (
	"errors"
	"log/slog"

	appauth "github.com/DouDOU-start/airgate-core/internal/app/auth"
	"github.com/DouDOU-start/airgate-core/internal/auth"
)

// AuthHandler 认证相关 Handler。
type AuthHandler struct {
	service *appauth.Service
	jwtMgr  *auth.JWTManager
}

// NewAuthHandler 创建认证 Handler。
func NewAuthHandler(service *appauth.Service, jwtMgr *auth.JWTManager) *AuthHandler {
	return &AuthHandler{
		service: service,
		jwtMgr:  jwtMgr,
	}
}

func (h *AuthHandler) handleLoginError(err error) (int, string, bool) {
	switch {
	case errors.Is(err, appauth.ErrInvalidCredentials):
		return 401, err.Error(), true
	case errors.Is(err, appauth.ErrUserDisabled):
		return 403, err.Error(), true
	default:
		slog.Error("登录失败", "error", err)
		return 500, "登录失败", false
	}
}

func (h *AuthHandler) handleRegisterError(err error) (int, string) {
	switch {
	case errors.Is(err, appauth.ErrRegistrationDisabled):
		return 403, err.Error()
	case errors.Is(err, appauth.ErrVerifyCodeRequired):
		return 400, err.Error()
	case errors.Is(err, appauth.ErrVerifyCodeInvalid):
		return 400, err.Error()
	case errors.Is(err, appauth.ErrEmailAlreadyExists):
		return 400, err.Error()
	default:
		slog.Error("注册失败", "error", err)
		return 500, "注册失败"
	}
}

func (h *AuthHandler) handleAPIKeyLoginError(err error) (int, string) {
	switch {
	case errors.Is(err, appauth.ErrInvalidAPIKeyFormat):
		return 400, err.Error()
	case errors.Is(err, appauth.ErrAPIKeyExpired):
		return 401, "API Key 已过期"
	case errors.Is(err, appauth.ErrInvalidAPIKey):
		return 401, "无效的 API Key"
	case errors.Is(err, appauth.ErrUserDisabled):
		return 403, "用户已被禁用"
	default:
		slog.Error("API Key 登录失败", "error", err)
		return 500, "登录失败"
	}
}

func (h *AuthHandler) handleSendVerifyCodeError(err error) (int, string) {
	switch {
	case errors.Is(err, appauth.ErrEmailAlreadyExists):
		return 400, "该邮箱已被注册"
	case errors.Is(err, appauth.ErrMailerNotConfigured):
		return 500, "邮件服务未配置"
	case errors.Is(err, appauth.ErrSendMailFailed):
		return 500, err.Error()
	default:
		slog.Error("发送验证码失败", "error", err)
		return 500, "发送验证码失败"
	}
}
