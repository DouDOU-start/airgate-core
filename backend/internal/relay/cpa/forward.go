package cpa

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"

	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
	"github.com/DouDOU-start/airgate-core/internal/relay/streamlife"
)

// ForwardRequest 一次账号路径转发请求。
type ForwardRequest struct {
	// Account 已解密的账号字段（凭证 + 平台 + 代理）。
	Account AccountAuthInput
	// Model 对外模型名。
	Model string
	// UpstreamModel is the provider-facing model name after account mapping.
	// When empty, Model is used.
	UpstreamModel string
	// Endpoint 入口端点标识（adaptor.Endpoint*）。
	Endpoint string
	// EntryProtocol 入口协议（openai / anthropic / gemini）。
	EntryProtocol string
	// Stream 是否流式。
	Stream bool
	// Payload 原始 JSON 请求体。
	Payload []byte
	// Headers 可选转发头。
	Headers http.Header
}

// ForwardResult 转发结果，语义对齐 pipeline.attemptResult。
type ForwardResult struct {
	StatusCode   int
	Headers      http.Header
	Body         []byte
	ContentType  string
	Usage        *dto.Usage
	FirstTokenMs int64
	Written      bool
	StreamErr    error
	Done         bool
	NetErr       error
	BuildErr     error
	// RefreshedCredentials refresh 成功后的新凭证（调用方应落库）。
	RefreshedCredentials map[string]string
}

// ForwardAccountTest 执行一次非流式账号连通性测试，供账号管理复用 CPA executor。
// 与 HTTP 转发入口分开，避免账号测试为了构造 gin.Context 而依赖 Web 层对象。
func (b *Bridge) ForwardAccountTest(ctx context.Context, req ForwardRequest) ForwardResult {
	req.Stream = false
	return b.Forward(ctx, nil, req)
}

// Forward 按账号平台选取 CPA executor 执行一次转发。
//
// 流式：边收 StreamChunk 边写 gin.Writer；旁路提取 usage。
// 非流式：返回完整 body + usage。
// OAuth 凭证临近过期时主动刷新；认证失败时再刷新一次并重试。
func (b *Bridge) Forward(ctx context.Context, c *gin.Context, req ForwardRequest) ForwardResult {
	if b == nil {
		return ForwardResult{BuildErr: fmt.Errorf("cpa bridge 未初始化")}
	}
	if strings.TrimSpace(req.Model) == "" {
		return ForwardResult{BuildErr: fmt.Errorf("缺少 model")}
	}
	if len(req.Payload) == 0 {
		return ForwardResult{BuildErr: fmt.Errorf("请求体为空")}
	}

	auth, err := MapAuth(req.Account)
	if err != nil {
		return ForwardResult{BuildErr: err}
	}
	ex, err := b.EnsureExecutor(auth.Provider)
	if err != nil {
		return ForwardResult{BuildErr: err}
	}
	var proactiveCredentials map[string]string
	if authNeedsProactiveRefresh(auth, time.Now()) {
		if refreshed, refreshErr := b.refreshAuth(ctx, ex, auth); refreshErr == nil && refreshed != nil {
			auth = refreshed
			proactiveCredentials = CredentialsFromAuth(auth)
		}
	}

	upstreamModel := strings.TrimSpace(req.UpstreamModel)
	if upstreamModel == "" {
		upstreamModel = req.Model
	}
	sourceFmt := sourceFormatFor(req.Endpoint, req.EntryProtocol)
	execReq := cliproxyexecutor.Request{
		Model:   upstreamModel,
		Payload: req.Payload,
	}
	opts := cliproxyexecutor.Options{
		Stream:          req.Stream,
		OriginalRequest: req.Payload,
		SourceFormat:    sourceFmt,
		ResponseFormat:  sourceFmt,
		Headers:         cloneHeader(req.Headers),
		Metadata: map[string]any{
			cliproxyexecutor.RequestedModelMetadataKey: req.Model,
			cliproxyexecutor.RequestPathMetadataKey:    requestPathOf(c),
		},
	}

	if isCountTokensEndpoint(req.Endpoint) {
		return withRefreshedCredentials(b.doCount(ctx, ex, auth, execReq, opts), proactiveCredentials)
	}
	if req.Stream {
		return withRefreshedCredentials(b.doStream(ctx, c, ex, auth, execReq, opts, req.Endpoint), proactiveCredentials)
	}
	return withRefreshedCredentials(b.doNonStream(ctx, ex, auth, execReq, opts), proactiveCredentials)
}

