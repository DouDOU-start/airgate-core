// Package dto 定义 relay 管线的对外协议数据结构（canonical：OpenAI chat completions）。
//
// 设计要点：请求体不做全量类型化解析——正常路径只为顶层字段建立零拷贝索引，
// 仅在确实改写字段时才懒构造 map[string]json.RawMessage；调度只显式解析
// model / stream / stream_options，其余字段原样透传，避免 DTO 落后于上游演进。
package dto

import (
	"encoding/json"
	"errors"
)

// ErrInvalidBody 表示请求体不是合法的 JSON 对象。
var ErrInvalidBody = errors.New("请求体必须是 JSON 对象")

// ChatRequest OpenAI chat completions 请求的透明载体。
// fields 是改写请求时才构造的字段表，Model/Stream 为显式解析出的调度字段。
type ChatRequest struct {
	fields map[string]json.RawMessage
	// rawFields 保存顶层字段在 raw 中的起止位置。正常透传请求只建立轻量索引，
	// 不再让 encoding/json 为 input/messages 等超大字段复制一份 RawMessage。
	rawFields map[string]rawFieldSpan
	// raw 保存校验通过的原始 JSON。请求未发生字段改写时，Marshal 直接复用该切片，
	// 避免 Relay Hook、内容审核、账号转发在热路径上反复编码大请求体。
	// 该切片只读；Set/Remove 会清空它并回退到 fields 序列化。
	raw []byte

	// Model 对外模型名（请求原始值）。
	Model string
	// Stream 是否流式请求。
	Stream bool
}

type rawFieldSpan struct {
	start int
	end   int
}

// ParseChatRequest 校验请求体并为顶层字段建立索引（未知字段透传），
// 显式解析 model / stream。非 JSON 对象报 ErrInvalidBody。
func ParseChatRequest(body []byte) (*ChatRequest, error) {
	// json.Valid 保留严格 JSON 校验；随后只扫描顶层键值边界建立零拷贝索引。
	// 相比反序列化 map[string]RawMessage，此路径不会复制数 MB 的 input/messages。
	if !json.Valid(body) {
		return nil, ErrInvalidBody
	}
	rawFields, ok := indexTopLevelJSONFields(body)
	if !ok {
		return nil, ErrInvalidBody
	}
	req := &ChatRequest{rawFields: rawFields, raw: body}
	if raw, ok := req.Get("model"); ok {
		// model 非字符串时保持空串，由入口校验兜底报 400。
		_ = json.Unmarshal(raw, &req.Model)
	}
	if raw, ok := req.Get("stream"); ok {
		_ = json.Unmarshal(raw, &req.Stream)
	}
	return req, nil
}

// indexTopLevelJSONFields 为已经通过 json.Valid 的对象建立顶层值切片索引。
// 扫描器只识别字符串边界与嵌套深度，不复制字段值；重复键以后出现者为准，
// 与 encoding/json 反序列化对象的行为一致。
func indexTopLevelJSONFields(body []byte) (map[string]rawFieldSpan, bool) {
	i := skipJSONSpace(body, 0)
	if i >= len(body) || body[i] != '{' {
		return nil, false
	}
	i++
	fields := make(map[string]rawFieldSpan)
	for {
		i = skipJSONSpace(body, i)
		if i >= len(body) {
			return nil, false
		}
		if body[i] == '}' {
			return fields, true
		}
		if body[i] != '"' {
			return nil, false
		}
		keyStart := i
		keyEnd := scanJSONStringEnd(body, keyStart)
		if keyEnd <= keyStart {
			return nil, false
		}
		var key string
		if err := json.Unmarshal(body[keyStart:keyEnd], &key); err != nil {
			return nil, false
		}
		i = skipJSONSpace(body, keyEnd)
		if i >= len(body) || body[i] != ':' {
			return nil, false
		}
		i = skipJSONSpace(body, i+1)
		valueStart := i
		valueEnd := scanJSONValueEnd(body, valueStart)
		if valueEnd <= valueStart {
			return nil, false
		}
		fields[key] = rawFieldSpan{start: valueStart, end: valueEnd}
		i = skipJSONSpace(body, valueEnd)
		if i >= len(body) {
			return nil, false
		}
		switch body[i] {
		case ',':
			i++
		case '}':
			return fields, true
		default:
			return nil, false
		}
	}
}

