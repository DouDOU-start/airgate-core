package handler

import (
	"errors"
	"log/slog"

	appapikey "github.com/DouDOU-start/airgate-core/internal/app/apikey"
	appoauth "github.com/DouDOU-start/airgate-core/internal/app/oauth"
)

// OAuthHandler OAuth 应用接入：管理端客户端 CRUD + 用户端授权 + 协议端点（token/userinfo/provision-key）。
type OAuthHandler struct {
	service *appoauth.Service
}

// NewOAuthHandler 创建 OAuthHandler。
func NewOAuthHandler(service *appoauth.Service) *OAuthHandler {
	return &OAuthHandler{service: service}
}

// handleError 管理面/用户端（信封响应）错误映射。
func (h *OAuthHandler) handleError(logMessage, publicMessage string, err error) (int, string) {
	switch {
	case errors.Is(err, appoauth.ErrClientNotFound):
		return 404, err.Error()
	case errors.Is(err, appoauth.ErrClientDisabled),
		errors.Is(err, appoauth.ErrRedirectURIMismatch),
		errors.Is(err, appoauth.ErrInvalidRedirectURI),
		errors.Is(err, appoauth.ErrPKCERequired):
		return 400, err.Error()
	default:
		slog.Error(logMessage, "error", err)
		return 500, publicMessage
	}
}

// oauthProtocolError 协议端点（RFC 6749 原始形态）错误映射：HTTP 状态码 + error 码。
func oauthProtocolError(err error) (int, string) {
	switch {
	case errors.Is(err, appoauth.ErrUnsupportedGrantType):
		return 400, "unsupported_grant_type"
	case errors.Is(err, appoauth.ErrInvalidGrant):
		return 400, "invalid_grant"
	case errors.Is(err, appoauth.ErrInvalidClientSecret),
		errors.Is(err, appoauth.ErrClientNotFound),
		errors.Is(err, appoauth.ErrClientDisabled):
		return 401, "invalid_client"
	case errors.Is(err, appoauth.ErrInvalidToken):
		return 401, "invalid_token"
	case errors.Is(err, appoauth.ErrUserDisabled),
		errors.Is(err, appapikey.ErrProvisionedKeyDisabled):
		return 403, "access_denied"
	case errors.Is(err, appapikey.ErrGroupNotFound),
		errors.Is(err, appapikey.ErrGroupForbidden),
		errors.Is(err, appapikey.ErrNoDefaultGroup):
		return 400, "invalid_request"
	default:
		slog.Error("oauth_protocol_error", "error", err)
		return 500, "server_error"
	}
}
