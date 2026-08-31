package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/pkg/logx"
)

// Recovery 拦截 panic 并写入 500 JSON，替代 gin.Recovery() 以便接入结构化日志。
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				rid := RequestIDFromGinContext(c)
				slog.Error("panic_recovered",
					logx.LogFieldError, r,
					"stack", string(debug.Stack()),
					logx.LogFieldRequestID, rid,
					logx.LogFieldMethod, c.Request.Method,
					logx.LogFieldPath, RedactSensitiveRequestPath(c.Request.URL.Path),
				)
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"error":      "internal_server_error",
					"request_id": rid,
				})
			}
		}()
		c.Next()
	}
}