// ForwardNonStream executes an account request without an HTTP response writer.
// It is used by connection tests so they exercise the same CPA path as relay traffic.
func (b *Bridge) ForwardNonStream(ctx context.Context, req ForwardRequest) ForwardResult {
	req.Stream = false
	return b.Forward(ctx, nil, req)
}

func (b *Bridge) doNonStream(
	ctx context.Context,
	ex coreauth.ProviderExecutor,
	auth *coreauth.Auth,
	execReq cliproxyexecutor.Request,
	opts cliproxyexecutor.Options,
) ForwardResult {
	opts.Stream = false
	resp, err := ex.Execute(ctx, auth, execReq, opts)
	if err != nil {
		if authHasRefreshCredential(auth) && isRefreshableAuthError(auth.Provider, err) {
			refreshed, refreshErr := b.refreshAuth(ctx, ex, auth)
			if refreshErr == nil && refreshed != nil {
				auth = refreshed
				resp, err = ex.Execute(ctx, auth, execReq, opts)
				if err == nil {
					result := nonStreamOK(resp)
					result.RefreshedCredentials = CredentialsFromAuth(auth)
					return result
				}
			}
		}
		return errorToResult(err)
	}
	return nonStreamOK(resp)
}

func (b *Bridge) doCount(
	ctx context.Context,
	ex coreauth.ProviderExecutor,
	auth *coreauth.Auth,
	execReq cliproxyexecutor.Request,
	opts cliproxyexecutor.Options,
) ForwardResult {
	opts.Stream = false
	resp, err := ex.CountTokens(ctx, auth, execReq, opts)
	if err != nil {
		if authHasRefreshCredential(auth) && isRefreshableAuthError(auth.Provider, err) {
			refreshed, refreshErr := b.refreshAuth(ctx, ex, auth)
			if refreshErr == nil && refreshed != nil {
				auth = refreshed
				resp, err = ex.CountTokens(ctx, auth, execReq, opts)
				if err == nil {
					result := nonStreamOK(resp)
					result.RefreshedCredentials = CredentialsFromAuth(auth)
					return result
				}
			}
		}
		return errorToResult(err)
	}
	return nonStreamOK(resp)
}

func (b *Bridge) doStream(
	ctx context.Context,
	c *gin.Context,
	ex coreauth.ProviderExecutor,
	auth *coreauth.Auth,
	execReq cliproxyexecutor.Request,
	opts cliproxyexecutor.Options,
	endpoint string,
) ForwardResult {
	opts.Stream = true
	start := time.Now()
	upstreamCtx, markStreamStarted, cancel := streamlife.DetachAfterStart(ctx, streamlife.MaxDuration)
	defer cancel()

	stream, err := ex.ExecuteStream(upstreamCtx, auth, execReq, opts)
	if err != nil {
		if authHasRefreshCredential(auth) && isRefreshableAuthError(auth.Provider, err) {
			refreshed, refreshErr := b.refreshAuth(upstreamCtx, ex, auth)
			if refreshErr == nil && refreshed != nil {
				auth = refreshed
				stream, err = ex.ExecuteStream(upstreamCtx, auth, execReq, opts)
				if err == nil {
					markStreamStarted()
					result := b.relayStream(upstreamCtx, c, stream, start, endpoint)
					result.RefreshedCredentials = CredentialsFromAuth(auth)
					return result
				}
			}
		}
		return errorToResult(err)
	}
	// ExecuteStream 成功表示已建立上游响应流；此后客户端断开不再取消上游，
	// relayStream 会停止向客户端写入，但继续读取到完成事件以捕获 usage。
	markStreamStarted()
	result := b.relayStream(upstreamCtx, c, stream, start, endpoint)
	// 部分 executor 在 goroutine 启动后才从首个 chunk 返回上游认证错误。
	// 尚未向客户端写出内容时仍可安全刷新并重试一次。
	if authHasRefreshCredential(auth) && isRefreshableAuthResult(auth.Provider, result) {
		refreshed, refreshErr := b.refreshAuth(upstreamCtx, ex, auth)
		if refreshErr == nil && refreshed != nil {
			auth = refreshed
			stream, err = ex.ExecuteStream(upstreamCtx, auth, execReq, opts)
			if err == nil {
				result = b.relayStream(upstreamCtx, c, stream, time.Now(), endpoint)
				result.RefreshedCredentials = CredentialsFromAuth(auth)
				return result
			}
		}
	}
	return result
}

