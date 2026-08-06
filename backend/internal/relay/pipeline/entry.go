package pipeline

import (
	"errors"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/pkg/multipartform"
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
)

// maxRequestBodyBytes 转发请求体上限（含多模态 base64 / multipart 图像内容留足余量）。
const maxRequestBodyBytes = 32 << 20

// readRawBody 读取原始请求体（统一读体上限），各协议入口共用；
// 失败时已按入口协议写出错误体，返回 (nil, false)。
func readRawBody(c *gin.Context) ([]byte, bool) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxRequestBodyBytes)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(c, http.StatusRequestEntityTooLarge, "invalid_request_error", "request_too_large", "请求体超出大小限制")
			return nil, false
		}
		writeError(c, http.StatusBadRequest, "invalid_request_error", "invalid_body", "读取请求体失败")
		return nil, false
	}
	// 保留客户端发送的原始字节，供极窄范围的 Codex 限流竞态诊断使用。
	// 这里只增加切片引用，不复制请求体；生命周期仍限定在当前请求内。
	c.Set(ctxKeyRelayInboundRequestBody, body)
	return body, true
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
	p.forward(c, keyInfo, req, adaptor.EndpointResponses)
}

// HandleAlphaSearch POST /v1/alpha/search 入口 handler（codex CLI 内置联网搜索）。
//
// 复用 responses 的读体/JSON 校验与 forward 主循环：请求体透传（SearchRequest 原样进
// 上游），按 model 路由到 openai 协议渠道。非流式；按次计费（全局价 / 分组覆盖价，
// 见 forward.go 的 alphaSearchPrice），非 2xx 不计费。
func (p *Pipeline) HandleAlphaSearch(c *gin.Context) {
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
	// 搜索响应为一次性 JSON（SearchResponse），恒非流式。
	req.Stream = false
	p.forward(c, keyInfo, req, adaptor.EndpointAlphaSearch)
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
	p.forward(c, keyInfo, req, adaptor.EndpointImagesGenerations)
}

// HandleImagesEdits POST /v1/images/edits 入口 handler
// （OpenAI Images 协议，multipart/form-data 原样透传）。
//
// 只从 multipart 中解析 model（路由用）与 stream（流式判定：SSE 透传 + 观察器计量），
// 原始字节 + 原 Content-Type（含 boundary）经 forwardOptions.rawBody 交 adaptor：
// 渠道 model_mapping 未生效时原样直发（零重组）；生效时 adaptor 仅定点重写
// model 字段值（模型重写属 adaptor 职责，文件字节与 boundary 不变）。
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