func scanJSONValueEnd(body []byte, start int) int {
	if start >= len(body) {
		return -1
	}
	switch body[start] {
	case '"':
		return scanJSONStringEnd(body, start)
	case '{', '[':
		depth := 0
		inString := false
		for i := start; i < len(body); i++ {
			current := body[i]
			if current == '"' && !isEscapedJSONByte(body, i) {
				inString = !inString
				continue
			}
			if inString {
				continue
			}
			switch current {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					return i + 1
				}
			}
		}
	default:
		i := start
		for i < len(body) && body[i] != ',' && body[i] != '}' && !isJSONSpaceByte(body[i]) {
			i++
		}
		return i
	}
	return -1
}

func scanJSONStringEnd(body []byte, start int) int {
	for i := start + 1; i < len(body); i++ {
		if body[i] == '"' && !isEscapedJSONByte(body, i) {
			return i + 1
		}
	}
	return -1
}

func isEscapedJSONByte(body []byte, index int) bool {
	slashes := 0
	for index--; index >= 0 && body[index] == '\\'; index-- {
		slashes++
	}
	return slashes%2 == 1
}

func skipJSONSpace(body []byte, index int) int {
	for index < len(body) && isJSONSpaceByte(body[index]) {
		index++
	}
	return index
}

func isJSONSpaceByte(value byte) bool {
	return value == ' ' || value == '\t' || value == '\r' || value == '\n'
}

// Clone 浅拷贝字段表：RawMessage 值只读共享，改写只发生在 map 层。
// failover 换渠道时各 attempt 的改写（model 重写 / param_override）互不污染。
func (r *ChatRequest) Clone() *ChatRequest {
	if r == nil {
		return nil
	}
	var fields map[string]json.RawMessage
	if r.fields != nil {
		fields = make(map[string]json.RawMessage, len(r.fields))
		for k, v := range r.fields {
			fields[k] = v
		}
	}
	return &ChatRequest{
		fields: fields, rawFields: r.rawFields, raw: r.raw,
		Model: r.Model, Stream: r.Stream,
	}
}

// Get 返回字段原始 JSON 值。
func (r *ChatRequest) Get(key string) (json.RawMessage, bool) {
	if r == nil {
		return nil, false
	}
	if r.fields != nil {
		raw, ok := r.fields[key]
		return raw, ok
	}
	span, ok := r.rawFields[key]
	if !ok || span.start < 0 || span.end < span.start || span.end > len(r.raw) {
		return nil, false
	}
	return json.RawMessage(r.raw[span.start:span.end]), true
}

// IncludeUsageRequested 判断客户端是否显式请求 stream_options.include_usage=true。
// 未带 stream_options、字段非对象或 include_usage 非 true 均返回 false——
// 网关会强制向上游注入 include_usage 以保证计费，透传层据此决定
// 是否把 usage-only chunk 下发给客户端。
func (r *ChatRequest) IncludeUsageRequested() bool {
	raw, ok := r.Get("stream_options")
	if !ok {
		return false
	}
	var opts struct {
		IncludeUsage bool `json:"include_usage"`
	}
	if err := json.Unmarshal(raw, &opts); err != nil {
		return false
	}
	return opts.IncludeUsage
}

// Set 设置字段值（v 会被 JSON 序列化）。
func (r *ChatRequest) Set(key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	r.materializeFields()
	r.fields[key] = raw
	r.raw = nil
	r.rawFields = nil
	return nil
}

// Remove 删除字段。
func (r *ChatRequest) Remove(key string) {
	if r == nil {
		return
	}
	r.materializeFields()
	delete(r.fields, key)
	r.raw = nil
	r.rawFields = nil
}

// Marshal 返回可转发 JSON。未修改请求直接复用入口原始字节；字段发生改写后才
// 序列化 fields（未知字段仍原样保留，键序为 Go map 序列化的字典序）。
func (r *ChatRequest) Marshal() ([]byte, error) {
	if r != nil && r.raw != nil {
		return r.raw, nil
	}
	if r == nil {
		return nil, ErrInvalidBody
	}
	r.materializeFields()
	return json.Marshal(r.fields)
}

// materializeFields 仅在请求确实发生字段改写时构造字段表；各 RawMessage 仍引用
// 原始请求切片，避免为了改一个 model 就复制其余超大字段。
func (r *ChatRequest) materializeFields() {
	if r == nil || r.fields != nil {
		return
	}
	r.fields = make(map[string]json.RawMessage, len(r.rawFields))
	for key, span := range r.rawFields {
		if span.start < 0 || span.end < span.start || span.end > len(r.raw) {
			continue
		}
		r.fields[key] = json.RawMessage(r.raw[span.start:span.end])
	}
}

