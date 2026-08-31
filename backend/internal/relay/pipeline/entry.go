package pipeline

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/pkg/multipartform"
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/clientid"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
)

// maxRequestBodyBytes bounds both the bytes accepted from the wire and the
// decoded request body (including multimodal base64 / multipart image data).
const maxRequestBodyBytes = 32 << 20

const maxContentEncodingLayers = 8

// ctxKeyRelayRequestBodyDecoded indicates that readRawBody normalized a
// compressed inbound request before parsing and forwarding it.
const ctxKeyRelayRequestBodyDecoded = "relay_request_body_decoded"

var (
	errUnsupportedContentEncoding = errors.New("unsupported request content encoding")
	errInvalidContentEncoding     = errors.New("invalid request content encoding")
	errDecodedRequestBodyTooLarge = errors.New("decoded request body exceeds limit")
)

// readRawBody reads the bounded wire body, retains those exact bytes in the
// request context for audit, then transparently decodes supported
// Content-Encoding layers. The returned bytes are normalized for parsing,
// moderation, routing, and forwarding. Failures write an entry-protocol error
// response and return (nil, false).
func readRawBody(c *gin.Context) ([]byte, bool) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxRequestBodyBytes)
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(c, http.StatusRequestEntityTooLarge, "invalid_request_error", "request_too_large", "请求体超出大小限制")
			return nil, false
		}
		writeError(c, http.StatusBadRequest, "invalid_request_error", "invalid_body", "读取请求体失败")
		return nil, false
	}
	// 保留客户端发送的原始字节，供请求审计与 Hook 复用。这里只增加切片引用，
	// 不复制请求体；生命周期仍限定在当前请求内。
	c.Set(ctxKeyRelayInboundRequestBody, raw)
	c.Set(ctxKeyRelayRequestBodyDecoded, false)
	contentEncoding := strings.Join(c.Request.Header.Values("Content-Encoding"), ",")
	body, decoded, err := decodeRequestBody(raw, contentEncoding, maxRequestBodyBytes)
	if err != nil {
		switch {
		case errors.Is(err, errUnsupportedContentEncoding):
			writeError(c, http.StatusUnsupportedMediaType, "invalid_request_error", "unsupported_content_encoding", "不支持的 Content-Encoding")
		case errors.Is(err, errDecodedRequestBodyTooLarge):
			writeError(c, http.StatusRequestEntityTooLarge, "invalid_request_error", "request_too_large", "请求体解压后超过大小限制")
		default:
			writeError(c, http.StatusBadRequest, "invalid_request_error", "invalid_content_encoding", "压缩请求体无效或已损坏")
		}
		return nil, false
	}
	c.Set(ctxKeyRelayRequestBodyDecoded, decoded)
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	return body, true
}

// decodeRequestBody decodes Content-Encoding layers in RFC 9110 order:
// codings are listed in the order applied, so decoding runs in reverse. Each
// layer is bounded independently to prevent nested compression bombs.
func decodeRequestBody(raw []byte, contentEncoding string, maxDecodedBytes int64) ([]byte, bool, error) {
	if maxDecodedBytes <= 0 {
		return nil, false, fmt.Errorf("%w: invalid limit", errDecodedRequestBodyTooLarge)
	}
	codings, err := parseContentEncodings(contentEncoding)
	if err != nil {
		return nil, false, err
	}
	if len(codings) == 0 {
		return raw, false, nil
	}

	body := raw
	decoded := false
	for i := len(codings) - 1; i >= 0; i-- {
		coding := codings[i]
		if coding == "identity" {
			continue
		}
		decoded = true
		body, err = decodeRequestBodyLayer(body, coding, maxDecodedBytes)
		if err != nil {
			return nil, false, err
		}
	}
	return body, decoded, nil
}

func parseContentEncodings(value string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	if len(parts) > maxContentEncodingLayers {
		return nil, fmt.Errorf("%w: too many layers", errInvalidContentEncoding)
	}
	codings := make([]string, 0, len(parts))
	for _, part := range parts {
		coding := strings.ToLower(strings.TrimSpace(part))
		if coding == "" {
			return nil, fmt.Errorf("%w: empty coding", errInvalidContentEncoding)
		}
		switch coding {
		case "identity", "gzip", "x-gzip", "zstd":
			codings = append(codings, coding)
		default:
			return nil, fmt.Errorf("%w: %s", errUnsupportedContentEncoding, coding)
		}
	}
	return codings, nil
}

