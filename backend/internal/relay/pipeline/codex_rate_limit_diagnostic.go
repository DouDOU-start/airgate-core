package pipeline

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/pkg/logx"
	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	"github.com/DouDOU-start/airgate-core/internal/relay/cpa"
)

// ctxKeyRelayInboundRequestBody 保存客户端发送的原始请求体字节。
// 仅在请求生命周期内使用，禁止写入常规用量或失败留痕表。
const ctxKeyRelayInboundRequestBody = "relay_inbound_request_body"

// logRateLimitedAccountSuccess 记录账号限流竞态：
// 当前注册表已将账号标为 rate_limited，但此前拿到账号快照的在途请求仍成功。
// 日志只保留调度摘要，禁止记录请求正文、Header、上游 Header 或账号错误正文。
func (p *Pipeline) logRateLimitedAccountSuccess(
	c *gin.Context,
	selected *accountreg.Snapshot,
	model string,
	endpoint string,
	stream bool,
	payloadBytes int,
	result cpa.ForwardResult,
) {
	if p == nil || p.accounts == nil || c == nil || c.Request == nil || selected == nil {
		return
	}
	if !isSuccessfulCPAResult(result) {
		return
	}

	current, ok := p.accounts.Snapshot(selected.ID)
	if !ok || current == nil || current.State != accountreg.StateRateLimited {
		return
	}

	attrs := []any{
		logx.LogFieldRequestID, requestIDOf(c),
		logx.LogFieldAccountID, selected.ID,
		logx.LogFieldPlatform, selected.Platform,
		"account_type", selected.Type,
		"selected_account_state", selected.State,
		"current_account_state", current.State,
		"current_state_until", current.StateUntil,
		logx.LogFieldModel, model,
		"endpoint", endpoint,
		"payload_bytes", payloadBytes,
		"upstream_response_status", result.StatusCode,
		"stream", stream,
	}

	logx.LoggerFromContext(c.Request.Context()).Log(
		c.Request.Context(),
		slog.LevelWarn,
		"relay_rate_limited_account_request_succeeded",
		attrs...,
	)
}

func isSuccessfulCPAResult(result cpa.ForwardResult) bool {
	if result.BuildErr != nil || result.NetErr != nil || result.StreamErr != nil {
		return false
	}
	if result.StatusCode < http.StatusOK || result.StatusCode >= http.StatusMultipleChoices {
		return false
	}
	if result.Written {
		return result.Done
	}
	return true
}

func relayInboundRequestBody(c *gin.Context) []byte {
	if c == nil {
		return nil
	}
	value, ok := c.Get(ctxKeyRelayInboundRequestBody)
	if !ok {
		return nil
	}
	body, _ := value.([]byte)
	return body
}
