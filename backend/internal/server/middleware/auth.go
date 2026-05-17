// Package middleware 提供 HTTP 中间件
package middleware

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// Context Key 常量
const (
	CtxKeyUserID   = "user_id"
	CtxKeyRole     = "role"
	CtxKeyEmail    = "email"
	CtxKeyKeyInfo  = "api_key_info"
	CtxKeyAPIKeyID = "jwt_api_key_id" // JWT 中的 API Key ID（API Key 登录场景）
)

// JWTAuth JWT 认证中间件
// 从 Authorization: Bearer <token> 头解析 JWT，将 user_id、role 设置到 Context。
// 管理员路由可传入 db，以额外支持 admin-xxx 管理员 API Key 作为替代凭证。
func JWTAuth(jwtMgr *auth.JWTManager, db ...*ent.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenStr := extractBearerToken(c)
		if tokenStr == "" {
			slog.Warn("jwt_validation_failed", sdk.LogFieldReason, "missing_token", sdk.LogFieldRequestID, RequestIDFromGinContext(c))
			response.Unauthorized(c, "缺少认证 Token")
			c.Abort()
			return
		}

		// 显式管理员 API Key 认证。仅调用方传入 db 的路由启用，避免 admin-xxx 在普通用户路由中生效。
		if auth.IsAdminAPIKey(tokenStr) && len(db) > 0 && db[0] != nil {
			if err := auth.ValidateAdminAPIKey(c.Request.Context(), db[0], tokenStr); err != nil {
				slog.Warn("admin_api_key_validation_failed", sdk.LogFieldReason, "invalid_admin_key", sdk.LogFieldError, err, sdk.LogFieldRequestID, RequestIDFromGinContext(c))
				response.Unauthorized(c, "管理员 API Key 无效")
				c.Abort()
				return
			}
			c.Set(CtxKeyUserID, 0)
			c.Set(CtxKeyRole, "admin")
			c.Set(CtxKeyEmail, "")

			ctx := c.Request.Context()
			logger := sdk.LoggerFromContext(ctx).With(sdk.LogFieldUserID, 0, "role", "admin")
			c.Request = c.Request.WithContext(sdk.WithLogger(ctx, logger))
			c.Next()
			return
		}

		claims, err := jwtMgr.ParseToken(tokenStr)
		if err != nil {
			slog.Warn("jwt_validation_failed", sdk.LogFieldReason, "invalid_or_expired", sdk.LogFieldError, err, sdk.LogFieldRequestID, RequestIDFromGinContext(c))
			response.Unauthorized(c, "Token 无效或已过期")
			c.Abort()
			return
		}

		c.Set(CtxKeyUserID, claims.UserID)
		c.Set(CtxKeyRole, claims.Role)
		c.Set(CtxKeyEmail, claims.Email)
		if claims.APIKeyID > 0 {
			c.Set(CtxKeyAPIKeyID, claims.APIKeyID)
		}
		// 用 user_id / role / api_key_id 派生新 logger 写回 ctx
		ctx := c.Request.Context()
		logger := sdk.LoggerFromContext(ctx).With(sdk.LogFieldUserID, claims.UserID, "role", claims.Role)
		if claims.APIKeyID > 0 {
			logger = logger.With(sdk.LogFieldAPIKeyID, claims.APIKeyID)
		}
		c.Request = c.Request.WithContext(sdk.WithLogger(ctx, logger))
		c.Next()
	}
}