func decodeRequestBodyLayer(input []byte, coding string, maxDecodedBytes int64) ([]byte, error) {
	limit := maxDecodedBytes
	if limit >= int64(^uint(0)>>1) {
		// Avoid overflow in the +1 probe below on unusual 64-bit limits.
		limit = int64(^uint(0)>>1) - 1
	}
	var reader io.Reader
	var closeReader func()
	switch coding {
	case "gzip", "x-gzip":
		zr, err := gzip.NewReader(bytes.NewReader(input))
		if err != nil {
			return nil, fmt.Errorf("%w: gzip header: %v", errInvalidContentEncoding, err)
		}
		reader = zr
		closeReader = func() { _ = zr.Close() }
	case "zstd":
		// A single decoder keeps allocation/concurrency bounded. The library's
		// max-memory/window checks reject hostile frames before a large buffer is
		// allocated.
		windowLimit := uint64(maxDecodedBytes)
		if windowLimit < 1024 {
			windowLimit = 1024
		}
		zr, err := zstd.NewReader(bytes.NewReader(input),
			zstd.WithDecoderConcurrency(1),
			zstd.WithDecoderLowmem(true),
			zstd.WithDecoderMaxMemory(uint64(maxDecodedBytes)),
			zstd.WithDecoderMaxWindow(windowLimit),
		)
		if err != nil {
			return nil, fmt.Errorf("%w: zstd header: %v", errInvalidContentEncoding, err)
		}
		reader = zr
		closeReader = zr.Close
	default:
		return nil, fmt.Errorf("%w: %s", errUnsupportedContentEncoding, coding)
	}
	if closeReader != nil {
		defer closeReader()
	}

	decoded, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		if errors.Is(err, zstd.ErrDecoderSizeExceeded) {
			return nil, fmt.Errorf("%w: %v", errDecodedRequestBodyTooLarge, err)
		}
		return nil, fmt.Errorf("%w: %v", errInvalidContentEncoding, err)
	}
	if int64(len(decoded)) > maxDecodedBytes {
		return nil, fmt.Errorf("%w: got %d bytes", errDecodedRequestBodyTooLarge, len(decoded))
	}
	return decoded, nil
}

func requestBodyWasDecoded(c *gin.Context) bool {
	if c == nil {
		return false
	}
	value, ok := c.Get(ctxKeyRelayRequestBodyDecoded)
	decoded, _ := value.(bool)
	return ok && decoded
}

// readRelayRequest 读取并解析转发请求体（读体上限 / JSON 对象校验），
// 三协议 JSON 入口共用；失败时已按入口协议写出错误体，返回 (nil, false)。
func readRelayRequest(c *gin.Context) (*dto.ChatRequest, bool) {
	body, ok := readRawBody(c)
	if !ok {
		return nil, false
	}

	req, err := dto.ParseChatRequest(body)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "invalid_json", "请求体必须是 JSON 对象")
		return nil, false
	}
	return req, true
}

// HandleChatCompletions POST /v1/chat/completions 入口 handler（OpenAI 协议）。
func (p *Pipeline) HandleChatCompletions(c *gin.Context) {
	setEntryProtocol(c, registry.ProtocolOpenAI)
	keyInfo, ok := requireKeyInfo(c)
	if !ok {
		return
	}
	req, ok := readRelayRequest(c)
	if !ok {
		return
	}
	if req.Model == "" {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "missing_model", "缺少 model 字段")
		return
	}
	p.forward(c, keyInfo, req, adaptor.EndpointChatCompletions)
}

// HandleResponses POST /v1/responses 入口 handler（OpenAI Responses API create）。
//
// 复用 chat completions 的读体/大小/JSON 校验与 forward 主循环，仅端点标识不同：
// 请求体透传（input/instructions/tools 等原样进上游），调度/failover/计费一致。
func (p *Pipeline) HandleResponses(c *gin.Context) {
	setEntryProtocol(c, registry.ProtocolOpenAI)
	keyInfo, ok := requireKeyInfo(c)
	if !ok {
		return
	}
	req, ok := readRelayRequest(c)
	if !ok {
		return
	}
	if req.Model == "" {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "missing_model", "缺少 model 字段")
		return
	}
	// The shared /v1/responses route is used by ordinary OpenAI clients and the
	// official Codex CLI. Keep the historical mixed account/channel selection;
	// once an account is selected, the plugin-wide mode applies only when that
	// account is the canonical Codex platform.
	p.forwardOpt(c, keyInfo, req, adaptor.EndpointResponses, p.codexResponsesForwardOptions(c))
}

// HandleCodexGuardianUnsupported explicitly rejects the internal Codex
// Guardian surfaces until their dedicated protocol contract is implemented.
// Keeping these routes behind API-key authentication is important: an
// unregistered /guardian path would otherwise fall through to the SPA
// NoRoute handler and return a misleading 200/text-html response.  Guardian
// requests must never be silently treated as ordinary Responses or CPA
// translation requests because their review/classifier semantics and headers
// are endpoint-specific.
func (p *Pipeline) HandleCodexGuardianUnsupported(c *gin.Context) {
	setEntryProtocol(c, registry.ProtocolOpenAI)
	writeError(c, http.StatusNotImplemented, "server_error", "unsupported_endpoint",
		"Codex Guardian endpoints are not supported by this gateway")
}