// relayStream 把 CPA StreamResult 的 chunk 写成客户端 SSE，并旁路提取 usage。
func (b *Bridge) relayStream(
	ctx context.Context,
	c *gin.Context,
	stream *cliproxyexecutor.StreamResult,
	start time.Time,
	endpoint string,
) ForwardResult {
	if stream == nil {
		return ForwardResult{NetErr: fmt.Errorf("CPA 返回空流")}
	}
	result := ForwardResult{
		StatusCode:  http.StatusOK,
		Headers:     cloneHeader(stream.Headers),
		ContentType: "text/event-stream",
	}

	w := c.Writer
	flusher, _ := w.(http.Flusher)
	var (
		usage            *dto.Usage
		firstTokenMs     int64
		written          bool
		downstreamClosed bool
		done             bool
		streamErr        error
	)

	extractUsage := dto.ExtractUsage
	if endpoint == adaptor.EndpointResponses {
		extractUsage = dto.ExtractResponsesUsage
	}
	var responsesFramer *responsesSSEFramer
	if endpoint == adaptor.EndpointResponses {
		responsesFramer = &responsesSSEFramer{}
	}
	isFirstContentPayload := looksLikeContent
	if endpoint == adaptor.EndpointResponses {
		isFirstContentPayload = responsesPayloadHasContentDelta
	}

	writePayload := func(payload []byte, ensureLineEnding bool) {
		// 旁路解析 usage / [DONE]。Responses 在分帧后解析，能够处理跨 chunk 的 JSON。
		scanStreamPayload(payload, extractUsage, &usage, &done)
		if firstTokenMs == 0 && isFirstContentPayload(payload) {
			firstTokenMs = time.Since(start).Milliseconds()
		}
		if downstreamClosed {
			return
		}
		if !written && !c.Writer.Written() {
			writeStreamHeaders(w, stream.Headers)
		}
		if _, err := w.Write(payload); err != nil {
			written = true
			downstreamClosed = true
			return
		}
		if ensureLineEnding && !bytes.HasSuffix(payload, []byte("\n")) {
			if _, err := w.Write([]byte("\n")); err != nil {
				written = true
				downstreamClosed = true
				return
			}
		}
		written = true
		if flusher != nil {
			flusher.Flush()
		}
	}

	for {
		select {
		case <-ctx.Done():
			if !written {
				result.NetErr = ctx.Err()
				return result
			}
			streamErr = ctx.Err()
			goto finish
		case chunk, ok := <-stream.Chunks:
			if !ok {
				if responsesFramer != nil {
					for _, frame := range responsesFramer.Flush() {
						writePayload(frame, false)
					}
				}
				goto finish
			}
			if chunk.Err != nil {
				streamErr = chunk.Err
				// 若尚未写出任何字节，按错误结果返回（可 failover）。
				if !written {
					return errorToResult(chunk.Err)
				}
				goto finish
			}
			payload := chunk.Payload
			if len(payload) == 0 {
				continue
			}
			if responsesFramer != nil {
				for _, frame := range responsesFramer.WriteChunk(payload) {
					writePayload(frame, false)
				}
				continue
			}
			writePayload(payload, true)
		}
	}

finish:
	if !written && streamErr == nil {
		result.NetErr = fmt.Errorf("CPA 流在返回内容前结束")
		return result
	}
	result.Written = written
	result.Usage = usage
	result.FirstTokenMs = firstTokenMs
	result.StreamErr = streamErr
	result.Done = done
	return result
}

func writeStreamHeaders(w gin.ResponseWriter, headers http.Header) {
	ct := headerOr(headers, "Content-Type", "text/event-stream")
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	if v := headerOr(headers, "X-Request-Id", ""); v != "" {
		w.Header().Set("X-Request-Id", v)
	}
	w.WriteHeader(http.StatusOK)
}

func nonStreamOK(resp cliproxyexecutor.Response) ForwardResult {
	body := resp.Payload
	var usage *dto.Usage
	if u, ok := dto.ExtractUsage(body); ok {
		usage = &u
	} else if u, ok := dto.ExtractResponsesUsage(body); ok {
		usage = &u
	}
	ct := nonStreamContentType(resp.Headers)
	return ForwardResult{
		StatusCode:  http.StatusOK,
		Headers:     cloneHeader(resp.Headers),
		Body:        body,
		ContentType: ct,
		Usage:       usage,
	}
}

// nonStreamContentType 修正 CPA executor 遗留的上游流式内容类型。
// xAI 的 /responses 即使由 Execute 聚合、翻译成单个 JSON，响应头仍可能保留
// text/event-stream；Anthropic SDK 会因此把普通 JSON 当作 SSE，最终丢失 usage。
func nonStreamContentType(headers http.Header) string {
	contentType := headerOr(headers, "Content-Type", "application/json")
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "text/event-stream") {
		return "application/json"
	}
	return contentType
}

