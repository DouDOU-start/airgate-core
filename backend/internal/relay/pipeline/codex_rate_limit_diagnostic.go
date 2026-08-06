package pipeline

import (
	"bytes"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/pkg/logx"
	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	"github.com/DouDOU-start/airgate-core/internal/relay/cpa"
)

// ctxKeyRelayInboundRequestBody 保存客户端发送的原始请求体字节。
// 仅在请求生命周期内使用，禁止写入常规用量或失败留痕表。
const ctxKeyRelayInboundRequestBody = "relay_inbound_request_body"

// logCodexRateLimitedAccountSuccess 记录 Codex OAuth 账号限流竞态：
// 当前注册表已将账号标为 rate_limited，但此前拿到账号快照的在途请求仍成功。
//
// 这是显式的诊断日志，按需求完整记录客户端 Header 与原始/实际转发 Body，
// 因而可能包含 API Key、Cookie、用户提示词等敏感明文。触发条件必须保持严格：
// Codex OAuth + 当前账号状态为 rate_limited + 本次 CPA 请求完整成功。
func (p *Pipeline) logCodexRateLimitedAccountSuccess(
	c *gin.Context,
	selected *accountreg.Snapshot,
	model string,
	endpoint string,
	stream bool,
	forwardBody []byte,
	forwardHeaders http.Header,
	result cpa.ForwardResult,
) {
	if p == nil || p.accounts == nil || c == nil || c.Request == nil || selected == nil {
		return
	}
	if !isCodexOAuthAccount(selected) || !isSuccessfulCPAResult(result) {
		return
	}

	current, ok := p.accounts.Snapshot(selected.ID)
	if !ok || current == nil || current.State != accountreg.StateRateLimited {
		return
	}

	inboundBody := relayInboundRequestBody(c)
	attrs := []any{
		logx.LogFieldRequestID, requestIDOf(c),
		logx.LogFieldAccountID, selected.ID,
		"account_name", selected.Name,
		logx.LogFieldPlatform, selected.Platform,
		"account_type", selected.Type,
		"selected_account_state", selected.State,
		"current_account_state", current.State,
		"current_state_until", current.StateUntil,
		"current_state_error", current.ErrorMsg,
		logx.LogFieldModel, model,
		"endpoint", endpoint,
		logx.LogFieldMethod, c.Request.Method,
		logx.LogFieldPath, c.Request.URL.Path,
		"request_raw_query", c.Request.URL.RawQuery,
		"request_host", c.Request.Host,
		"request_proto", c.Request.Proto,
		"request_remote_addr", c.Request.RemoteAddr,
		"request_content_length", c.Request.ContentLength,
		"request_transfer_encoding", append([]string(nil), c.Request.TransferEncoding...),
		"inbound_request_headers", c.Request.Header.Clone(),
		"inbound_request_trailers", c.Request.Trailer.Clone(),
		"forward_request_headers", cloneHTTPHeader(forwardHeaders),
		"forward_request_body", string(forwardBody),
		"forward_request_body_bytes", len(forwardBody),
		"upstream_response_status", result.StatusCode,
		"upstream_response_headers", cloneHTTPHeader(result.Headers),
		"stream", stream,
	}
	// 实际转发体可能经过 Relay Hook 或 JSON 重新序列化。仅当原始字节与转发字节
	// 不同时额外记录原始体，既保留诊断价值，也避免完全相同时重复放大日志。
	if len(inboundBody) > 0 && !bytes.Equal(inboundBody, forwardBody) {
		attrs = append(attrs,
			"inbound_request_body", string(inboundBody),
			"inbound_request_body_bytes", len(inboundBody),
			"request_body_rewritten", true,
		)
	} else {
		attrs = append(attrs, "request_body_rewritten", false)
	}

	logx.LoggerFromContext(c.Request.Context()).Log(
		c.Request.Context(),
		slog.LevelWarn,
		"relay_codex_rate_limited_account_request_succeeded",
		attrs...,
	)
}

func isCodexOAuthAccount(account *accountreg.Snapshot) bool {
	if account == nil || !strings.EqualFold(strings.TrimSpace(account.Platform), "codex") {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(account.Type)) {
	case "", "oauth", "refresh_token", "setup_token", "session", "device":
		return true
	default:
		return false
	}
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

func cloneHTTPHeader(header http.Header) http.Header {
	if header == nil {
		return nil
	}
	return header.Clone()
}