// HandleCodexAgentIdentityJWKSUnsupported reserves the official Agent
// Identity key-discovery contract without pretending that Core can validate or
// relay AgentAssertion credentials. The Codex CLI fetches this endpoint before
// it has an AirGate API-key request context, and the native executor currently
// accepts OAuth/API-key leases only. Returning a structured 501 keeps the
// capability boundary explicit and prevents the global SPA NoRoute handler
// from turning an unsupported auth flow into a misleading 200/text-html.
func (p *Pipeline) HandleCodexAgentIdentityJWKSUnsupported(c *gin.Context) {
	if c == nil {
		return
	}
	setEntryProtocol(c, registry.ProtocolOpenAI)
	c.Header("Cache-Control", "no-store")
	if c.Request == nil || c.Request.Method != http.MethodGet {
		c.Header("Allow", http.MethodGet)
		writeError(c, http.StatusMethodNotAllowed, "invalid_request_error", "method_not_allowed",
			"HTTP method is not supported for the Codex Agent Identity JWKS endpoint")
		return
	}
	writeError(c, http.StatusNotImplemented, "server_error", "agent_identity_unsupported",
		"Codex Agent Identity is not supported by this gateway")
}

// HandleResponsesWebSocketFallback handles a legacy/explicit GET route when a
// caller wires the pipeline without a native WebSocket transport. The normal
// production route uses HandleResponsesWebSocket; HTTP 426 is still the
// official Codex CLI signal to fall back to the regular Responses stream. This
// explicit route also prevents the SPA NoRoute fallback from returning
// 200/text-html, which Codex interprets as a hard WebSocket connection failure.
func (p *Pipeline) HandleResponsesWebSocketFallback(c *gin.Context) {
	c.Header("Connection", "Upgrade")
	c.Header("Upgrade", "websocket")
	c.Header("Sec-WebSocket-Version", "13")
	c.AbortWithStatusJSON(http.StatusUpgradeRequired, gin.H{
		"error": gin.H{
			"type":    "unsupported_transport",
			"code":    "websocket_not_available",
			"message": "Responses WebSocket transport is not available; retry over HTTP/SSE",
		},
	})
}

// HandleAlphaSearch POST /v1/alpha/search 入口 handler（codex CLI 内置联网搜索）。
//
// 复用 responses 的读体/JSON 校验与 forward 主循环：请求体透传（SearchRequest 原样进
// 上游），按 model 路由到 openai 协议渠道。非流式；按次计费（全局价 / 分组覆盖价，
// 见 forward.go 的 alphaSearchPrice），非 2xx 不计费。
// HandleCompact serves the dedicated Codex/OpenAI Responses compact endpoint.
// Compact is unary by contract and must not enter ordinary Responses SSE
// translation even if a client sends a stray stream=true field.
func (p *Pipeline) HandleCompact(c *gin.Context) {
	setEntryProtocol(c, registry.ProtocolOpenAI)
	if !requireCodexClientBoundary(c, adaptor.EndpointCompact) {
		return
	}
	keyInfo, ok := requireKeyInfo(c)
	if !ok {
		return
	}
	req, ok := readRelayRequest(c)
	if !ok {
		return
	}
	if req.Model == "" {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "missing_model", "缺少 model 字段")
		return
	}
	req.Stream = false
	// Compact is an official Codex-only unary contract. It must never be
	// mistaken for a regular Responses request and sent to a CPA channel.
	p.forwardOpt(c, keyInfo, req, adaptor.EndpointCompact, forwardOptions{
		nativeCodexAccountsOnly: true,
	})
}

func (p *Pipeline) HandleAlphaSearch(c *gin.Context) {
	setEntryProtocol(c, registry.ProtocolOpenAI)
	if !requireCodexClientBoundary(c, adaptor.EndpointAlphaSearch) {
		return
	}
	keyInfo, ok := requireKeyInfo(c)
	if !ok {
		return
	}
	req, ok := readRelayRequest(c)
	if !ok {
		return
	}
	req.Stream = false
	if req.Model == "" {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "missing_model", "缺少 model 字段")
		return
	}
	// 搜索响应为一次性 JSON（SearchResponse），恒非流式。
	req.Stream = false
	// Alpha Search has no stable CPA translation contract; keep it on the
	// native Codex account plane even when the plugin policy is cpa_translate.
	p.forwardOpt(c, keyInfo, req, adaptor.EndpointAlphaSearch, forwardOptions{
		nativeCodexAccountsOnly: true,
	})
}

