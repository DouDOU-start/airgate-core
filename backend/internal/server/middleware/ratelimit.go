package middleware

import (
	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/ratelimit"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// RateLimit 限流中间件
// 从 Context 获取 user_id，调用限流器检查
func RateLimit(limiter *ratelimit.Limiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, exists := c.Get(CtxKeyUserID)
		if !exists {
			c.Next()
			return
		}

		// 使用请求路径作为平台标识
		platform := c.Request.URL.Path

		if err := limiter.Check(c.Request.Context(), userID.(int), platform); err != nil {
			response.Error(c, 429, 429, err.Error())
			c.Abort()
			return
		}
		c.Next()
	}
}
