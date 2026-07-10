package pipeline

import (
	"math"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/relay/errfmt"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
)

// ctxKeyEntryProtocol gin ctx 中的入口协议键：各入口 handler 进入即设置，
// 其后所有网关自产错误（writeError 及其派生）按该协议出原生形态（errfmt.Render）。
const ctxKeyEntryProtocol = "relay_entry_protocol"

// setEntryProtocol 在入口 handler 最前面调用，标记本请求的入口协议。
func setEntryProtocol(c *gin.Context, protocol string) {
	c.Set(ctxKeyEntryProtocol, protocol)
}

// entryProtocolOf 读取入口协议；未设置（装配错误/非转发路径）按 openai 兜底。
func entryProtocolOf(c *gin.Context) string {
	if v := c.GetString(ctxKeyEntryProtocol); v != "" {
		return v
	}
	return registry.ProtocolOpenAI
}

// requestIDOf 读取入口中间件生成的请求级 request_id；未装配时为空串。
func requestIDOf(c *gin.Context) string {
	return c.GetString(middleware.CtxKeyRequestID)
}

// writeError 按入口协议写出网关自产错误的原生形态错误体
// （上游错误不经此函数，走 writeUpstreamError 语义重建路径）。
func writeError(c *gin.Context, status int, errType, code, message string) {
	c.JSON(status, errfmt.Render(entryProtocolOf(c), status, errType, code, message, requestIDOf(c)))
}

// writeRateLimitError 写出 429 错误体并携带 Retry-After 头（秒向上取整，最小 1）。
func writeRateLimitError(c *gin.Context, code, message string, retryAfter time.Duration) {
	seconds := int(math.Ceil(retryAfter.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	c.Header("Retry-After", strconv.Itoa(seconds))
	writeError(c, 429, "rate_limit_error", code, message)
}
