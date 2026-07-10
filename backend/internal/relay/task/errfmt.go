package task

import (
	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/relay/errfmt"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
)

// ctxKeyEntryProtocol gin ctx 中的入口协议键（与 pipeline 同键同语义：
// 各入口 handler 进入即设置，其后所有错误按该协议出原生形态）。
const ctxKeyEntryProtocol = "relay_entry_protocol"

// setEntryProtocol 在入口 handler 最前面调用，标记本请求的入口协议。
func setEntryProtocol(c *gin.Context, protocol string) {
	c.Set(ctxKeyEntryProtocol, protocol)
}

// entryProtocolOf 读取入口协议；未设置（装配错误）按 openai 兜底。
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

// writeError 按入口协议写出网关自产错误的原生形态错误体。
func writeError(c *gin.Context, status int, errType, code, message string) {
	c.JSON(status, errfmt.Render(entryProtocolOf(c), status, errType, code, message, requestIDOf(c)))
}

// writeUpstreamError 上游不可重试错误：语义保留、载体重建（口径同 pipeline）。
func writeUpstreamError(c *gin.Context, status int, up errfmt.UpstreamError) {
	c.JSON(status, errfmt.RenderUpstream(entryProtocolOf(c), status, up, requestIDOf(c)))
}