// HandleImagesGenerations POST /v1/images/generations 入口 handler
// （OpenAI Images 协议，JSON 透传；gpt-image / DALL·E 系）。
//
// stream:true 走 SSE 字节级透传：openai adaptor 为图像端点提供透传型观察器
// （imageStreamObserver）旁路捕获 completed 事件的 usage 与产出张数（计次）。
func (p *Pipeline) HandleImagesGenerations(c *gin.Context) {
	setEntryProtocol(c, registry.ProtocolOpenAI)
	keyInfo, ok := requireKeyInfo(c)
	if !ok {
		return
	}
	req, ok := readRelayRequest(c)
	if !ok {
		return
	}
	if req.Model == "" {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "missing_model", "缺少 model 字段")
		return
	}
	// The official Codex CLI uses the same JSON Images contract as OpenAI.
	// Preserve the historical mixed CPA/channel account selection; the
	// plugin-wide mode controls only a canonical Codex account after selection
	// (auto/native may fall back to CPA, native_only does not).
	p.forwardOpt(c, keyInfo, req, adaptor.EndpointImagesGenerations, p.codexImagesForwardOptions(c))
}

// HandleImagesEdits POST /v1/images/edits 入口 handler
// （OpenAI Images 协议，兼容官方 Codex CLI 的 JSON 形态与传统 multipart）。
//
// JSON 请求走透明 ChatRequest DTO（未知字段保留），因此可继续使用普通 JSON
// relay hook、CPA images_edits 专用执行器以及渠道 model_mapping；multipart 请求
// 仍只解析调度字段，原始字节 + 原 Content-Type（含 boundary）交 adaptor，保证
// 文件字节与 boundary 不被重组。
func (p *Pipeline) HandleImagesEdits(c *gin.Context) {
	setEntryProtocol(c, registry.ProtocolOpenAI)
	keyInfo, ok := requireKeyInfo(c)
	if !ok {
		return
	}
	body, ok := readRawBody(c)
	if !ok {
		return
	}
	contentType := c.GetHeader("Content-Type")
	if isJSONMediaType(contentType) {
		req, ok := parseImagesEditsJSON(body, c)
		if !ok {
			return
		}
		// Official Codex image edits are typed JSON (`images[].image_url`).  Keep
		// the raw JSON bytes through the account/native path so unknown fields and
		// provider-specific image options remain byte-for-byte transparent.
		p.forwardOpt(c, keyInfo, req, adaptor.EndpointImagesEdits, p.codexImagesForwardOptions(c))
		return
	}
	fields, err := multipartform.ExtractFields(body, contentType, "model", "stream", "n", "size", "resolution", "quality")
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "invalid_multipart",
			"multipart 请求体解析失败: "+err.Error())
		return
	}
	if fields["model"] == "" {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "missing_model", "缺少 model 字段")
		return
	}

	// 空字段表仅承载调度所需 model/stream；原始 multipart 字节经 rawBody 原样直发上游。
	req, err := dto.ParseChatRequest([]byte("{}"))
	if err != nil {
		writeError(c, http.StatusInternalServerError, "server_error", "internal_error", "内部错误")
		return
	}
	req.Model = fields["model"]
	req.Stream = fields["stream"] == "true"
	for _, key := range []string{"n", "size", "resolution", "quality"} {
		if fields[key] != "" {
			_ = req.Set(key, fields[key])
		}
	}
	p.forwardOpt(c, keyInfo, req, adaptor.EndpointImagesEdits, forwardOptions{
		rawBody:        body,
		rawContentType: contentType,
	})
}

// isJSONMediaType accepts application/json and vendor +json media types while
// keeping multipart handling strict. Parameters such as charset are ignored.
func isJSONMediaType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil {
		return false
	}
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}

func parseImagesEditsJSON(body []byte, c *gin.Context) (*dto.ChatRequest, bool) {
	req, err := dto.ParseChatRequest(body)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "invalid_json", "请求体必须是 JSON 对象")
		return nil, false
	}
	if strings.TrimSpace(req.Model) == "" {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "missing_model", "缺少 model 字段")
		return nil, false
	}
	return req, true
}

// HandleMessages POST /v1/messages 入口 handler（Anthropic Messages 协议，纯透传）。
// Anthropic 请求体顶层同样有 model / stream 字段，dto.ChatRequest 的透明字段表直接复用；
// 只路由到 anthropic 类型渠道（Pick 协议过滤），错误体走 Anthropic 原生形态。
func (p *Pipeline) HandleMessages(c *gin.Context) {
	setEntryProtocol(c, registry.ProtocolAnthropic)
	keyInfo, ok := requireKeyInfo(c)
	if !ok {
		return
	}
	req, ok := readRelayRequest(c)
	if !ok {
		return
	}
	if req.Model == "" {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "missing_model", "缺少 model 字段")
		return
	}
	p.forward(c, keyInfo, req, adaptor.EndpointMessages)
}

