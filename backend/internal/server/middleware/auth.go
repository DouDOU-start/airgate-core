// Package middleware 提供 HTTP 中间件
package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// Context Key 常量
const (
	CtxKeyUserID  = "user_id"
	CtxKeyRole    = "role"
	CtxKeyEmail   = "email"
	CtxKeyKeyInfo = "api_key_info"
)

// JWTAuth JWT 认证中间件
// 从 Authorization: Bearer <token> 头解析 JWT，将 user_id、role 设置到 Context
func JWTAuth(jwtMgr *auth.JWTManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenStr := extractBearerToken(c)
		if tokenStr == "" {
			response.Unauthorized(c, "缺少认证 Token")
			c.Abort()
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
		c.Next()
	}
}

// APIKeyAuth API Key 认证中间件
// 从 Authorization: Bearer sk-xxx 头解析 API Key
func APIKeyAuth(db *ent.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := extractBearerToken(c)
		if key == "" {
			response.Unauthorized(c, "缺少 API Key")
			c.Abort()
			return
		}

		// 验证 API Key 格式
		if !strings.HasPrefix(key, "sk-") {
			response.Unauthorized(c, "无效的 API Key 格式")
			c.Abort()
			return
		}

		info, err := auth.ValidateAPIKey(c.Request.Context(), db, key)
		if err != nil {
			response.Unauthorized(c, err.Error())
			c.Abort()
			return
		}

		c.Set(CtxKeyUserID, info.UserID)
		c.Set(CtxKeyKeyInfo, info)
		c.Next()
	}
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
