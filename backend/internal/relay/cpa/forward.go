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
	"github.com/DouDOU-start/airgate-core/internal/relay/streamerr"
	"github.com/DouDOU-start/airgate-core/internal/relay/streamlife"
)

const (
	cpaPreContentBufferLimit       = 1 << 20
	antigravityGemini37FlashPublic = "gemini-3.7-flash-high"
	antigravityGemini37FlashTiered = "gemini-3.7-flash-tiered"
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
	// RequestStartedAt 是请求进入转发主循环的时间，用于记录包含故障转移的真实首字耗时。
	RequestStartedAt time.Time
}

// ForwardResult 转发结果，语义对齐 pipeline.attemptResult。
type ForwardResult struct {
	StatusCode  int
	Headers     http.Header
	Body        []byte
	ContentType string
	Usage       *dto.Usage
	// FirstTokenMs 是当前账号 attempt 自身的内容首字耗时，供账号调度 EWMA 使用。
	FirstTokenMs int64
	// RequestFirstTokenMs 是从请求进入转发主循环到内容首字的总耗时，包含前置处理与故障转移。
	RequestFirstTokenMs int64
	// ExecutorBootstrapMs 是 CPA executor 从开始执行到拿到上游响应头并返回流对象的耗时，
	// 包含请求转换、请求构造和上游握手，用于定位超大请求首字前的本地开销。
	ExecutorBootstrapMs int64
	Written             bool
	StreamErr           error
	Done                bool
	NetErr              error
	BuildErr            error
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

	mappedAuth, err := b.mappedAuth(req.Account)
	if err != nil {
		return ForwardResult{BuildErr: err}
	}
	auth := mappedAuth.auth
	ex, err := b.EnsureExecutor(auth.Provider)
	if err != nil {
		return ForwardResult{BuildErr: err}
	}
	var proactiveCredentials map[string]string
	if mappedAuth.needsProactiveRefresh(time.Now()) {
		if refreshed, refreshErr := b.refreshAuth(ctx, ex, auth); refreshErr == nil && refreshed != nil {
			auth = refreshed
			proactiveCredentials = CredentialsFromAuth(auth)
		}
	}

	upstreamModel := strings.TrimSpace(req.UpstreamModel)
	if upstreamModel == "" {
		upstreamModel = req.Model
	}
	upstreamModel = resolveProviderUpstreamModel(auth.Provider, req.Model, upstreamModel)
	modelRewrite := providerResponseModelRewrite(auth.Provider, req.Model, upstreamModel)
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
		result := b.doCount(ctx, ex, auth, execReq, opts)
		return withRefreshedCredentials(rewriteForwardResultModel(result, modelRewrite), proactiveCredentials)
	}
	if req.Stream {
		return withRefreshedCredentials(b.doStream(ctx, c, ex, auth, execReq, opts, req.Endpoint, req.RequestStartedAt, modelRewrite), proactiveCredentials)
	}
	result := b.doNonStream(ctx, ex, auth, execReq, opts)
	return withRefreshedCredentials(rewriteForwardResultModel(result, modelRewrite), proactiveCredentials)
}

// resolveProviderUpstreamModel 只处理供应商内部模型名，Model 仍保留对外标准 ID。
// Antigravity 实时目录把 Gemini 3.7 Flash 暴露为 tiered，但 CPA 静态目录、
// 模型广场和计费统一使用 high；因此仅在最终上游请求处转换。
func resolveProviderUpstreamModel(provider, requestedModel, upstreamModel string) string {
	if strings.EqualFold(strings.TrimSpace(provider), "antigravity") &&
		strings.EqualFold(strings.TrimSpace(requestedModel), antigravityGemini37FlashPublic) &&
		strings.EqualFold(strings.TrimSpace(upstreamModel), antigravityGemini37FlashPublic) {
		return antigravityGemini37FlashTiered
	}
	return upstreamModel
}

type responseModelRewrite struct {
	from string
	to   string
}

func newResponseModelRewrite(requestedModel, upstreamModel string) responseModelRewrite {
	requestedModel = strings.TrimSpace(requestedModel)
	upstreamModel = strings.TrimSpace(upstreamModel)
	if requestedModel == "" || upstreamModel == "" || strings.EqualFold(requestedModel, upstreamModel) {
		return responseModelRewrite{}
	}
	return responseModelRewrite{from: upstreamModel, to: requestedModel}
}