// HandleMessagesCountTokens POST /v1/messages/count_tokens 入口 handler
// （Anthropic 协议，透传 + 零计费）。
// 余额预检 / failover / 错误形态与 /v1/messages 完全一致；
// 成功不产生 usage_log、不扣任何费用（zeroBilling 选项）。
func (p *Pipeline) HandleMessagesCountTokens(c *gin.Context) {
	setEntryProtocol(c, registry.ProtocolAnthropic)
	keyInfo, ok := requireKeyInfo(c)
	if !ok {
		return
	}
	req, ok := readRelayRequest(c)
	if !ok {
		return
	}
	if req.Model == "" {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "missing_model", "缺少 model 字段")
		return
	}
	p.forwardOpt(c, keyInfo, req, adaptor.EndpointMessagesCountTokens, forwardOptions{zeroBilling: true})
}

// HandleGenerateContent POST /v1beta/models/{model}:{动词} 入口 handler（Gemini 协议，纯透传）。
// 支持动词：generateContent / streamGenerateContent / predict（Imagen 生图）/
// countTokens（token 计数，零计费）。
//
// gin 的 ':' 参数语法只认 '/' 分隔，"{model}:generateContent" 整段落进单个
// path 参数（见 router.go 注册），此处自行按最后一个 ':' 切出 model 与动词：
// model 取自 URL（Gemini 请求体不携带 model），stream 由动词决定
// （streamGenerateContent 恒按 SSE 处理，上游侧 adaptor 强制 ?alt=sse；
// predict / countTokens 恒非流式）。
func (p *Pipeline) HandleGenerateContent(c *gin.Context) {
	setEntryProtocol(c, registry.ProtocolGemini)
	keyInfo, ok := requireKeyInfo(c)
	if !ok {
		return
	}

	raw := c.Param("modelAction")
	idx := strings.LastIndex(raw, ":")
	if idx <= 0 || idx == len(raw)-1 {
		writeError(c, http.StatusNotFound, "invalid_request_error", "invalid_path",
			"路径须为 /v1beta/models/{model}:generateContent|:streamGenerateContent|:predict|:countTokens")
		return
	}
	model, action := raw[:idx], raw[idx+1:]

	var (
		stream   bool
		endpoint string
		opts     forwardOptions
	)
	switch action {
	case "generateContent":
		endpoint = adaptor.EndpointGenerateContent
	case "streamGenerateContent":
		endpoint = adaptor.EndpointGenerateContent
		stream = true
	case "predict":
		// Imagen 系按次生图（无流式形态）；按次×张数计费见 pricing.ComputeCosts。
		endpoint = adaptor.EndpointPredict
	case "countTokens":
		// token 计数：透传但零计费（不产生 usage_log）；余额预检照常走以防滥用。
		endpoint = adaptor.EndpointCountTokens
		opts.zeroBilling = true
	default:
		writeError(c, http.StatusNotFound, "invalid_request_error", "unknown_method",
			"不支持的方法 "+action+"（仅支持 generateContent / streamGenerateContent / predict / countTokens）")
		return
	}

	req, ok := readRelayRequest(c)
	if !ok {
		return
	}
	// model / stream 由 URL 决定（不写入字段表：请求体原样透传，Gemini 体内本无 model）。
	req.Model = model
	req.Stream = stream

	p.forwardOpt(c, keyInfo, req, endpoint, opts)
}

// modelItem GET /v1/models 的单条模型（OpenAI 格式 + 非标 protocols 扩展）。
// Protocols 标注该模型可经哪些入口协议调用（openai/anthropic/gemini），
// 供第一方应用按模型选端点；标准 OpenAI 客户端会忽略未知字段，不破坏兼容。
type modelItem struct {
	ID        string   `json:"id"`
	Object    string   `json:"object"`
	Created   int64    `json:"created"`
	OwnedBy   string   `json:"owned_by"`
	Protocols []string `json:"protocols,omitempty"`
}

// modelList GET /v1/models 响应（OpenAI 格式）。
type modelList struct {
	Object string      `json:"object"`
	Data   []modelItem `json:"data"`
}

