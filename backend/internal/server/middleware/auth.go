// Package middleware 提供 HTTP 中间件
package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

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
// 同时支持管理员 API Key（admin-xxx 格式）作为备选认证方式。
func JWTAuth(jwtMgr *auth.JWTManager, db ...*ent.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenStr := extractBearerToken(c)
		if tokenStr == "" {
			response.Unauthorized(c, "缺少认证 Token")
			c.Abort()
			return
		}

		// 管理员 API Key 认证
		if auth.IsAdminAPIKey(tokenStr) && len(db) > 0 && db[0] != nil {
			if err := auth.ValidateAdminAPIKey(c.Request.Context(), db[0], tokenStr); err != nil {
				response.Unauthorized(c, "管理员 API Key 无效")
				c.Abort()
				return
			}
			c.Set(CtxKeyUserID, 0)
			c.Set(CtxKeyRole, "admin")
			c.Set(CtxKeyEmail, "")
			c.Next()
			return
		}

		claims, err := jwtMgr.ParseToken(tokenStr)
		if err != nil {
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
			abortWithOpenAIError(c, http.StatusUnauthorized, "missing_api_key", "缺少 API Key")
			return
		}

		// 验证 API Key 格式
		if !strings.HasPrefix(key, "sk-") {
			abortWithOpenAIError(c, http.StatusUnauthorized, "invalid_api_key", "无效的 API Key 格式")
			return
		}

		info, err := auth.ValidateAPIKey(c.Request.Context(), db, key)
		if err != nil {
			code := "invalid_api_key"
			status := http.StatusUnauthorized
			switch err {
			case auth.ErrAPIKeyExpired:
				code = "api_key_expired"
			case auth.ErrAPIKeyQuota:
				code = "insufficient_quota"
				status = http.StatusPaymentRequired
			case auth.ErrAPIKeyGroupUnbound:
				code = "api_key_misconfigured"
				status = http.StatusForbidden
			}
			abortWithOpenAIError(c, status, code, err.Error())
			return
		}

		c.Set(CtxKeyUserID, info.UserID)
		c.Set(CtxKeyKeyInfo, info)
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
		if !exists || role.(string) != "admin" {
			response.Forbidden(c, "需要管理员权限")
			c.Abort()
			return
		}
		c.Next()
	}
}

// extractBearerToken 从 Authorization 头提取 Bearer Token
func extractBearerToken(c *gin.Context) string {
	header := c.GetHeader("Authorization")
	if header == "" {
		return ""
	}
	// 支持 "Bearer <token>" 格式
	parts := strings.SplitN(header, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return strings.TrimSpace(parts[1])
	}
	return ""
}
