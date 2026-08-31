package pipeline

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/errlog"
	"github.com/DouDOU-start/airgate-core/internal/pkg/logx"
	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/cpa"
	"github.com/DouDOU-start/airgate-core/internal/relay/cursor"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
	"github.com/DouDOU-start/airgate-core/internal/relay/errfmt"
	"github.com/DouDOU-start/airgate-core/internal/relay/outcome"
	"github.com/DouDOU-start/airgate-core/internal/relay/pricing"
	providertransport "github.com/DouDOU-start/airgate-core/internal/relay/transport"
	"github.com/DouDOU-start/airgate-core/internal/requestaudit"
)

// executeAccountAttempt 执行一次账号路径转发（CPA）。
// 槽位/RPM 由调用方在抢到后传入；本函数 defer 释放账号并发槽。
func (p *Pipeline) executeAccountAttempt(
	c *gin.Context,
	acc *accountreg.Snapshot,
	req *dto.ChatRequest,
	endpoint string,
	protocol string,
	payload []byte,
	start time.Time,
	requestID string,
	rpmMinute int64,
	auditRequest *requestaudit.Handle,
	sessionKey string,
	opts forwardOptions,
) attemptResult {
	defer func() {
		// 槽位释放异步化：ZREM 幂等，不必阻塞请求收尾/下一次 failover 尝试。
		go p.concurrency.ReleaseAccountSlot(context.Background(), acc.ID, requestID)
		if rec := recover(); rec != nil {
			p.rpm.DecrementAccountRPM(context.Background(), acc.ID, rpmMinute)
			panic(rec)
		}
	}()

	if p.cpa == nil && p.providerTransport == nil {
		return attemptResult{buildErr: errCPAUnavailable}
	}
	// Account-dependent request rewrites run only after routing has selected
	// this concrete account. Keep the shared failover payload untouched so a
	// later attempt cannot inherit Codex-only function history.
	payload = p.transformSelectedCodexAttempt(c.Request.Context(), c, acc, req, endpoint, protocol, payload, opts, requestID)

	providerHeaders := accountProviderHeadersForRequest(c, endpoint, opts.rawBody, opts.rawContentType)
	for name, values := range opts.providerHeaders {
		canonical := http.CanonicalHeaderKey(name)
		if values == nil {
			providerHeaders.Del(canonical)
			continue
		}
		providerHeaders[canonical] = append([]string(nil), values...)
	}
	if strings.EqualFold(strings.TrimSpace(endpoint), adaptor.EndpointCodexTurnCosts) {
		providerHeaders = codexTurnCostsProviderHeaders(providerHeaders)
	}
	method := strings.TrimSpace(opts.method)
	if method == "" {
		method = http.MethodPost
	}
	fwdReq := cpa.ForwardRequest{
		Account: cpa.AccountAuthInput{
			AccountID:   acc.ID,
			Name:        acc.Name,
			Platform:    acc.Platform,
			Type:        acc.Type,
			Credentials: acc.Credentials,
			ProxyURL:    acc.ProxyURL,
		},
		Model:         req.Model,
		UpstreamModel: acc.ResolveModel(req.Model),
		Endpoint:      endpoint,
		Method:        method,
		Path: codexProviderPathForAccount(cpa.AccountAuthInput{
			Platform:    acc.Platform,
			Type:        acc.Type,
			Credentials: acc.Credentials,
		}, endpoint, opts.providerPath),
		Query:                        cloneForwardQueryValues(opts.providerQueryDefaults),
		EntryProtocol:                protocol,
		Stream:                       req.Stream,
		Payload:                      payload,
		RawBody:                      opts.rawBody,
		RawContentType:               opts.rawContentType,
		Headers:                      providerHeaders,
		RequestStartedAt:             start,
		CursorSessionKey:             sessionKey,
		RemoteControlToken:           opts.remoteControlToken,
		RemoteControlServerID:        opts.remoteControlServerID,
		RemoteControlName:            opts.remoteControlName,
		RemoteControlProtocolVersion: opts.remoteControlProtocolVersion,
		InstallationID:               opts.installationID,
	}

	ctx := c.Request.Context()
	var auditTransport *requestaudit.RoundTripper
	if auditRequest != nil {
		target := requestaudit.Target{
			RouteKind: "account", AccountID: acc.ID, AccountName: acc.Name,
			AccountEmail: accountEmail(acc), AccountPlatform: acc.Platform, AccountType: acc.Type,
		}
		// Native Codex execution happens in a separate plugin process and cannot
		// inherit the in-process RoundTripper. Pass a controlled sink through the
		// provider context so the plugin can create/finish the real upstream
		// attempt without ever receiving credential headers in audit metadata.
		ctx = withProviderAuditSink(ctx, newNativeAuditSink(auditRequest, target))
		// Keep the legacy RoundTripper available for a CPA fallback. The account
		// proxy must remain on the ForwardRequest so native transport can use it;
		// executeProvider strips it only when entering CPA with this wrapper.
		if p.cpa != nil || pipelineHasCPATranslation(p.providerTransport) {
			var baseTransport http.RoundTripper
			var errBuild error
			if acc.Platform == "cursor" {
				// Cursor Agent 走 Connect-RPC over HTTP/2 双向流，必须强制 h2
				// ALPN；通用账号传输层会被上游 ALB 以 464 拒绝。
				baseTransport, errBuild = cursor.SharedH2Transport(acc.ProxyURL)
			} else {
				baseTransport, errBuild = p.accountAuditTransport(acc.ProxyURL)
			}
			if errBuild != nil {
				return attemptResult{auditErr: fmt.Errorf("构造账号代理审计传输层失败: %w", errBuild)}
			}
			// CPA 通过固定字符串上下文键接收最终网络层；同时清空 Auth.ProxyURL，
			// 避免 executor 的代理优先级绕过审计 RoundTripper。
			auditTransport = requestaudit.NewRoundTripper(baseTransport, auditRequest, target)
			//nolint:staticcheck
			ctx = context.WithValue(ctx, "cliproxy.roundtripper", auditTransport)
		}
	}
	cancel := context.CancelFunc(func() {})
	if !req.Stream {
		ctx, cancel = context.WithTimeout(ctx, nonStreamTimeout)
	}
	defer cancel()

	result := p.executeProvider(ctx, c, fwdReq)
	if auditTransport != nil && req.Stream && result.Done && result.StreamErr == nil &&
		result.NetErr == nil && result.BuildErr == nil && result.StatusCode >= 200 && result.StatusCode < 300 {
		// CPA 已识别协议终态时覆盖底层“未继续读到 HTTP EOF”的临时观测，
		// 避免把 executor 的正常提前收尾误报成上游流中断。
		auditTransport.MarkLatestStreamCompleted()
	}
	if req.Stream {
		logLargeAccountRequestTiming(c, acc, req.Model, endpoint, len(payload), result)
	}
	if isAuditWriteError(result.NetErr) || isAuditWriteError(result.BuildErr) || isAuditWriteError(result.StreamErr) {
		return attemptResult{auditErr: requestaudit.ErrWrite}
	}
	// 在 handleAccountOutcome 的成功分支调用 MarkActive 之前，捕获“账号已被并发请求
	// 标为限流，但当前在途请求仍成功”的通用调度摘要。
	p.logRateLimitedAccountSuccess(c, acc, req.Model, endpoint, req.Stream, len(payload), result)

	// refresh 后的凭证写回内存（+ 可选落库）。
	if len(result.RefreshedCredentials) > 0 && p.accounts != nil {
		p.accounts.UpdateCredentials(acc.ID, result.RefreshedCredentials)
	}

	return attemptResult{
		netErr:              result.NetErr,
		buildErr:            result.BuildErr,
		statusCode:          result.StatusCode,
		headers:             result.Headers,
		body:                result.Body,
		contentType:         result.ContentType,
		usage:               result.Usage,
		firstTokenMs:        result.FirstTokenMs,
		requestFirstTokenMs: result.RequestFirstTokenMs,
		written:             result.Written,
		responseStarted:     result.ResponseStarted,
		dataReceived:        result.DataReceived,
		streamErr:           result.StreamErr,
		done:                result.Done,
	}
}