// HandleModels GET /v1/models：按 keyInfo 分组聚合「渠道 + 账号池」可用模型并集
// （全协议全量列出，每条带 protocols 协议集合）。
func (p *Pipeline) HandleModels(c *gin.Context) {
	// Current Codex clients append a whole `client_version` when refreshing
	// their provider-owned catalog and send the standard Codex originator/UA.
	// The shared /v1/models and /models aliases are also used by ordinary
	// OpenAI clients, so query-key presence alone is not a safe discriminator.
	// Prefer explicit route/header signals and keep a strict semver query as a
	// backwards-compatible weak signal for clients that strip headers.
	if isCodexModelRequest(c) {
		p.HandleCodexModels(c)
		return
	}

	setEntryProtocol(c, registry.ProtocolOpenAI)
	keyInfo, ok := requireKeyInfo(c)
	if !ok {
		return
	}

	entries := p.modelEntriesForGroup(keyInfo.GroupID)
	items := make([]modelItem, 0, len(entries))
	for _, e := range entries {
		items = append(items, modelItem{ID: e.Name, Object: "model", Created: 0, OwnedBy: "airgate", Protocols: e.Protocols})
	}
	c.JSON(http.StatusOK, modelList{Object: "list", Data: items})
}

func isCodexModelRequest(c *gin.Context) bool {
	// The official Codex client sends its Originator/User-Agent identity on the
	// shared models aliases as it does on every other API request.  A bare
	// `client_version=1.2.3` query is not an identity signal: ordinary OpenAI
	// callers and compatibility probes can provide the same shape, and must not
	// be switched to the Codex catalog or native account pool.
	return isCodexClientRequest(c)
}

// isCodexClientRequest recognizes the official Codex CLI on shared public
// aliases. Route-specific prefixes are strong signals; the standard
// Originator/UA headers cover clients using a plain /v1 base URL. The
// client_version query is deliberately excluded here: Codex uses it for the
// shared models catalog, but it is not a general client-identity signal and
// must not move an ordinary OpenAI request to the native account pool.
func isCodexClientRequest(c *gin.Context) bool {
	if c == nil {
		return false
	}
	// Keep the client identity consistent with clientid.Detect: Claude Code has
	// explicit precedence when a proxy forwards mixed/stale headers.  Without
	// this guard, a Codex-looking route could select native Codex accounts while
	// relayClientType still reported `claude_code`, causing hooks and routing to
	// disagree for the same request.
	if clientType := clientid.Get(c); clientType != "" {
		return clientType == clientid.Codex
	}
	if c.Request != nil {
		if clientType := clientid.ClassifyRequest(c.Request); clientType != "" {
			return clientType == clientid.Codex
		}
	}
	// Only dedicated Codex namespaces are strong path signals.  `/v1`,
	// `/backend-api`, and `/wham` are shared roots used by ordinary OpenAI or
	// ChatGPT clients; those requests must carry an official Codex identity
	// header (Originator/UA/client UA or a known Codex metadata header).
	if fullPath := strings.ToLower(strings.TrimSpace(c.FullPath())); isDedicatedCodexRoutePath(fullPath) {
		return true
	}
	if c.Request == nil || c.Request.URL == nil {
		return false
	}
	path := strings.ToLower(strings.TrimSpace(c.Request.URL.Path))
	if isDedicatedCodexRoutePath(path) {
		return true
	}
	return false
}

// requireCodexClientBoundary rejects ordinary callers on endpoints whose
// wire contract is owned exclusively by the official Codex client.  Several
// of those endpoints are mounted below shared aliases such as /v1,
// /backend-api, and /wham so that a Codex installation can use whichever base
// URL it was given.  The route shape alone is therefore not sufficient to
// enable the native Codex account pool: an ordinary OpenAI/ChatGPT caller on
// the same alias must receive a structured 404 instead of being silently
// routed to a Codex account.
//
// Dedicated /codex, /api/codex, and /backend-api/codex paths are accepted by
// isCodexClientRequest as strong path signals.  Shared aliases require an
// official Originator/User-Agent/client metadata signal.
func requireCodexClientBoundary(c *gin.Context, endpoint string) bool {
	if isCodexClientRequest(c) {
		return true
	}
	if c == nil {
		return false
	}
	message := "Codex endpoint requires an official Codex client"
	if strings.TrimSpace(endpoint) != "" {
		message = "Codex " + strings.TrimSpace(endpoint) + " endpoint requires an official Codex client"
	}
	writeError(c, http.StatusNotFound, "invalid_request_error", "unsupported_endpoint", message)
	return false
}

// isDedicatedCodexRoutePath recognizes only path namespaces owned by Codex.
// Shared backend/public roots deliberately do not qualify because ordinary
// OpenAI/ChatGPT clients can legitimately use the same endpoint suffixes.
func isDedicatedCodexRoutePath(path string) bool {
	path = strings.TrimRight(strings.ToLower(strings.TrimSpace(path)), "/")
	if path == "" {
		return false
	}
	for _, prefix := range []string{
		"/codex/v1", "/codex",
		"/api/codex/v1", "/api/codex",
		"/backend-api/codex/v1", "/backend-api/codex",
	} {
		if suffix, ok := codexRouteSuffix(path, prefix); ok {
			return suffix != "" && isKnownCodexBackendRouteSuffix(suffix)
		}
	}
	return false
}