// APIKeyAuth API Key 认证中间件
// 从 Authorization: Bearer sk-xxx 头解析 API Key
// 返回 OpenAI 兼容错误格式，确保 Claude Code 等客户端能正确识别
func APIKeyAuth(db *ent.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := extractBearerToken(c)
		if key == "" {
			slog.Warn("api_key_validation_failed", sdk.LogFieldReason, "missing_api_key", sdk.LogFieldRequestID, RequestIDFromGinContext(c))
			abortWithOpenAIError(c, http.StatusUnauthorized, "missing_api_key", "缺少 API Key")
			return
		}

		// 验证 API Key 格式
		if !strings.HasPrefix(key, "sk-") {
			slog.Warn("api_key_validation_failed", sdk.LogFieldReason, "invalid_format", sdk.LogFieldRequestID, RequestIDFromGinContext(c))
			abortWithOpenAIError(c, http.StatusUnauthorized, "invalid_api_key", "无效的 API Key 格式")
			return
		}

		info, err := auth.ValidateAPIKey(c.Request.Context(), db, key)
		if err != nil {
			code := "invalid_api_key"
			status := http.StatusUnauthorized
			reason := "invalid_key"
			switch err {
			case auth.ErrInvalidAPIKey:
				// 维持默认 401 / invalid_api_key
			case auth.ErrAPIKeyExpired:
				code = "api_key_expired"
				reason = "expired"
			case auth.ErrAPIKeyQuota:
				code = "insufficient_quota"
				status = http.StatusPaymentRequired
				reason = "quota_exceeded"
			case auth.ErrAPIKeyGroupUnbound:
				code = "api_key_misconfigured"
				status = http.StatusForbidden
				reason = "group_unbound"
			default:
				// DB 超时 / 连接池满 / ctx 取消 等服务端侧问题：返 503 让客户端重试，
				// 绝不能误判为"凭证无效"让客户端以为 key 被吊销。
				code = "service_unavailable"
				status = http.StatusServiceUnavailable
				reason = "service_unavailable"
			}
			slog.Warn("api_key_validation_failed", sdk.LogFieldReason, reason, sdk.LogFieldError, err, sdk.LogFieldStatus, status, sdk.LogFieldRequestID, RequestIDFromGinContext(c))
			abortWithOpenAIError(c, status, code, err.Error())
			return
		}

		c.Set(CtxKeyUserID, info.UserID)
		c.Set(CtxKeyKeyInfo, info)
		// 派生带 user_id/group_id/api_key_id 的 logger 写回 ctx，给后续插件链路复用
		ctx := c.Request.Context()
		logger := sdk.LoggerFromContext(ctx).With(
			sdk.LogFieldUserID, info.UserID,
			sdk.LogFieldGroupID, info.GroupID,
			sdk.LogFieldAPIKeyID, info.KeyID,
		)
		c.Request = c.Request.WithContext(sdk.WithLogger(ctx, logger))
		c.Next()
	}
}

// abortWithOpenAIError 返回 OpenAI 兼容的错误格式并终止请求
func abortWithOpenAIError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{
		"error": gin.H{
			"message": message,
			"type":    "authentication_error",
			"code":    code,
		},
	})
}

// AdminOnly 管理员权限中间件（需要在 JWTAuth 之后使用）
func AdminOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, exists := c.Get(CtxKeyRole)
		roleStr, ok := role.(string)
		if !exists || !ok || roleStr != "admin" {
			slog.Warn("admin_access_denied", sdk.LogFieldReason, "non_admin_role", sdk.LogFieldRequestID, RequestIDFromGinContext(c))
			response.Forbidden(c, "需要管理员权限")
			c.Abort()
			return
		}
		c.Next()
	}
}

// RequireRoles 要求 JWT 中的 role 属于给定集合。
func RequireRoles(roles ...string) gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		allowed[role] = struct{}{}
	}
	return func(c *gin.Context) {
		role, exists := c.Get(CtxKeyRole)
		roleStr, ok := role.(string)
		if !exists || !ok {
			slog.Warn("role_access_denied", sdk.LogFieldReason, "missing_role", sdk.LogFieldRequestID, RequestIDFromGinContext(c))
			response.Forbidden(c, "权限不足")
			c.Abort()
			return
		}
		if _, ok := allowed[roleStr]; !ok {
			slog.Warn("role_access_denied", sdk.LogFieldReason, "role_not_allowed", "role", roleStr, sdk.LogFieldRequestID, RequestIDFromGinContext(c))
			response.Forbidden(c, "权限不足")
			c.Abort()
			return
		}
		c.Next()
	}
}

// extractBearerToken 从 Authorization 头或 x-api-key 头提取 API Key
// 优先使用 Authorization: Bearer <token>，回退到 x-api-key（Anthropic 标准格式）
func extractBearerToken(c *gin.Context) string {
	header := c.GetHeader("Authorization")
	if header != "" {
		// 支持 "Bearer <token>" 格式
		parts := strings.SplitN(header, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			return strings.TrimSpace(parts[1])
		}
	}
	// 回退：Anthropic 标准 x-api-key 头
	if key := c.GetHeader("x-api-key"); key != "" {
		return key
	}
	return ""
}

// HasAPIKey 检查请求是否携带 API Key（Authorization: Bearer 或 x-api-key）
func HasAPIKey(c *gin.Context) bool {
	auth := c.GetHeader("Authorization")
	if len(auth) > 7 && strings.EqualFold(auth[:7], "Bearer ") {
		return true
	}
	return c.GetHeader("x-api-key") != ""
}