// pipelineHasCPATranslation reports whether the configured provider transport
// can execute the legacy CPA translation path. This matters for embedders that
// inject only a CPAAdapter through ProviderTransport and leave the historical
// CPA field nil: request-audit must still wrap the real in-process HTTP call.
func pipelineHasCPATranslation(transport providertransport.ProviderTransport) bool {
	if transport == nil {
		return false
	}
	capabilities, ok := transport.(providertransport.CapabilityProvider)
	return ok && capabilities.Capabilities().Translation
}

func accountProviderHeaders(c *gin.Context) http.Header {
	return accountProviderHeadersForRequest(c, "", nil, "")
}

// accountProviderHeadersForRequest copies the safe Codex/OpenAI request
// headers and supplies a wire-aware Content-Type only when the caller did not
// provide one. Native Realtime call creation accepts either a raw SDP offer,
// a JSON backend envelope, or multipart data; the explicit inbound value (and
// its multipart boundary) always wins. The raw Content-Type is kept separately
// in forwardOptions because raw endpoints may be assembled by an internal
// caller rather than directly from c.Request.Header.
func accountProviderHeadersForRequest(c *gin.Context, endpoint string, rawBody []byte, rawContentType string) http.Header {
	out := make(http.Header)
	if c != nil && c.Request != nil {
		for _, name := range []string{
			"Accept", "Accept-Encoding", "Accept-Language", "Cache-Control",
			"Content-Type", "Content-Encoding", "If-Match", "If-None-Match", "User-Agent",
			"OpenAI-Beta", "OpenAI-Alpha", "OAI-Product-Sku",
			"OpenAI-Organization", "OpenAI-Project", "OpenAI-Version",
			"X-OpenAI-Fedramp", "X-OpenAI-Internal-Codex-Residency",
			"X-OpenAI-Client-User-Agent", "X-OpenAI-Client-Version", "X-Client-Version",
			"Originator", "X-Oai-Attestation", "X-OpenAI-Memgen-Request",
			"X-ResponsesAPI-Include-Timing-Metrics", "X-OpenAI-Internal-Codex-Responses-Lite",
			"Session-Id", "X-Session-Id", "Thread-Id", "X-Client-Request-Id", "X-OpenAI-Subagent",
			"X-Codex-Installation-Id", "X-Codex-Routing-Hint", "X-Codex-Turn-State",
			"X-Codex-Turn-Metadata", "X-Codex-Parent-Thread-Id", "X-Codex-Window-Id",
			"X-Codex-Beta-Features", "Traceparent", "Tracestate",
		} {
			if values := c.Request.Header.Values(name); len(values) > 0 {
				out[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
			}
		}
		// Codex CLI evolves its telemetry/routing headers independently of
		// Core releases (for example, new x-codex-turn-metadata fields).  Keep
		// forwarding all non-sensitive X-Codex/X-OpenAI headers rather than
		// silently dropping a newly introduced contract.  Credential-shaped and
		// hop-by-hop names remain blocked below, so this does not weaken the
		// selected-account boundary.
		for name, values := range c.Request.Header {
			if !isForwardableCodexHeaderName(name) || len(values) == 0 {
				continue
			}
			canonical := http.CanonicalHeaderKey(name)
			if _, exists := out[canonical]; exists {
				continue
			}
			out[canonical] = append([]string(nil), values...)
		}
	}
	// The official turn-cost reconciliation client projects only provider
	// scope headers (organization/project). Authentication is supplied later by
	// the selected API-key account, so caller credentials, cookies, routing
	// hints, and internal metadata must not cross this endpoint boundary.
	if strings.EqualFold(strings.TrimSpace(endpoint), adaptor.EndpointCodexTurnCosts) {
		out = codexTurnCostsProviderHeaders(out)
	}
	if out.Get("Content-Type") == "" {
		contentType := ""
		if strings.TrimSpace(rawContentType) != "" {
			// Preserve the supplied value byte-for-byte, including multipart
			// boundary parameters and any media-type parameters.
			contentType = rawContentType
		} else if strings.EqualFold(strings.TrimSpace(endpoint), adaptor.EndpointRealtimeCalls) &&
			bytes.HasPrefix(bytes.TrimSpace(rawBody), []byte("v=0")) {
			contentType = "application/sdp"
		}
		if contentType == "" {
			contentType = "application/json"
		}
		out.Set("Content-Type", contentType)
	}
	// readRawBody may have decoded an inbound gzip/zstd body before parsing.
	// Do not send the decoded payload with the stale encoding marker; the
	// plugin/CPA transport will frame the normalized body itself.
	if requestBodyWasDecoded(c) {
		out.Del("Content-Encoding")
	}
	return out
}

func codexTurnCostsProviderHeaders(in http.Header) http.Header {
	out := make(http.Header)
	for name, values := range in {
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "openai-organization", "openai-project", "content-type":
			if len(values) > 0 {
				out[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
			}
		}
	}
	return out
}

func isForwardableCodexHeaderName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" || isHopByHopProviderHeader(lower) || isCredentialProviderHeader(lower) {
		return false
	}
	return strings.HasPrefix(lower, "x-codex-") ||
		strings.HasPrefix(lower, "x-openai-") ||
		strings.HasPrefix(lower, "x-oai-")
}

