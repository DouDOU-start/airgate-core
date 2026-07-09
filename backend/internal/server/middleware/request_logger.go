package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

// CtxKeyRequestID 在 gin.Context 中存放 request_id 的键名。
const CtxKeyRequestID = "request_id"

// maxRequestIDLen 客户端自报 X-Request-ID 的落库/回显长度上限（字节）。
// 自生成 UUID 为 36 字符；截断按字节即可——合法 request id 应为 ASCII，
// 超长本身已属异常输入。
const maxRequestIDLen = 64

// 业务层往 gin.Context 写入的"访问日志富化字段"。RequestLogger 在收尾时读取这些键
// 把它们合并进 http_request 那一行，避免再单独打 forward_request_completed 重复信息。
const (
	CtxKeyAccessModel     = "access_log_model"
	CtxKeyAccessPlatform  = "access_log_platform"
	CtxKeyAccessAccountID = "access_log_account_id"
	CtxKeyAccessAttempts  = "access_log_attempts"
)

// healthPaths 记录健康检查路径，避免污染访问日志。
var healthPaths = map[string]struct{}{
	"/healthz": {},
	"/health":  {},
	"/readyz":  {},
}

// RequestLogger 输出 http_request 访问日志，并在 ctx/header 上传播 request_id。
func RequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		// 抽取或生成 request_id，写入响应头供客户端排错。
		// X-Request-ID 为攻击者可控头且无上限：usage_logs.request_id 带 btree 索引，
		// 超长值会令索引写入报错、整批计费 INSERT 失败（变相免费用量），此处统一截断。
		rid := sdk.ExtractOrGenerateRequestID(c.Request.Header)
		if len(rid) > maxRequestIDLen {
			rid = rid[:maxRequestIDLen]
		}
		c.Header(sdk.HeaderRequestID, rid)
		c.Set(CtxKeyRequestID, rid)

		// 把 rid + 派生 logger 注入 std context，让下游 handler 与插件链路自动带上
		ctx := sdk.WithRequestID(c.Request.Context(), rid)
		ctx, _ = sdk.LoggerWithRequestID(ctx)
		c.Request = c.Request.WithContext(ctx)

		c.Next()

		duration := time.Since(start)
		status := c.Writer.Status()
		path := c.Request.URL.Path

		// 根据 path/状态码挑选日志级别
		level := slog.LevelInfo
		if _, isHealth := healthPaths[path]; isHealth {
			level = slog.LevelDebug
		} else if status >= 500 {
			level = slog.LevelError
		} else if status >= 400 {
			level = slog.LevelWarn
		}

		// 鉴权后才会塞 user_id；未登录请求记 0 占位
		var userID int
		if v, ok := c.Get(CtxKeyUserID); ok {
			switch n := v.(type) {
			case int:
				userID = n
			case int64:
				userID = int(n)
			}
		}

		attrs := []any{
			sdk.LogFieldMethod, c.Request.Method,
			sdk.LogFieldPath, path,
			sdk.LogFieldStatus, status,
			sdk.LogFieldDurationMs, duration.Milliseconds(),
			sdk.LogFieldUserID, userID,
			"ip", c.ClientIP(),
			"bytes_out", c.Writer.Size(),
			sdk.LogFieldRequestID, rid,
		}
		// 业务层（如 forwarder）已写回的转发上下文，合并进 http_request 那一行
		if v, ok := c.Get(CtxKeyAccessModel); ok {
			if s, ok := v.(string); ok && s != "" {
				attrs = append(attrs, sdk.LogFieldModel, s)
			}
		}
		if v, ok := c.Get(CtxKeyAccessPlatform); ok {
			if s, ok := v.(string); ok && s != "" {
				attrs = append(attrs, sdk.LogFieldPlatform, s)
			}
		}
		if v, ok := c.Get(CtxKeyAccessAccountID); ok {
			if n, ok := v.(int); ok && n > 0 {
				attrs = append(attrs, sdk.LogFieldAccountID, n)
			}
		}
		if v, ok := c.Get(CtxKeyAccessAttempts); ok {
			if n, ok := v.(int); ok && n > 1 {
				attrs = append(attrs, "attempts", n)
			}
		}
		slog.Default().Log(c.Request.Context(), level, "http_request", attrs...)
	}
}

// RequestIDFromGinContext 从 gin.Context 读取 request_id，便于其它中间件复用。
func RequestIDFromGinContext(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if v, ok := c.Get(CtxKeyRequestID); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return sdk.RequestIDFromContext(c.Request.Context())
}