// Usage 一次请求的 token 用量（上游口径：PromptTokens 包含 CachedTokens）。
type Usage struct {
	PromptTokens        int
	CompletionTokens    int
	CachedTokens        int
	CacheCreationTokens int
	// CacheCreation5mTokens / CacheCreation1hTokens Claude 双档缓存写入明细
	// （Anthropic usage.cache_creation.ephemeral_5m/1h_input_tokens）；OpenAI 上游恒 0。
	CacheCreation5mTokens int
	CacheCreation1hTokens int
	// Calls 按次计费的计次数：图像端点由 adaptor 从响应 data/predictions 数组长度提取
	//（产出张数）；其余端点恒 0，计费侧（pricing.ComputeCosts）把 0 视为 1 次。
	Calls int
	// ImageSize / ImageQuality 图像端点实际产出的分辨率与质量档
	//（gpt-image 系响应顶层 size/quality 字段，以响应为准而非请求参数——
	// 覆盖 size:"auto" 与 edits multipart 场景）；供分辨率价表计费与落账留痕，
	// 其余端点恒空。
	ImageSize    string
	ImageQuality string
}

// usageWire usage 字段的双协议命名兼容解析载体。
type usageWire struct {
	// OpenAI chat completions 风格
	PromptTokens        *int `json:"prompt_tokens"`
	CompletionTokens    *int `json:"completion_tokens"`
	TotalTokens         *int `json:"total_tokens"`
	PromptTokensDetails *struct {
		CachedTokens        *int `json:"cached_tokens"`
		CacheWriteTokens    *int `json:"cache_write_tokens"`
		CacheCreationTokens *int `json:"cache_creation_tokens"`
	} `json:"prompt_tokens_details"`

	// OpenAI Responses 风格（input_tokens/output_tokens + details 子对象）。
	// 计数字段 input_tokens/output_tokens 与 Anthropic 同名，复用下方回退候选，
	// 仅 details 子对象命名不同（cached_tokens 在 input_tokens_details）。
	InputTokensDetails *struct {
		CachedTokens        *int `json:"cached_tokens"`
		CacheWriteTokens    *int `json:"cache_write_tokens"`
		CacheCreationTokens *int `json:"cache_creation_tokens"`
	} `json:"input_tokens_details"`
	CompletionTokensDetails *struct {
		ReasoningTokens *int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
	OutputTokensDetails *struct {
		ReasoningTokens *int `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`

	// Anthropic 风格（缺省回退）
	InputTokens              *int `json:"input_tokens"`
	OutputTokens             *int `json:"output_tokens"`
	CacheReadInputTokens     *int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens *int `json:"cache_creation_input_tokens"`
	// Anthropic 双档缓存写入明细（usage.cache_creation 子对象）。
	CacheCreation *struct {
		Ephemeral5mInputTokens *int `json:"ephemeral_5m_input_tokens"`
		Ephemeral1hInputTokens *int `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

// ParseUsage 解析 usage 对象：OpenAI 命名优先，Anthropic 字段仅作值回退。
// input_tokens 按“已含缓存读取的总输入”解释；若调用方确认响应是 Anthropic
// 原生计数语义，应使用 ParseAnthropicUsage。
// raw 不是对象或不含任何已知计数字段时返回 (Usage{}, false)。
// 上游为不可信第三方：负数计数一律钳 0，防虚增计费/负成本入账。
func ParseUsage(raw []byte) (Usage, bool) {
	return parseUsage(raw, false)
}

// ParseAnthropicUsage 解析 Anthropic usage：input_tokens 是未命中缓存的新鲜输入，
// cache_read_input_tokens 单列，因此 PromptTokens 需要合并二者以符合计费总输入口径。
func ParseAnthropicUsage(raw []byte) (Usage, bool) {
	return parseUsage(raw, true)
}

// ParseGeminiTranslatedOpenAIUsage 解析 CPA 将 Gemini 系响应翻译成 OpenAI 后的 usage。
// 当前 CPA 翻译器把思考 token 只写入 reasoning_tokens 明细，未并入输出总量；
// toolUsePromptTokenCount 未单列，但仍包含在 total_tokens 中，因此按总量差额补入输入。
func ParseGeminiTranslatedOpenAIUsage(raw []byte) (Usage, bool) {
	u, found := parseUsage(raw, false)
	if !found {
		return Usage{}, false
	}
	var w usageWire
	if err := json.Unmarshal(raw, &w); err != nil {
		return Usage{}, false
	}
	reasoning := 0
	if w.CompletionTokensDetails != nil && w.CompletionTokensDetails.ReasoningTokens != nil {
		reasoning = *w.CompletionTokensDetails.ReasoningTokens
	} else if w.OutputTokensDetails != nil && w.OutputTokensDetails.ReasoningTokens != nil {
		reasoning = *w.OutputTokensDetails.ReasoningTokens
	}
	if reasoning > 0 {
		u.CompletionTokens = saturatingTokenSum(u.CompletionTokens, reasoning)
	}
	if w.TotalTokens != nil && *w.TotalTokens > 0 {
		accounted := saturatingTokenSum(u.PromptTokens, u.CompletionTokens, u.CacheCreationTokens)
		if gap := *w.TotalTokens - accounted; gap > 0 {
			u.PromptTokens = saturatingTokenSum(u.PromptTokens, gap)
		}
	}
	return u, true
}

func parseUsage(raw []byte, anthropicInputSemantics bool) (Usage, bool) {
	var w usageWire
	if err := json.Unmarshal(raw, &w); err != nil {
		return Usage{}, false
	}

	var u Usage
	found := false
	pick := func(candidates ...*int) int {
		for _, c := range candidates {
			if c != nil {
				found = true
				if *c < 0 {
					return 0
				}
				return *c
			}
		}
		return 0
	}

	u.PromptTokens = pick(w.PromptTokens, w.InputTokens)
	u.CompletionTokens = pick(w.CompletionTokens, w.OutputTokens)
	var chatCached, responsesCached, chatCreation, responsesCreation *int
	if w.PromptTokensDetails != nil {
		chatCached = w.PromptTokensDetails.CachedTokens
		chatCreation = w.PromptTokensDetails.CacheCreationTokens
		if chatCreation == nil {
			chatCreation = w.PromptTokensDetails.CacheWriteTokens
		}
	}
	if w.InputTokensDetails != nil {
		responsesCached = w.InputTokensDetails.CachedTokens
		responsesCreation = w.InputTokensDetails.CacheCreationTokens
		if responsesCreation == nil {
			responsesCreation = w.InputTokensDetails.CacheWriteTokens
		}
	}
	u.CachedTokens = pick(chatCached, responsesCached, w.CacheReadInputTokens)
	u.CacheCreationTokens = pick(chatCreation, responsesCreation, w.CacheCreationInputTokens)
	// OpenAI 的 cache_write/cache_creation 是 prompt/input_tokens 的子集；
	// canonical PromptTokens 不含缓存写，避免后续同时按普通输入与缓存写重复计费。
	if (chatCreation != nil || responsesCreation != nil) && u.CacheCreationTokens > 0 {
		u.PromptTokens -= u.CacheCreationTokens
		if u.PromptTokens < 0 {
			u.PromptTokens = 0
		}
	}
	if anthropicInputSemantics && w.PromptTokens == nil &&
		w.InputTokensDetails == nil && w.CacheReadInputTokens != nil {
		u.PromptTokens = saturatingTokenSum(u.PromptTokens, u.CachedTokens)
	}
	// Anthropic 双档缓存写入明细（存在时供计费分档；OpenAI 上游无此字段 → 0）。
	if w.CacheCreation != nil {
		u.CacheCreation5mTokens = pick(w.CacheCreation.Ephemeral5mInputTokens)
		u.CacheCreation1hTokens = pick(w.CacheCreation.Ephemeral1hInputTokens)
		if u.CacheCreationTokens == 0 {
			u.CacheCreationTokens = saturatingTokenSum(u.CacheCreation5mTokens, u.CacheCreation1hTokens)
		}
	}

	if !found {
		return Usage{}, false
	}
	return u, true
}

// ExtractUsage 从一段响应 JSON（非流式响应体或 SSE data 载荷）中提取顶层 usage 字段。
// 无 usage 字段、usage 为 null 或解析失败时返回 (Usage{}, false)。
func ExtractUsage(data []byte) (Usage, bool) {
	return extractUsage(data, ParseUsage)
}

// ExtractGeminiTranslatedOpenAIUsage 从 OpenAI Chat 响应中提取 CPA Gemini 翻译用量。
func ExtractGeminiTranslatedOpenAIUsage(data []byte) (Usage, bool) {
	return extractUsage(data, ParseGeminiTranslatedOpenAIUsage)
}

// ExtractAnthropicUsage 从响应顶层提取并按 Anthropic 语义归一化 usage。
func ExtractAnthropicUsage(data []byte) (Usage, bool) {
	var probe struct {
		Message *struct {
			Usage json.RawMessage `json:"usage"`
		} `json:"message"`
		Usage json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return Usage{}, false
	}
	// Anthropic 流式 message_start 把输入与缓存用量放在 message.usage；
	// message_delta 和非流式响应则使用顶层 usage。
	if probe.Message != nil && len(probe.Message.Usage) > 0 && string(probe.Message.Usage) != "null" {
		return ParseAnthropicUsage(probe.Message.Usage)
	}
	if len(probe.Usage) > 0 && string(probe.Usage) != "null" {
		return ParseAnthropicUsage(probe.Usage)
	}
	return Usage{}, false
}

// geminiUsageWire 是 Gemini usageMetadata 的最小计量字段集合。
// promptTokenCount 不含工具调用提示，thoughtsTokenCount 不含在正文输出中，
// 两者都需要分别补入 canonical 输入/输出口径。
type geminiUsageWire struct {
	PromptTokenCount        *int `json:"promptTokenCount"`
	CandidatesTokenCount    *int `json:"candidatesTokenCount"`
	ThoughtsTokenCount      *int `json:"thoughtsTokenCount"`
	CachedContentTokenCount *int `json:"cachedContentTokenCount"`
	ToolUsePromptTokenCount *int `json:"toolUsePromptTokenCount"`
	TotalTokenCount         *int `json:"totalTokenCount"`
}

// ParseGeminiUsage 解析 Gemini usageMetadata。
// promptTokenCount 已包含缓存读取，但不含 toolUsePromptTokenCount；
// candidatesTokenCount 与 thoughtsTokenCount 是互不重叠的输出分量。
func ParseGeminiUsage(raw []byte) (Usage, bool) {
	var w geminiUsageWire
	if err := json.Unmarshal(raw, &w); err != nil {
		return Usage{}, false
	}
	found := w.PromptTokenCount != nil || w.CandidatesTokenCount != nil ||
		w.ThoughtsTokenCount != nil || w.CachedContentTokenCount != nil ||
		w.ToolUsePromptTokenCount != nil || w.TotalTokenCount != nil
	if !found {
		return Usage{}, false
	}
	value := func(v *int) int {
		if v == nil || *v < 0 {
			return 0
		}
		return *v
	}
	prompt := value(w.PromptTokenCount)
	toolPrompt := value(w.ToolUsePromptTokenCount)
	input := saturatingTokenSum(prompt, toolPrompt)
	completion := saturatingTokenSum(value(w.CandidatesTokenCount), value(w.ThoughtsTokenCount))
	// totalTokenCount 可补足兼容上游遗漏的 candidates/thoughts 分量；取较大值
	// 而非相加，避免完整明细与累计总量重复计数。
	fromTotal := value(w.TotalTokenCount) - input
	if fromTotal > completion {
		completion = fromTotal
	}
	return Usage{
		PromptTokens:     input,
		CompletionTokens: completion,
		CachedTokens:     value(w.CachedContentTokenCount),
	}, true
}

// ParseOpenAITranslatedGeminiUsage 解析 CPA 将 OpenAI/Codex 系响应翻译成 Gemini 后的
// usageMetadata。翻译后的 candidatesTokenCount 已包含 reasoning，thoughtsTokenCount
// 只是其明细，不能像 Gemini 原生口径一样再次相加。
func ParseOpenAITranslatedGeminiUsage(raw []byte) (Usage, bool) {
	return parseTranslatedGeminiUsage(raw, false)
}

// ParseClaudeTranslatedGeminiUsage 解析 CPA 将 Claude 响应翻译成 Gemini 后的
// usageMetadata。翻译器直接把 Claude 新鲜输入写入 promptTokenCount，并把缓存读写
// 合并到 cachedContentTokenCount，因此 canonical PromptTokens 需要补回缓存总量。
func ParseClaudeTranslatedGeminiUsage(raw []byte) (Usage, bool) {
	return parseTranslatedGeminiUsage(raw, true)
}

func parseTranslatedGeminiUsage(raw []byte, independentCache bool) (Usage, bool) {
	var w geminiUsageWire
	if err := json.Unmarshal(raw, &w); err != nil {
		return Usage{}, false
	}
	found := w.PromptTokenCount != nil || w.CandidatesTokenCount != nil ||
		w.ThoughtsTokenCount != nil || w.CachedContentTokenCount != nil ||
		w.ToolUsePromptTokenCount != nil || w.TotalTokenCount != nil
	if !found {
		return Usage{}, false
	}
	value := func(v *int) int {
		if v == nil || *v < 0 {
			return 0
		}
		return *v
	}
	prompt := saturatingTokenSum(value(w.PromptTokenCount), value(w.ToolUsePromptTokenCount))
	completion := value(w.CandidatesTokenCount)
	if fromTotal := value(w.TotalTokenCount) - prompt; fromTotal > completion {
		completion = fromTotal
	}
	cached := value(w.CachedContentTokenCount)
	if independentCache {
		prompt = saturatingTokenSum(prompt, cached)
	}
	return Usage{PromptTokens: prompt, CompletionTokens: completion, CachedTokens: cached}, true
}

func saturatingTokenSum(values ...int) int {
	maxInt := int(^uint(0) >> 1)
	total := 0
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if value > maxInt-total {
			return maxInt
		}
		total += value
	}
	return total
}

// ExtractGeminiUsage 从 Gemini 原生响应或 Antigravity 包装响应中提取 usageMetadata。
func ExtractGeminiUsage(data []byte) (Usage, bool) {
	return extractGeminiUsage(data, ParseGeminiUsage)
}

// ExtractOpenAITranslatedGeminiUsage 提取 OpenAI/Codex 系翻译后的 Gemini 用量。
func ExtractOpenAITranslatedGeminiUsage(data []byte) (Usage, bool) {
	return extractGeminiUsage(data, ParseOpenAITranslatedGeminiUsage)
}

// ExtractClaudeTranslatedGeminiUsage 提取 Claude 翻译后的 Gemini 用量。
func ExtractClaudeTranslatedGeminiUsage(data []byte) (Usage, bool) {
	return extractGeminiUsage(data, ParseClaudeTranslatedGeminiUsage)
}

func extractGeminiUsage(data []byte, parse func([]byte) (Usage, bool)) (Usage, bool) {
	var probe struct {
		Response *struct {
			UsageMetadata json.RawMessage `json:"usageMetadata"`
		} `json:"response"`
		UsageMetadata json.RawMessage `json:"usageMetadata"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return Usage{}, false
	}
	if probe.Response != nil && len(probe.Response.UsageMetadata) > 0 && string(probe.Response.UsageMetadata) != "null" {
		return parse(probe.Response.UsageMetadata)
	}
	if len(probe.UsageMetadata) > 0 && string(probe.UsageMetadata) != "null" {
		return parse(probe.UsageMetadata)
	}
	return Usage{}, false
}

func extractUsage(data []byte, parse func([]byte) (Usage, bool)) (Usage, bool) {
	var probe struct {
		Usage json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return Usage{}, false
	}
	if len(probe.Usage) == 0 || string(probe.Usage) == "null" {
		return Usage{}, false
	}
	return parse(probe.Usage)
}

// ExtractResponsesUsage 从 Responses API 的 response.completed 事件载荷提取 usage。
// Responses 流式 usage 只在 completed 事件、且嵌套在 data.response.usage 下——
// 优先取 response.usage；无 response 包装时回退顶层 usage（容错，兼容个别上游
// 把 usage 放顶层）。usage 缺失/为 null/解析失败返回 (Usage{}, false)。
func ExtractResponsesUsage(data []byte) (Usage, bool) {
	return extractResponsesUsage(data, ParseUsage)
}

// ExtractGeminiTranslatedResponsesUsage 从 Responses 响应中提取 CPA Gemini 翻译用量。
func ExtractGeminiTranslatedResponsesUsage(data []byte) (Usage, bool) {
	return extractResponsesUsage(data, ParseGeminiTranslatedOpenAIUsage)
}

func extractResponsesUsage(data []byte, parse func([]byte) (Usage, bool)) (Usage, bool) {
	var probe struct {
		Response *struct {
			Usage json.RawMessage `json:"usage"`
		} `json:"response"`
		Usage json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return Usage{}, false
	}
	if probe.Response != nil && len(probe.Response.Usage) > 0 && string(probe.Response.Usage) != "null" {
		return parse(probe.Response.Usage)
	}
	if len(probe.Usage) > 0 && string(probe.Usage) != "null" {
		return parse(probe.Usage)
	}
	return Usage{}, false
}