func isHopByHopProviderHeader(lower string) bool {
	switch lower {
	case "authorization", "proxy-authorization", "host", "connection", "keep-alive",
		"proxy-authenticate", "proxy-connection", "te", "trailer", "transfer-encoding",
		"upgrade", "content-length", "set-cookie":
		return true
	default:
		return false
	}
}

func isCredentialProviderHeader(lower string) bool {
	for _, marker := range []string{
		"authorization", "api-key", "apikey", "access-token", "refresh-token", "id-token",
		"session-token", "credential", "secret", "password", "cookie",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

const largeAccountRequestTimingThreshold = 1 << 20

// logLargeAccountRequestTiming 仅记录 1MB 以上账号请求的首字前阶段耗时，避免普通请求
// 产生日志噪声。executor_bootstrap 同时包含 CPA 请求转换与上游响应头握手；结合
// post_bootstrap_first_content 可判断时间主要消耗在本地构造/握手阶段还是响应头之后。
func logLargeAccountRequestTiming(
	c *gin.Context,
	acc *accountreg.Snapshot,
	model string,
	endpoint string,
	payloadBytes int,
	result cpa.ForwardResult,
) {
	if c == nil || c.Request == nil || acc == nil || payloadBytes < largeAccountRequestTimingThreshold {
		return
	}
	postBootstrapFirstContentMs := int64(0)
	if result.FirstTokenMs > result.ExecutorBootstrapMs {
		postBootstrapFirstContentMs = result.FirstTokenMs - result.ExecutorBootstrapMs
	}
	requestBeforeAttemptMs := int64(0)
	if result.RequestFirstTokenMs > result.FirstTokenMs {
		requestBeforeAttemptMs = result.RequestFirstTokenMs - result.FirstTokenMs
	}
	logx.LoggerFromContext(c.Request.Context()).InfoContext(
		c.Request.Context(),
		"relay_account_large_request_timing",
		logx.LogFieldAccountID, acc.ID,
		logx.LogFieldPlatform, acc.Platform,
		logx.LogFieldModel, model,
		"endpoint", endpoint,
		"payload_bytes", payloadBytes,
		"request_before_attempt_ms", requestBeforeAttemptMs,
		"executor_bootstrap_ms", result.ExecutorBootstrapMs,
		"post_bootstrap_first_content_ms", postBootstrapFirstContentMs,
		"attempt_first_token_ms", result.FirstTokenMs,
		"request_first_token_ms", result.RequestFirstTokenMs,
		logx.LogFieldStatus, result.StatusCode,
		"stream_completed", result.Done,
	)
}

func accountEmail(acc *accountreg.Snapshot) string {
	if acc == nil {
		return ""
	}
	if email := strings.TrimSpace(acc.Email); email != "" {
		return email
	}
	return strings.TrimSpace(acc.Credentials["email"])
}

func isAuditWriteError(err error) bool {
	return err != nil && (errors.Is(err, requestaudit.ErrWrite) || strings.Contains(err.Error(), requestaudit.ErrWrite.Error()))
}

// errCPAUnavailable CPA 桥未注入时的构建错误（不 failover 到同账号）。
var errCPAUnavailable = errString("CPA 桥接层未配置")

type errString string

func (e errString) Error() string { return string(e) }

// accountAttemptHop 构造账号路径的重试链一跳。
func accountAttemptHop(seq int, acc *accountreg.Snapshot, upstreamStatus int, verdict, reason string, retryAfterMs, latencyMs int64, autoDisabled bool) errlog.AttemptHop {
	return errlog.AttemptHop{
		Seq:          seq,
		AccountID:    acc.ID,
		AccountName:  acc.Name,
		UpstreamStat: upstreamStatus,
		Verdict:      verdict,
		Reason:       reason,
		RetryAfterMs: retryAfterMs,
		LatencyMs:    latencyMs,
		AutoDisabled: autoDisabled,
	}
}

// recordAccountUsage 账号路径计费收尾（ChannelID/ChannelKeyID=0，填 AccountID）。
func (p *Pipeline) recordAccountUsage(
	c *gin.Context,
	keyInfo *auth.APIKeyInfo,
	acc *accountreg.Snapshot,
	req *dto.ChatRequest,
	endpoint string,
	result attemptResult,
	start time.Time,
	price pricing.Price,
) {
	if p.sink == nil {
		return
	}
	var usage dto.Usage
	if result.usage != nil {
		usage = *result.usage
	}
	usage = enrichImageBillingUsage(c.Request.URL.Path, req, result.body, usage)
	tier := serviceTierOf(req)
	reasoningEffort := reasoningEffortOf(c, req)
	costs := pricing.ComputeCosts(price, pricing.Usage{
		PromptTokens:          usage.PromptTokens,
		CompletionTokens:      usage.CompletionTokens,
		CachedTokens:          usage.CachedTokens,
		CacheCreationTokens:   usage.CacheCreationTokens,
		CacheCreation5mTokens: usage.CacheCreation5mTokens,
		CacheCreation1hTokens: usage.CacheCreation1hTokens,
		Calls:                 usage.Calls,
		ImageSize:             usage.ImageSize,
		ImageQuality:          usage.ImageQuality,
	}, tier)
	calc := p.calculator.Calculate(billing.CalculateInput{
		InputCost:         costs.Input,
		OutputCost:        costs.Output,
		CachedInputCost:   costs.Cached,
		CacheCreationCost: costs.CacheCreation5m + costs.CacheCreation1h,
		BillingRate:       billing.ResolveBillingRate(keyInfo),
		SellRate:          keyInfo.SellRate,
		AccountRate:       acc.EffectiveCostRatio(),
	})

	inputTokens := usage.PromptTokens - usage.CachedTokens
	if inputTokens < 0 {
		inputTokens = 0
	}
	billingSnapshot := resolveUsageBilling(endpoint, price, usage)
	usageStatus := usageStatusFor(result, usage, billingSnapshot.Calls)
	if !persistableUsageStatus(usageStatus) {
		return
	}

	// 纯观测口径，异步执行不阻塞计费收尾。
	go p.rpm.IncrementUserGroupRPM(context.Background(), keyInfo.UserID, keyInfo.GroupID)

	p.sink.Record(billing.UsageRecord{
		UserID:                keyInfo.UserID,
		UserEmail:             keyInfo.UserEmail,
		APIKeyID:              keyInfo.KeyID,
		AccountID:             acc.ID,
		GroupID:               keyInfo.GroupID,
		Model:                 req.Model,
		InputTokens:           inputTokens,
		OutputTokens:          usage.CompletionTokens,
		CachedInputTokens:     usage.CachedTokens,
		CacheCreationTokens:   usage.CacheCreationTokens,
		CacheCreation5mTokens: usage.CacheCreation5mTokens,
		CacheCreation1hTokens: usage.CacheCreation1hTokens,
		Calls:                 billingSnapshot.Calls,
		BillingMode:           billingSnapshot.Mode,
		InputPrice:            billingSnapshot.InputPrice,
		OutputPrice:           price.Output,
		CachedInputPrice:      price.CachedInput,
		CacheCreationPrice:    price.CacheCreation5m,
		CacheCreation1hPrice:  price.CacheCreation1h,
		ServiceTier:           tier,
		ReasoningEffort:       reasoningEffort,
		ImageSize:             usage.ImageSize,
		ImageQuality:          usage.ImageQuality,
		UsageStatus:           usageStatus,
		InputCost:             calc.InputCost,
		OutputCost:            calc.OutputCost,
		CachedInputCost:       calc.CachedInputCost,
		CacheCreationCost:     calc.CacheCreationCost,
		TotalCost:             calc.TotalCost,
		ActualCost:            calc.ActualCost,
		BilledCost:            calc.BilledCost,
		RateMultiplier:        calc.RateMultiplier,
		SellRate:              calc.SellRate,
		AccountRateMultiplier: calc.AccountRateMultiplier,
		Stream:                req.Stream,
		DurationMs:            time.Since(start).Milliseconds(),
		FirstTokenMs:          accountUsageFirstToken(result),
		UserAgent:             truncateRunes(c.Request.UserAgent(), maxUserAgentLen),
		IPAddress:             c.ClientIP(),
		Endpoint:              c.Request.URL.Path,
		Source:                billing.SourceRelay,
		RequestID:             requestIDOf(c),
	})
}

func accountUsageFirstToken(result attemptResult) int64 {
	if result.requestFirstTokenMs > 0 {
		return result.requestFirstTokenMs
	}
	return result.firstTokenMs
}

// handleAccountOutcome 处理账号路径一次 attempt 的 outcome（与渠道路径对称）。
// 返回 true 表示请求已终止（成功/不可重试错误/已写出流）。
func (p *Pipeline) handleAccountOutcome(
	c *gin.Context,
	keyInfo *auth.APIKeyInfo,
	acc *accountreg.Snapshot,
	req *dto.ChatRequest,
	endpoint string,
	result attemptResult,
	start time.Time,
	price pricing.Price,
	settings GatewaySettings,
	opts forwardOptions,
	rpmMinute int64,
	attempts int,
	hops *[]errlog.AttemptHop,
	summary *failureSummary,
	hardExclude, softExclude *[]int,
	attemptLatency int64,
	probeLease accountreg.RateLimitProbeLease,
) (done bool) {
	rateLimitProbe := probeLease != 0
	apiKeyHint := ""
	if acc.Credentials != nil {
		apiKeyHint = acc.Credentials["access_token"]
		if apiKeyHint == "" {
			apiKeyHint = acc.Credentials["api_key"]
		}
	}

	if partialErr := result.unreplayableProviderFailure(); partialErr != nil {
		reason := outcome.SanitizeKeyLeak(partialErr.Error(), []string{apiKeyHint})
		if rateLimitProbe && p.accounts != nil {
			p.accounts.MarkRateLimitProbeFailed(acc.ID, probeLease, 0, reason)
		}
		billed := result.usage != nil && !opts.zeroBilling
		if billed {
			p.recordAccountUsage(c, keyInfo, acc, req, endpoint, result, start, price)
		}
		writeError(c, http.StatusBadGateway, "upstream_error", "upstream_response_interrupted", "上游响应中断，已停止重试以避免重复执行请求")
		hop := accountAttemptHop(len(*hops)+1, acc, result.statusCode, "streamAborted", reason, 0, attemptLatency, false)
		p.recordFailure(c, keyInfo, req, start, errlog.Entry{
			Phase: errlog.PhaseStreamAborted, StatusCode: http.StatusBadGateway,
			ErrorType: "upstream_error", ErrorCode: "upstream_response_interrupted",
			Message: "上游已返回部分数据后中断，已阻止账号/渠道 failover", Billed: billed,
			Attempts: attempts, Chain: append(*hops, hop),
			AccountID: acc.ID, AccountName: acc.Name,
		})
		return true
	}

	// buildErr：一次性 400
	if result.buildErr != nil {
		p.rpm.DecrementAccountRPM(context.Background(), acc.ID, rpmMinute)
		adminMsg := outcome.SanitizeKeyLeak(result.buildErr.Error(), []string{apiKeyHint})
		if rateLimitProbe && p.accounts != nil {
			p.accounts.MarkRateLimitProbeFailed(acc.ID, probeLease, 0, adminMsg)
		}
		userMsg := outcome.SanitizeUpstreamLeak(result.buildErr.Error(), []string{apiKeyHint}, "")
		writeError(c, http.StatusBadRequest, "invalid_request_error", "bad_request", userMsg)
		p.recordFailure(c, keyInfo, req, start, errlog.Entry{
			Phase: errlog.PhaseBadRequest, StatusCode: http.StatusBadRequest,
			ErrorType: "invalid_request_error", ErrorCode: "bad_request",
			Message: adminMsg, Attempts: attempts,
			AccountID: acc.ID, AccountName: acc.Name,
		})
		return true
	}

	ctx := c.Request.Context()
	if ctx.Err() != nil && !result.written {
		if rateLimitProbe && p.accounts != nil {
			// Forward 已经开始执行，取消时无法确定请求是否触达上游；按失败探测
			// 冷却，避免客户端连续取消后立即重新取得同一账号的探测资格。
			p.accounts.MarkRateLimitProbeFailed(acc.ID, probeLease, 0, "限流恢复探测在响应完成前被取消，结果未知")
		}
		p.rpm.DecrementAccountRPM(context.Background(), acc.ID, rpmMinute)
		markCanceled(c)
		p.recordFailure(c, keyInfo, req, start, errlog.Entry{
			Phase: errlog.PhaseCanceled, StatusCode: statusClientClosedRequest,
			Message: "客户端取消请求", Attempts: attempts, Chain: *hops,
		})
		return true
	}

	// 流式已写出
	if result.written {
		streamComplete := result.done
		if result.streamErr != nil || !streamComplete {
			slog.Warn("relay_account_stream_aborted",
				"account_id", acc.ID, "model", req.Model,
				"complete", streamComplete, "error", result.streamErr)
			if rateLimitProbe && p.accounts != nil {
				reason := "上游未发送完成标志即断流"
				if result.streamErr != nil {
					reason = outcome.SanitizeKeyLeak(result.streamErr.Error(), []string{apiKeyHint})
				}
				p.accounts.MarkRateLimitProbeFailed(acc.ID, probeLease, 0, reason)
			}
		} else {
			p.recordAccountFirstToken(acc.ID, req.Model, result.firstTokenMs)
			if p.accounts != nil {
				p.accounts.ClearModelRateLimited(acc.ID, req.Model)
				if rateLimitProbe {
					p.accounts.MarkRateLimitProbeSucceeded(acc.ID, probeLease)
				} else {
					p.accounts.MarkActiveIfNotRateLimited(acc.ID)
				}
			}
			if result.usage == nil {
				slog.Warn("relay_account_stream_usage_missing",
					"account_id", acc.ID, "model", req.Model)
			}
		}
		if !opts.zeroBilling {
			p.recordAccountUsage(c, keyInfo, acc, req, endpoint, result, start, price)
		}
		if result.streamErr != nil || !streamComplete {
			reason := "上游未发送完成标志即断流"
			if result.streamErr != nil {
				reason = outcome.SanitizeKeyLeak(result.streamErr.Error(), []string{apiKeyHint})
			}
			hop := accountAttemptHop(len(*hops)+1, acc, result.statusCode, "streamAborted", reason, 0, attemptLatency, false)
			p.recordFailure(c, keyInfo, req, start, errlog.Entry{
				Phase: errlog.PhaseStreamAborted, StatusCode: result.statusCode,
				Message: "上游流中断，响应未完成", Billed: true,
				Attempts: attempts, Chain: append(*hops, hop),
				AccountID: acc.ID, AccountName: acc.Name,
			})
		}
		return true
	}

	o := outcome.Classify(result.statusCode, result.headers, result.body, result.netErr)
	o.Reason = outcome.SanitizeKeyLeak(o.Reason, []string{apiKeyHint})

	switch o.Verdict {
	case outcome.Success:
		if opts.responseTransform != nil {
			if err := opts.responseTransform(acc, &result); err != nil {
				p.rpm.DecrementAccountRPM(context.Background(), acc.ID, rpmMinute)
				if rateLimitProbe && p.accounts != nil {
					p.accounts.MarkRateLimitProbeFailed(acc.ID, probeLease, 0, err.Error())
				}
				writeError(c, http.StatusBadGateway, "upstream_error", "invalid_upstream_response", "上游 Codex 文件响应无效")
				p.recordFailure(c, keyInfo, req, start, errlog.Entry{
					Phase: errlog.PhaseUpstreamClientError, StatusCode: http.StatusBadGateway,
					ErrorType: "upstream_error", ErrorCode: "invalid_upstream_response",
					Message: err.Error(), Attempts: attempts,
					AccountID: acc.ID, AccountName: acc.Name,
				})
				return true
			}
		}
		if p.accounts != nil {
			p.accounts.ClearModelRateLimited(acc.ID, req.Model)
			if rateLimitProbe {
				p.accounts.MarkRateLimitProbeSucceeded(acc.ID, probeLease)
			} else {
				p.accounts.MarkActiveIfNotRateLimited(acc.ID)
			}
		}
		if !opts.zeroBilling {
			p.recordAccountUsage(c, keyInfo, acc, req, endpoint, result, start, price)
		}
		if endpoint == adaptor.EndpointRealtimeCalls {
			p.rememberRealtimeCallAccount(keyInfo.UserID, keyInfo.GroupID, result.headers.Get("Location"), acc.ID)
		}
		writeUpstreamBodyForMode(c, result, opts.passthroughResponse)
		return true

	case outcome.RateLimited:
		p.rpm.DecrementAccountRPM(context.Background(), acc.ID, rpmMinute)
		retryAfter := o.RetryAfter
		if p.accounts != nil {
			if rateLimitProbe {
				blockedUntil := p.accounts.MarkRateLimitProbeFailed(acc.ID, probeLease, retryAfter, o.Reason)
				if remaining := time.Until(blockedUntil); remaining > retryAfter {
					retryAfter = remaining
				}
				p.accounts.MarkModelRateLimited(acc.ID, req.Model, retryAfter)
			} else {
				// (账号, 模型) 级冷却：单模型限流只冷该模型（带退避+抖动）；
				// 全部可服务模型都在冷却时才升级账号级 rate_limited，
				// 走既有单飞恢复探测。
				modelUntil := p.accounts.MarkModelRateLimited(acc.ID, req.Model, retryAfter)
				if remaining := time.Until(modelUntil); remaining > retryAfter {
					retryAfter = remaining
				}
				if p.accounts.AllModelsRateLimited(acc.ID, time.Now()) {
					p.accounts.MarkRateLimited(acc.ID, modelUntil, o.Reason)
				}
			}
		}
		*hardExclude = append(*hardExclude, acc.ID)
		summary.rateLimited = true
		summary.observeRetryAfter(retryAfter)
		*hops = append(*hops, accountAttemptHop(len(*hops)+1, acc, result.statusCode, "rateLimited", o.Reason, retryAfter.Milliseconds(), attemptLatency, false))
		slog.Warn("relay_account_rate_limited",
			"account_id", acc.ID, "model", req.Model, "retry_after", retryAfter.String(), "recovery_probe", rateLimitProbe)
		return false

	case outcome.AuthFailed:
		p.rpm.DecrementAccountRPM(context.Background(), acc.ID, rpmMinute)
		autoBan := settings.AutoBanEnabled
		if autoBan && p.accounts != nil {
			p.accounts.MarkDisabled(acc.ID, outcome.TruncateErrorMsg(o.Reason))
		} else if rateLimitProbe && p.accounts != nil {
			p.accounts.MarkRateLimitProbeFailed(acc.ID, probeLease, 0, o.Reason)
		}
		*hardExclude = append(*hardExclude, acc.ID)
		summary.authFailed = true
		*hops = append(*hops, accountAttemptHop(len(*hops)+1, acc, result.statusCode, "authFailed", o.Reason, 0, attemptLatency, autoBan))
		slog.Warn("relay_account_auth_failed",
			"account_id", acc.ID, "model", req.Model, "auto_ban", autoBan, "reason", o.Reason)
		return false

	case outcome.Transient:
		p.rpm.DecrementAccountRPM(context.Background(), acc.ID, rpmMinute)
		if rateLimitProbe {
			if p.accounts != nil {
				p.accounts.MarkRateLimitProbeFailed(acc.ID, probeLease, 0, o.Reason)
			}
			*hardExclude = append(*hardExclude, acc.ID)
		} else {
			// 账号池场景已收敛到渠道；普通 transient 仅本轮 soft 排除，不做 pool 软降级。
			*softExclude = append(*softExclude, acc.ID)
		}
		summary.transient = true
		verdictName := "transient"
		if result.netErr != nil {
			verdictName = "networkError"
		}
		*hops = append(*hops, accountAttemptHop(len(*hops)+1, acc, result.statusCode, verdictName, o.Reason, 0, attemptLatency, false))
		slog.Warn("relay_account_transient_failure",
			"account_id", acc.ID, "model", req.Model, "reason", o.Reason)
		return false

	default: // ClientError
		if rateLimitProbe && p.accounts != nil {
			p.accounts.MarkRateLimitProbeFailed(acc.ID, probeLease, 0, o.Reason)
		}
		billed := result.usage != nil && !opts.zeroBilling
		if billed {
			p.recordAccountUsage(c, keyInfo, acc, req, endpoint, result, start, price)
		}
		if opts.passthroughResponse {
			writePassthroughUpstreamBody(c, result)
			hop := accountAttemptHop(len(*hops)+1, acc, result.statusCode, "clientError", o.Reason, 0, attemptLatency, false)
			p.recordFailure(c, keyInfo, req, start, errlog.Entry{
				Phase: errlog.PhaseUpstreamClientError, StatusCode: result.statusCode,
				Message: o.Reason, Billed: billed, Attempts: attempts,
				Chain: append(*hops, hop), AccountID: acc.ID, AccountName: acc.Name,
			})
			return true
		}
		up := errfmt.ParseUpstream(result.statusCode, result.body)
		up.Message = outcome.SanitizeUpstreamLeak(up.Message, []string{apiKeyHint}, "")
		writeUpstreamError(c, result.statusCode, up)
		hop := accountAttemptHop(len(*hops)+1, acc, result.statusCode, "clientError", o.Reason, 0, attemptLatency, false)
		p.recordFailure(c, keyInfo, req, start, errlog.Entry{
			Phase: errlog.PhaseUpstreamClientError, StatusCode: result.statusCode,
			Message: o.Reason, Billed: billed,
			Attempts: attempts, Chain: append(*hops, hop),
			AccountID: acc.ID, AccountName: acc.Name,
		})
		return true
	}
}

// prepareAccountPayload 序列化请求体供 CPA 转发（原样 JSON）。
func prepareAccountPayload(req *dto.ChatRequest, opts forwardOptions) ([]byte, error) {
	if opts.rawBody != nil {
		return opts.rawBody, nil
	}
	return req.Marshal()
}

// acquireAccountSlots 抢账号 RPM + 并发槽；失败返回 soft=true 表示容量满。
func (p *Pipeline) acquireAccountSlots(ctx context.Context, acc *accountreg.Snapshot, stream bool) (requestID string, rpmMinute int64, soft bool, ok bool) {
	requestID = uuid.New().String()
	minute, err := p.concurrency.AcquireAccountCapacity(
		ctx, acc.ID, requestID, acc.MaxRPM, acc.MaxConcurrency, channelSlotTTL(stream),
	)
	if err != nil {
		return "", 0, true, false
	}
	return requestID, minute, false, true
}