func providerResponseModelRewrite(provider, requestedModel, upstreamModel string) responseModelRewrite {
	if strings.EqualFold(strings.TrimSpace(provider), "antigravity") &&
		strings.EqualFold(strings.TrimSpace(requestedModel), antigravityGemini37FlashPublic) &&
		strings.EqualFold(strings.TrimSpace(upstreamModel), antigravityGemini37FlashTiered) {
		return newResponseModelRewrite(requestedModel, upstreamModel)
	}
	return responseModelRewrite{}
}

func rewriteForwardResultModel(result ForwardResult, rewrite responseModelRewrite) ForwardResult {
	if len(result.Body) > 0 {
		result.Body = rewriteResponseModelPayload(result.Body, rewrite)
	}
	return result
}

// rewriteResponseModelPayload 仅回写 JSON 中名为 model 的字段，不替换正文里的普通文本。
// 同时兼容非流式 JSON、裸流式 JSON 和 SSE data 行。
func rewriteResponseModelPayload(payload []byte, rewrite responseModelRewrite) []byte {
	if rewrite.from == "" || rewrite.to == "" || !bytes.Contains(payload, []byte(rewrite.from)) {
		return payload
	}
	trimmed := bytes.TrimSpace(payload)
	if json.Valid(trimmed) {
		if rewritten, ok := rewriteJSONModelFields(trimmed, rewrite); ok {
			return rewritten
		}
		return payload
	}

	lines := bytes.SplitAfter(payload, []byte("\n"))
	changed := false
	for i, line := range lines {
		lineEnding := []byte{}
		content := line
		if bytes.HasSuffix(content, []byte("\n")) {
			lineEnding = []byte("\n")
			content = content[:len(content)-1]
		}
		if bytes.HasSuffix(content, []byte("\r")) {
			lineEnding = []byte("\r\n")
			content = content[:len(content)-1]
		}
		trimmedLine := bytes.TrimSpace(content)
		if !bytes.HasPrefix(trimmedLine, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(trimmedLine[len("data:"):])
		if !json.Valid(data) {
			continue
		}
		rewritten, ok := rewriteJSONModelFields(data, rewrite)
		if !ok {
			continue
		}
		prefixLen := bytes.Index(content, []byte("data:")) + len("data:")
		prefix := content[:prefixLen]
		spacing := content[prefixLen:]
		spacing = spacing[:len(spacing)-len(bytes.TrimLeft(spacing, " \t"))]
		lines[i] = bytes.Join([][]byte{prefix, spacing, rewritten, lineEnding}, nil)
		changed = true
	}
	if !changed {
		return payload
	}
	return bytes.Join(lines, nil)
}

func rewriteJSONModelFields(payload []byte, rewrite responseModelRewrite) ([]byte, bool) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil || !rewriteModelFields(value, rewrite) {
		return payload, false
	}
	rewritten, err := json.Marshal(value)
	if err != nil {
		return payload, false
	}
	return rewritten, true
}