func isCodexBackendRoutePath(path string) bool {
	path = strings.TrimRight(strings.ToLower(strings.TrimSpace(path)), "/")
	for _, prefix := range []string{"/backend-api/codex", "/backend-api/codex/v1"} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

// isCodexRoutePath recognizes the finite Codex route families exposed by the
// gateway. Explicit `/codex` namespaces are strong signals. `/backend-api`
// and `/wham`, however, are shared backend roots, so the root (or an arbitrary
// child path) must not identify a request as Codex: only a known Codex endpoint
// family below those aliases is accepted.
func isCodexRoutePath(path string) bool {
	path = strings.TrimRight(strings.ToLower(strings.TrimSpace(path)), "/")
	if path == "" {
		return false
	}

	// These path segments are dedicated to Codex. Keep the versioned prefixes
	// first so their suffix is parsed correctly. A bare namespace is not an
	// endpoint and therefore is deliberately rejected below.
	for _, prefix := range []string{
		"/codex/v1", "/codex",
		"/api/codex/v1", "/api/codex",
		"/backend-api/codex/v1", "/backend-api/codex",
	} {
		if suffix, ok := codexRouteSuffix(path, prefix); ok {
			return suffix != "" && isKnownCodexBackendRouteSuffix(suffix)
		}
	}

	// WHAM is a backend-client/control-plane namespace, but the bare root and
	// unknown children are not sufficient client identity signals. This avoids
	// enabling Codex-only instruction/routing behavior for another application
	// that merely happens to sit behind a /wham reverse-proxy prefix.
	for _, prefix := range []string{
		"/backend-api/v1/wham",
		"/backend-api/wham/v1",
		"/backend-api/wham",
		"/wham/v1",
		"/wham",
	} {
		if suffix, ok := codexRouteSuffix(path, prefix); ok {
			return suffix != "" && isKnownCodexWhamRouteSuffix(suffix)
		}
	}

	// Some official builds receive a base URL already rooted at backend-api and
	// append a registered Codex endpoint directly. The backend-api roots remain
	// shared: accepting only this finite set prevents /backend-api itself or an
	// unrelated backend endpoint from being treated as a Codex client.
	for _, prefix := range []string{"/backend-api/v1", "/backend-api"} {
		if suffix, ok := codexRouteSuffix(path, prefix); ok {
			return suffix != "" && isKnownCodexBackendRouteSuffix(suffix)
		}
	}
	return false
}

// codexRouteSuffix strips prefix only at a complete path-segment boundary.
// The returned suffix never has a leading or trailing slash.
func codexRouteSuffix(path, prefix string) (string, bool) {
	if path == prefix {
		return "", true
	}
	if !strings.HasPrefix(path, prefix+"/") {
		return "", false
	}
	suffix := strings.TrimPrefix(path, prefix+"/")
	// `path` was already right-trimmed by isCodexRoutePath. Preserve the
	// segment boundaries here instead of trimming both sides: a doubled slash
	// is a malformed/ambiguous route and must not become a valid Codex signal.
	if suffix == "" || strings.HasPrefix(suffix, "/") || strings.Contains(suffix, "//") {
		return "", true
	}
	return suffix, true
}

// isKnownCodexWhamRouteSuffix covers the finite top-level families registered
// by the backend-client, control-plane, plugin and Remote Control handlers.
// Matching a complete first segment keeps similarly named paths such as
// /environmentsx or /remote-other outside the Codex boundary; the endpoint
// handlers apply their stricter full-path and method allowlists afterwards.
func isKnownCodexWhamRouteSuffix(suffix string) bool {
	return hasCodexRouteRoot(suffix, []string{
		"usage",
		"rate-limit-reset-credits",
		"accounts",
		"agent-identities",
		"profiles",
		"config",
		"settings",
		"tasks",
		"environments",
		"workspace-messages",
		"ps",
		"connectors",
		"plugins",
		"public",
		"remote",
		"alpha",
		"analytics-events",
		"analytics",
	})
}

func isKnownCodexBackendRouteSuffix(suffix string) bool {
	if isKnownCodexWhamRouteSuffix(suffix) {
		return true
	}
	return hasCodexRouteRoot(suffix, []string{
		"responses",
		"images",
		"models",
		"realtime",
		"live",
		"memories",
		"guardian",
		"guardian-classifier",
		"files",
	})
}

func hasCodexRouteRoot(suffix string, roots []string) bool {
	root, _, _ := strings.Cut(strings.Trim(suffix, "/"), "/")
	for _, candidate := range roots {
		if root == candidate {
			return true
		}
	}
	return false
}

// isWholeClientVersion accepts the format emitted by Codex's
// client_version_to_whole helper: exactly three non-negative decimal
// components. It intentionally rejects prerelease/build suffixes so arbitrary
// query parameters cannot silently switch the shared OpenAI model contract.
func isWholeClientVersion(value string) bool {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

// geminiModelItem GET /v1beta/models 的单条模型（Gemini ListModels 最小合法子集）。
type geminiModelItem struct {
	Name                       string   `json:"name"`
	DisplayName                string   `json:"displayName"`
	SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
}

// geminiModelList GET /v1beta/models 响应（Gemini 原生形态）。
type geminiModelList struct {
	Models []geminiModelItem `json:"models"`
}

// HandleGeminiModels GET /v1beta/models：仅列出可经 gemini 协议调用的模型
// （原生 SDK 用户拿到的目录必须都能在本入口调通），渲染为 Gemini 原生
// {"models":[{"name":"models/<id>",...}]} 最小合法形态。
// 口径与 /v1/models 相同：渠道 + 账号池按分组并集，再滤 protocols 含 gemini。
func (p *Pipeline) HandleGeminiModels(c *gin.Context) {
	setEntryProtocol(c, registry.ProtocolGemini)
	keyInfo, ok := requireKeyInfo(c)
	if !ok {
		return
	}

	entries := p.modelEntriesForGroup(keyInfo.GroupID)
	items := make([]geminiModelItem, 0, len(entries))
	for _, e := range entries {
		if !slices.Contains(e.Protocols, registry.ProtocolGemini) {
			continue
		}
		items = append(items, geminiModelItem{
			Name:                       "models/" + e.Name,
			DisplayName:                e.Name,
			SupportedGenerationMethods: []string{"generateContent", "streamGenerateContent", "countTokens"},
		})
	}
	c.JSON(http.StatusOK, geminiModelList{Models: items})
}

// accountModelProtocols 账号路径经 CPA 翻译，可经下列入口协议调用。
var accountModelProtocols = []string{
	registry.ProtocolOpenAI,
	registry.ProtocolAnthropic,
	registry.ProtocolGemini,
}

// modelEntriesForGroup 聚合渠道注册表与账号池在指定分组下的模型目录。
// 同名模型合并 protocols 并集；结果按模型名字典序。
//
// 未在价目表标价的模型一律剔除：与 forward/task 缺价预检 fail-closed 一致，
// 目录里出现的模型必须可被下游实际调用（渠道与账号路径相同）。
func (p *Pipeline) modelEntriesForGroup(groupID int) []registry.ModelEntry {
	// 无价目表时 fail-closed：不暴露任何模型（避免缺价预检侧 panic/漏拦时目录仍放行）。
	if p.pricing == nil {
		return nil
	}

	set := map[string]map[string]struct{}{}
	merge := func(name string, protos []string) {
		if name == "" {
			return
		}
		if _, priced := p.pricing.Get(name); !priced {
			return
		}
		if set[name] == nil {
			set[name] = map[string]struct{}{}
		}
		for _, proto := range protos {
			if proto != "" {
				set[name][proto] = struct{}{}
			}
		}
	}

	if p.registry != nil {
		for _, e := range p.registry.ModelEntriesForGroup(groupID) {
			merge(e.Name, e.Protocols)
		}
	}
	if p.accounts != nil {
		for _, name := range p.accounts.ModelsForGroup(groupID) {
			merge(name, accountModelProtocols)
		}
	}

	entries := make([]registry.ModelEntry, 0, len(set))
	for name, protos := range set {
		ps := make([]string, 0, len(protos))
		for proto := range protos {
			ps = append(ps, proto)
		}
		sort.Strings(ps)
		entries = append(entries, registry.ModelEntry{Name: name, Protocols: ps})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries
}

// requireKeyInfo 从 gin ctx 取 APIKeyAuth 写入的 keyInfo；缺失（装配错误）写 401。
func requireKeyInfo(c *gin.Context) (*auth.APIKeyInfo, bool) {
	value, exists := c.Get(middleware.CtxKeyKeyInfo)
	if !exists {
		writeError(c, http.StatusUnauthorized, "authentication_error", "missing_api_key", "缺少 API Key")
		return nil, false
	}
	keyInfo, ok := value.(*auth.APIKeyInfo)
	if !ok || keyInfo == nil {
		writeError(c, http.StatusUnauthorized, "authentication_error", "invalid_api_key", "API Key 信息无效")
		return nil, false
	}
	return keyInfo, true
}
