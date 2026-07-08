// Package dto 定义 relay 管线的对外协议数据结构（canonical：OpenAI chat completions）。
//
// 设计要点：请求体不做全量类型化解析——用 map[string]json.RawMessage 承载全部字段，
// 只显式解析调度所需的 model / stream / stream_options，其余字段原样透传，
// 避免 DTO 落后于上游字段演进。
package dto

import (
	"encoding/json"
	"errors"
)

// ErrInvalidBody 表示请求体不是合法的 JSON 对象。
var ErrInvalidBody = errors.New("请求体必须是 JSON 对象")

// ChatRequest OpenAI chat completions 请求的透明载体。
// fields 保存全部原始字段（含未知字段），Model/Stream 为显式解析出的调度字段。
type ChatRequest struct {
	fields map[string]json.RawMessage

	// Model 对外模型名（请求原始值）。
	Model string
	// Stream 是否流式请求。
	Stream bool
}

// ParseChatRequest 解析请求体：全字段进 map（未知字段透传），
// 显式解析 model / stream。非 JSON 对象报 ErrInvalidBody。
func ParseChatRequest(body []byte) (*ChatRequest, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, ErrInvalidBody
	}
	// JSON null 会解析成 nil map 且不报错，统一按非法请求体处理（避免后续 Set 写 nil map）。
	if fields == nil {
		return nil, ErrInvalidBody
	}
	req := &ChatRequest{fields: fields}
	if raw, ok := fields["model"]; ok {
		// model 非字符串时保持空串，由入口校验兜底报 400。
		_ = json.Unmarshal(raw, &req.Model)
	}
	if raw, ok := fields["stream"]; ok {
		_ = json.Unmarshal(raw, &req.Stream)
	}
	return req, nil
}

// Clone 浅拷贝字段表：RawMessage 值只读共享，改写只发生在 map 层。
// failover 换渠道时各 attempt 的改写（model 重写 / param_override）互不污染。
func (r *ChatRequest) Clone() *ChatRequest {
	fields := make(map[string]json.RawMessage, len(r.fields))
	for k, v := range r.fields {
		fields[k] = v
	}
	return &ChatRequest{fields: fields, Model: r.Model, Stream: r.Stream}
}

// Has 判断字段是否存在。
func (r *ChatRequest) Has(key string) bool {
	_, ok := r.fields[key]
	return ok
}

// Get 返回字段原始 JSON 值。
func (r *ChatRequest) Get(key string) (json.RawMessage, bool) {
	raw, ok := r.fields[key]
	return raw, ok
}

// IncludeUsageRequested 判断客户端是否显式请求 stream_options.include_usage=true。
// 未带 stream_options、字段非对象或 include_usage 非 true 均返回 false——
// 网关会强制向上游注入 include_usage 以保证计费，透传层据此决定
// 是否把 usage-only chunk 下发给客户端。
func (r *ChatRequest) IncludeUsageRequested() bool {
	raw, ok := r.fields["stream_options"]
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
	r.fields[key] = raw
	return nil
}

// Remove 删除字段。
func (r *ChatRequest) Remove(key string) {
	delete(r.fields, key)
}

// Marshal 序列化为 JSON（未知字段原样保留；键序为 Go map 序列化的字典序）。
func (r *ChatRequest) Marshal() ([]byte, error) {
	return json.Marshal(r.fields)
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
	// ReasoningTokens 推理 token 数（Responses output_tokens_details.reasoning_tokens）。
	// 不单独计费（已含于 CompletionTokens），仅落账用于展示/统计。
	ReasoningTokens int
}

// usageWire usage 字段的双协议命名兼容解析载体。
type usageWire struct {
	// OpenAI chat completions 风格
	PromptTokens        *int `json:"prompt_tokens"`
	CompletionTokens    *int `json:"completion_tokens"`
	PromptTokensDetails *struct {
		CachedTokens *int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`

	// OpenAI Responses 风格（input_tokens/output_tokens + details 子对象）。
	// 计数字段 input_tokens/output_tokens 与 Anthropic 同名，复用下方回退候选，
	// 仅 details 子对象命名不同（cached_tokens 在 input_tokens_details、
	// reasoning_tokens 在 output_tokens_details）。
	InputTokensDetails *struct {
		CachedTokens *int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
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

// ParseUsage 解析 usage 对象：OpenAI 命名优先，Anthropic 命名回退。
// raw 不是对象或不含任何已知计数字段时返回 (Usage{}, false)。
// 上游为不可信第三方：负数计数一律钳 0，防虚增计费/负成本入账。
func ParseUsage(raw []byte) (Usage, bool) {
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
	var chatCached, responsesCached *int
	if w.PromptTokensDetails != nil {
		chatCached = w.PromptTokensDetails.CachedTokens
	}
	if w.InputTokensDetails != nil {
		responsesCached = w.InputTokensDetails.CachedTokens
	}
	u.CachedTokens = pick(chatCached, responsesCached, w.CacheReadInputTokens)
	u.CacheCreationTokens = pick(w.CacheCreationInputTokens)
	// Anthropic 双档缓存写入明细（存在时供计费分档；OpenAI 上游无此字段 → 0）。
	if w.CacheCreation != nil {
		u.CacheCreation5mTokens = pick(w.CacheCreation.Ephemeral5mInputTokens)
		u.CacheCreation1hTokens = pick(w.CacheCreation.Ephemeral1hInputTokens)
	}

	// reasoning 已含于 output/completion，不叠加计费也不影响 found 判定；负值钳 0。
	if w.OutputTokensDetails != nil && w.OutputTokensDetails.ReasoningTokens != nil {
		if r := *w.OutputTokensDetails.ReasoningTokens; r > 0 {
			u.ReasoningTokens = r
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
	var probe struct {
		Usage json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return Usage{}, false
	}
	if len(probe.Usage) == 0 || string(probe.Usage) == "null" {
		return Usage{}, false
	}
	return ParseUsage(probe.Usage)
}

// ExtractResponsesUsage 从 Responses API 的 response.completed 事件载荷提取 usage。
// Responses 流式 usage 只在 completed 事件、且嵌套在 data.response.usage 下——
// 优先取 response.usage；无 response 包装时回退顶层 usage（容错，兼容个别上游
// 把 usage 放顶层）。usage 缺失/为 null/解析失败返回 (Usage{}, false)。
func ExtractResponsesUsage(data []byte) (Usage, bool) {
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
		return ParseUsage(probe.Response.Usage)
	}
	if len(probe.Usage) > 0 && string(probe.Usage) != "null" {
		return ParseUsage(probe.Usage)
	}
	return Usage{}, false
}