func rewriteModelFields(value any, rewrite responseModelRewrite) bool {
	changed := false
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			if key == "model" {
				if model, ok := child.(string); ok && strings.EqualFold(strings.TrimSpace(model), rewrite.from) {
					current[key] = rewrite.to
					changed = true
					continue
				}
			}
			if rewriteModelFields(child, rewrite) {
				changed = true
			}
		}
	case []any:
		for _, child := range current {
			if rewriteModelFields(child, rewrite) {
				changed = true
			}
		}
	}
	return changed
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
	requestStartedAt time.Time,
	modelRewrite responseModelRewrite,
) ForwardResult {
	opts.Stream = true
	start := time.Now()
	upstreamCtx, markStreamStarted, cancel := streamlife.DetachAfterStart(ctx, streamlife.MaxDuration)
	defer cancel()

	stream, err := ex.ExecuteStream(upstreamCtx, auth, execReq, opts)
	bootstrapMs := time.Since(start).Milliseconds()
	if err != nil {
		if authHasRefreshCredential(auth) && isRefreshableAuthError(auth.Provider, err) {
			refreshed, refreshErr := b.refreshAuth(upstreamCtx, ex, auth)
			if refreshErr == nil && refreshed != nil {
				auth = refreshed
				stream, err = ex.ExecuteStream(upstreamCtx, auth, execReq, opts)
				bootstrapMs = time.Since(start).Milliseconds()
				if err == nil {
					markStreamStarted()
					result := b.relayStreamSinceWithModelRewrite(upstreamCtx, c, stream, start, requestStartedAt, endpoint, modelRewrite)
					result.ExecutorBootstrapMs = bootstrapMs
					result.RefreshedCredentials = CredentialsFromAuth(auth)
					return result
				}
			}
		}
		result := errorToResult(err)
		result.ExecutorBootstrapMs = bootstrapMs
		return result
	}
	// ExecuteStream 成功表示已建立上游响应流；此后客户端断开不再取消上游，
	// relayStream 会停止向客户端写入，但继续读取到完成事件以捕获 usage。
	markStreamStarted()
	result := b.relayStreamSinceWithModelRewrite(upstreamCtx, c, stream, start, requestStartedAt, endpoint, modelRewrite)
	result.ExecutorBootstrapMs = bootstrapMs
	// 部分 executor 在 goroutine 启动后才从首个 chunk 返回上游认证错误。
	// 尚未向客户端写出内容时仍可安全刷新并重试一次。
	if authHasRefreshCredential(auth) && isRefreshableAuthResult(auth.Provider, result) {
		refreshed, refreshErr := b.refreshAuth(upstreamCtx, ex, auth)
		if refreshErr == nil && refreshed != nil {
			auth = refreshed
			stream, err = ex.ExecuteStream(upstreamCtx, auth, execReq, opts)
			if err == nil {
				bootstrapMs = time.Since(start).Milliseconds()
				result = b.relayStreamSinceWithModelRewrite(upstreamCtx, c, stream, time.Now(), requestStartedAt, endpoint, modelRewrite)
				result.ExecutorBootstrapMs = bootstrapMs
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
	return b.relayStreamSince(ctx, c, stream, start, time.Time{}, endpoint)
}

func (b *Bridge) relayStreamSince(
	ctx context.Context,
	c *gin.Context,
	stream *cliproxyexecutor.StreamResult,
	start time.Time,
	requestStartedAt time.Time,
	endpoint string,
) ForwardResult {
	return b.relayStreamSinceWithModelRewrite(ctx, c, stream, start, requestStartedAt, endpoint, responseModelRewrite{})
}

func (b *Bridge) relayStreamSinceWithModelRewrite(
	ctx context.Context,
	c *gin.Context,
	stream *cliproxyexecutor.StreamResult,
	start time.Time,
	requestStartedAt time.Time,
	endpoint string,
	modelRewrite responseModelRewrite,
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
		usage               *dto.Usage
		firstTokenMs        int64
		requestFirstTokenMs int64
		written             bool
		downstreamClosed    bool
		contentStarted      bool
		done                bool
		streamErr           error
		pending             bytes.Buffer
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
	switch endpoint {
	case adaptor.EndpointResponses:
		isFirstContentPayload = responsesPayloadHasContent
	case adaptor.EndpointMessages:
		isFirstContentPayload = anthropicPayloadHasContentDelta
	}

	writePayload := func(payload []byte, ensureLineEnding bool) {
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
	appendPayload := func(dst *bytes.Buffer, payload []byte, ensureLineEnding bool) {
		_, _ = dst.Write(payload)
		if ensureLineEnding && !bytes.HasSuffix(payload, []byte("\n")) {
			_ = dst.WriteByte('\n')
		}
	}
	flushPending := func() {
		if pending.Len() == 0 {
			return
		}
		writePayload(pending.Bytes(), false)
		pending.Reset()
	}
	queuePayload := func(payload []byte, ensureLineEnding bool) {
		appendPayload(&pending, payload, ensureLineEnding)
		if pending.Len() > cpaPreContentBufferLimit {
			// 极端上游在首内容前发送大量生命周期数据时停止缓冲，避免
			// 单请求无界占用；一旦提交后，后续错误按流中断处理。
			flushPending()
		}
	}
	processPayload := func(payload []byte, ensureLineEnding bool) (stop bool) {
		payload = rewriteResponseModelPayload(payload, modelRewrite)
		// 旁路解析 usage / 完成标志。Responses 在分帧后解析，可处理跨 chunk JSON。
		scanStreamPayload(payload, extractUsage, &usage, &done)
		if event, ok := streamerr.Detect(payload); ok {
			if !contentStarted && !written {
				pending.Reset()
				result.StatusCode = event.StatusCode
				result.Body = event.Body
				result.Usage = usage
				return true
			}
			// 已输出真实内容（或缓冲上限已迫使提交）后不可重试；保留错误帧
			// 给客户端，并把该次请求标记为流中断。
			streamErr = fmt.Errorf("上游流错误：%s", string(event.Body))
			writePayload(payload, ensureLineEnding)
			return true
		}

		hasContent := !contentStarted && isFirstContentPayload(payload)
		if hasContent {
			contentStarted = true
			now := time.Now()
			if firstTokenMs == 0 {
				firstTokenMs = now.Sub(start).Milliseconds()
			}
			if requestFirstTokenMs == 0 && !requestStartedAt.IsZero() && now.After(requestStartedAt) {
				requestFirstTokenMs = now.Sub(requestStartedAt).Milliseconds()
			}
			flushPending()
		}
		if contentStarted || written {
			writePayload(payload, ensureLineEnding)
		} else {
			queuePayload(payload, ensureLineEnding)
		}
		return false
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
						if processPayload(frame, false) {
							if !written {
								return result
							}
							goto finish
						}
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
			if endpoint == adaptor.EndpointChatCompletions {
				payload = frameChatCompletionsPayload(payload)
			}
			if responsesFramer != nil {
				for _, frame := range responsesFramer.WriteChunk(payload) {
					if processPayload(frame, false) {
						if !written {
							return result
						}
						goto finish
					}
				}
				continue
			}
			if processPayload(payload, true) {
				if !written {
					return result
				}
				goto finish
			}
		}
	}

finish:
	if !written && streamErr == nil {
		if done {
			// 合法的无内容完成流仍需把生命周期事件交给客户端。
			flushPending()
		} else {
			pending.Reset()
			result.NetErr = fmt.Errorf("CPA 流在返回真实内容前结束")
			return result
		}
	}
	result.Written = written
	result.Usage = usage
	result.FirstTokenMs = firstTokenMs
	result.RequestFirstTokenMs = requestFirstTokenMs
	result.StreamErr = streamErr
	result.Done = done
	return result
}

// frameChatCompletionsPayload 把 CPA 翻译器产出的裸 Chat Completions JSON
// 恢复为标准 SSE data 帧。Codex executor 在响应格式为 openai 时返回裸 JSON，
// 若直接写出，不仅客户端无法按 SSE 解析，完成标志与 usage 的旁路扫描也会失效。
// 已经带 SSE 字段的 payload 保持原样，避免重复添加 data 前缀。
func frameChatCompletionsPayload(payload []byte) []byte {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || hasSSEField(trimmed) {
		return payload
	}
	if !bytes.Equal(trimmed, []byte("[DONE]")) && !json.Valid(trimmed) {
		return payload
	}
	framed := make([]byte, 0, len(trimmed)+len("data: \n\n"))
	framed = append(framed, "data: "...)
	framed = append(framed, trimmed...)
	framed = append(framed, '\n', '\n')
	return framed
}

func hasSSEField(payload []byte) bool {
	for len(payload) > 0 {
		line := payload
		if i := bytes.IndexByte(payload, '\n'); i >= 0 {
			line = payload[:i]
			payload = payload[i+1:]
		} else {
			payload = nil
		}
		line = bytes.TrimSpace(line)
		for _, prefix := range [][]byte{
			[]byte("data:"), []byte("event:"), []byte("id:"), []byte("retry:"), []byte(":"),
		} {
			if bytes.HasPrefix(line, prefix) {
				return true
			}
		}
	}
	return false
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
		Type    string `json:"type"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Candidates []struct {
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
	}
	if json.Unmarshal(data, &event) != nil {
		return false
	}
	// response.incomplete 是 Responses 的显式终态（常见原因是 max_output_tokens），
	// CPA 会在转发该事件后正常关闭 chunk 通道；它不是传输中断。
	if event.Type == "response.completed" || event.Type == "response.incomplete" ||
		event.Type == "response.done" || event.Type == "message_stop" {
		return true
	}
	// Codex 转成 Chat Completions 后，终态是带 finish_reason 的 choices chunk，
	// CPA 不保证额外补发 [DONE]，因此需要直接识别协议终态。
	for _, choice := range event.Choices {
		if strings.TrimSpace(choice.FinishReason) != "" {
			return true
		}
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

// responsesPayloadHasContent 判断 Responses SSE 帧是否包含真实输出。
// 生命周期事件可能含空 output/content 字段，不能据此记录首字；除增量事件外，
// 部分上游只在 done/终态事件中给出最终正文或工具调用，也必须识别。
func responsesPayloadHasContent(payload []byte) bool {
	scanner := bufio.NewScanner(bytes.NewReader(payload))
	scanner.Buffer(make([]byte, 0, 64*1024), 32<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := bytes.TrimSpace([]byte(strings.TrimPrefix(line, "data:")))
		if dto.ResponsesEventHasContent(data) {
			return true
		}
	}
	return false
}

// anthropicPayloadHasContentDelta 判断 Anthropic Messages SSE 是否出现真实内容增量。
// message_start 的 message.content 通常是空数组，不能因为包含 content 字段就提前
// 提交响应头；仅 content_block_delta 代表客户端已经收到不可安全重放的正文。
func anthropicPayloadHasContentDelta(payload []byte) bool {
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
		if json.Unmarshal(data, &event) == nil && event.Type == "content_block_delta" {
			return true
		}
	}
	return false
}
