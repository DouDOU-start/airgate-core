// Package handler 提供 HTTP 请求处理器。
package handler

import (
	"errors"
	"log/slog"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/ent"
	appauth "github.com/DouDOU-start/airgate-core/internal/app/auth"
	appsettings "github.com/DouDOU-start/airgate-core/internal/app/settings"
	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/infra/mailer"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
)

// AuthHandler 认证相关 Handler。
type AuthHandler struct {
	service         *appauth.Service
	settingsService *appsettings.Service
	codeStore       *mailer.VerifyCodeStore
	db              *ent.Client
	jwtMgr          *auth.JWTManager
}

// NewAuthHandler 创建认证 Handler。
func NewAuthHandler(service *appauth.Service, settingsService *appsettings.Service, codeStore *mailer.VerifyCodeStore, db *ent.Client, jwtMgr *auth.JWTManager) *AuthHandler {
	return &AuthHandler{
		service:         service,
		settingsService: settingsService,
		codeStore:       codeStore,
		db:              db,
		jwtMgr:          jwtMgr,
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
	case errors.Is(err, appauth.ErrEmailAlreadyExists):
		return 400, err.Error()
	default:
		slog.Error("注册失败", "error", err)
		return 500, "注册失败"
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

	identity := appauth.AuthIdentity{
		UserID: userID,
		Role:   role,
		Email:  email,
	}
	if apiKeyID, exists := c.Get(middleware.CtxKeyAPIKeyID); exists {
		if id, ok := apiKeyID.(int); ok {
			identity.APIKeyID = id
		}
	}
	return identity, true
}