func errorToResult(err error) ForwardResult {
	info := ClassifyError(err)
	if info.NetLike {
		return ForwardResult{NetErr: err}
	}
	return ForwardResult{
		StatusCode: info.StatusCode,
		Headers:    info.Headers,
		Body:       info.Body,
	}
}

func withRefreshedCredentials(result ForwardResult, credentials map[string]string) ForwardResult {
	if len(result.RefreshedCredentials) == 0 && len(credentials) > 0 {
		result.RefreshedCredentials = credentials
	}
	return result
}

func sourceFormatFor(endpoint, entryProtocol string) sdktranslator.Format {
	switch endpoint {
	case adaptor.EndpointResponses:
		return sdktranslator.FormatOpenAIResponse
	case adaptor.EndpointMessages, adaptor.EndpointMessagesCountTokens:
		return sdktranslator.FormatClaude
	case adaptor.EndpointGenerateContent, adaptor.EndpointPredict, adaptor.EndpointCountTokens:
		return sdktranslator.FormatGemini
	case adaptor.EndpointImagesGenerations, adaptor.EndpointImagesEdits:
		return sdktranslator.FromString("openai-image")
	case adaptor.EndpointXAIVideosGenerations, adaptor.EndpointXAIVideosRetrieve:
		return sdktranslator.FromString("openai-video")
	case adaptor.EndpointChatCompletions, adaptor.EndpointAlphaSearch:
		return sdktranslator.FormatOpenAI
	}
	// 回退：按入口协议。
	switch entryProtocol {
	case registry.ProtocolAnthropic:
		return sdktranslator.FormatClaude
	case registry.ProtocolGemini:
		return sdktranslator.FormatGemini
	default:
		return sdktranslator.FormatOpenAI
	}
}

func isCountTokensEndpoint(endpoint string) bool {
	return endpoint == adaptor.EndpointMessagesCountTokens || endpoint == adaptor.EndpointCountTokens
}

func requestPathOf(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	return c.Request.URL.Path
}

func cloneHeader(h http.Header) http.Header {
	if h == nil {
		return nil
	}
	return h.Clone()
}

func headerOr(h http.Header, key, fallback string) string {
	if h == nil {
		return fallback
	}
	if v := strings.TrimSpace(h.Get(key)); v != "" {
		return v
	}
	return fallback
}

// scanStreamPayload 从一段 SSE/JSON chunk 中旁路提取 usage 与完成标志。
func scanStreamPayload(payload []byte, extract func([]byte) (dto.Usage, bool), usage **dto.Usage, done *bool) {
	scanner := bufio.NewScanner(bytes.NewReader(payload))
	scanner.Buffer(make([]byte, 0, 64*1024), 32<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				*done = true
				continue
			}
			if data == "" {
				continue
			}
			if u, ok := extract([]byte(data)); ok {
				cp := u
				*usage = &cp
			}
			if isProtocolCompletion([]byte(data)) {
				*done = true
			}
		}
	}
}

func isProtocolCompletion(data []byte) bool {
	var event struct {
		Type       string `json:"type"`
		Candidates []struct {
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
	}
	if json.Unmarshal(data, &event) != nil {
		return false
	}
	if event.Type == "response.completed" || event.Type == "response.done" || event.Type == "message_stop" {
		return true
	}
	for _, candidate := range event.Candidates {
		if strings.TrimSpace(candidate.FinishReason) != "" {
			return true
		}
	}
	return false
}

// looksLikeContent 粗判 chunk 是否含内容（用于 first_token 计时）。
func looksLikeContent(payload []byte) bool {
	s := string(payload)
	if !strings.Contains(s, "data:") {
		return len(bytes.TrimSpace(payload)) > 0
	}
	return strings.Contains(s, "content") ||
		strings.Contains(s, "delta") ||
		strings.Contains(s, "text") ||
		strings.Contains(s, "output")
}

// responsesPayloadHasContentDelta 判断 Responses SSE 帧是否包含真实内容增量。
// response.created / response.in_progress 等生命周期事件可能含 output/content 字段，
// 不能据此记录首字；仅 type 以 .delta 结尾的事件算首内容。
func responsesPayloadHasContentDelta(payload []byte) bool {
	scanner := bufio.NewScanner(bytes.NewReader(payload))
	scanner.Buffer(make([]byte, 0, 64*1024), 32<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := bytes.TrimSpace([]byte(strings.TrimPrefix(line, "data:")))
		var event struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &event) == nil && strings.HasSuffix(event.Type, ".delta") {
			return true
		}
	}
	return false
}
