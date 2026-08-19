package cursor

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Protocol 标识入口协议。
type Protocol int

const (
	// ProtoOpenAI 对应 /v1/chat/completions。
	ProtoOpenAI Protocol = iota
	// ProtoAnthropic 对应 /v1/messages。
	ProtoAnthropic
)

// ContentPart 是一段文本或图片内容。
type ContentPart struct {
	Type      string // "text" | "image"
	Text      string
	ImageMime string
	ImageData string // base64（不含 data: 前缀）
}

// NToolCall 是助手发起的一次工具调用。
type NToolCall struct {
	ID   string
	Name string
	Args json.RawMessage // 参数对象 JSON
}

// NMessage 是与入口协议无关的中立消息。
type NMessage struct {
	Role       string // "user" | "assistant" | "tool"
	Content    []ContentPart
	ToolCalls  []NToolCall // 仅 assistant
	ToolCallID string      // 仅 tool（工具结果）
	ToolName   string
	IsError    bool
}

// NToolDef 是一个可供模型调用的工具定义。
type NToolDef struct {
	Name        string
	Description string
	Schema      json.RawMessage // JSON Schema 对象
}

// ParsedRequest 是入口请求解析后的中立形态。
type ParsedRequest struct {
	Protocol      Protocol
	Model         string
	Stream        bool
	SystemPrompts []string
	Messages      []NMessage
	Tools         []NToolDef
	// ReasoningEffort 是归一化后的思考档位（minimal/low/medium/high/xhigh），
	// 来自 OpenAI reasoning_effort 或 Anthropic thinking.budget_tokens；空表示未指定。
	ReasoningEffort string
}

// normalizeEffort 归一化思考档位取值，未知值视为未指定。
func normalizeEffort(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "minimal":
		return "minimal"
	case "low":
		return "low"
	case "medium":
		return "medium"
	case "high":
		return "high"
	case "xhigh":
		return "xhigh"
	}
	return ""
}

// effortFromBudget 把 Anthropic thinking.budget_tokens 映射为思考档位。
func effortFromBudget(n int) string {
	switch {
	case n <= 0:
		return ""
	case n < 4096:
		return "low"
	case n < 16384:
		return "medium"
	case n < 32768:
		return "high"
	default:
		return "xhigh"
	}
}

// ParseRequest 按入口协议解析请求体。
func ParseRequest(protocol Protocol, payload []byte) (*ParsedRequest, error) {
	switch protocol {
	case ProtoAnthropic:
		return parseAnthropic(payload)
	default:
		return parseOpenAI(payload)
	}
}

// ---- OpenAI /v1/chat/completions ----

type oaiRequest struct {
	Model           string    `json:"model"`
	Stream          bool      `json:"stream"`
	Messages        []oaiMsg  `json:"messages"`
	Tools           []oaiTool `json:"tools"`
	ReasoningEffort string    `json:"reasoning_effort"`
	// Reasoning 兼容 Responses 风格的 {"reasoning":{"effort":"high"}} 写法。
	Reasoning *struct {
		Effort string `json:"effort"`
	} `json:"reasoning"`
}

type oaiMsg struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	Name       string          `json:"name"`
	ToolCalls  []oaiToolCall   `json:"tool_calls"`
	ToolCallID string          `json:"tool_call_id"`
}

type oaiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaiTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

func parseOpenAI(payload []byte) (*ParsedRequest, error) {
	var req oaiRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, fmt.Errorf("解析 OpenAI 请求失败: %w", err)
	}
	out := &ParsedRequest{Protocol: ProtoOpenAI, Model: req.Model, Stream: req.Stream}
	out.ReasoningEffort = normalizeEffort(req.ReasoningEffort)
	if out.ReasoningEffort == "" && req.Reasoning != nil {
		out.ReasoningEffort = normalizeEffort(req.Reasoning.Effort)
	}
	for _, tool := range req.Tools {
		if !strings.EqualFold(tool.Type, "function") && tool.Type != "" {
			continue
		}
		out.Tools = append(out.Tools, NToolDef{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			Schema:      tool.Function.Parameters,
		})
	}
	for _, msg := range req.Messages {
		switch msg.Role {
		case "system", "developer":
			if text := oaiContentText(msg.Content); text != "" {
				out.SystemPrompts = append(out.SystemPrompts, text)
			}
		case "user":
			parts, err := oaiContentParts(msg.Content)
			if err != nil {
				return nil, err
			}
			out.Messages = append(out.Messages, NMessage{Role: "user", Content: parts})
		case "assistant":
			parts, err := oaiContentParts(msg.Content)
			if err != nil {
				return nil, err
			}
			nm := NMessage{Role: "assistant", Content: parts}
			for _, tc := range msg.ToolCalls {
				args := json.RawMessage(tc.Function.Arguments)
				if len(strings.TrimSpace(tc.Function.Arguments)) == 0 {
					args = json.RawMessage("{}")
				}
				nm.ToolCalls = append(nm.ToolCalls, NToolCall{ID: tc.ID, Name: tc.Function.Name, Args: args})
			}
			out.Messages = append(out.Messages, nm)
		case "tool":
			out.Messages = append(out.Messages, NMessage{
				Role:       "tool",
				ToolCallID: msg.ToolCallID,
				ToolName:   msg.Name,
				Content:    mustParts(oaiContentParts(msg.Content)),
			})
		}
	}
	return out, nil
}

// oaiContentText 从 string 或 parts 数组中提取纯文本。
func oaiContentText(raw json.RawMessage) string {
	parts, err := oaiContentParts(raw)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Type == "text" && p.Text != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

func oaiContentParts(raw json.RawMessage) ([]ContentPart, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	// content 可能是字符串
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("解析 content 字符串失败: %w", err)
		}
		if strings.TrimSpace(s) == "" {
			return nil, nil
		}
		return []ContentPart{{Type: "text", Text: s}}, nil
	}
	// content 是数组
	var items []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL struct {
			URL string `json:"url"`
		} `json:"image_url"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("解析 content 数组失败: %w", err)
	}
	var parts []ContentPart
	for _, it := range items {
		switch it.Type {
		case "text", "input_text":
			if it.Text != "" {
				parts = append(parts, ContentPart{Type: "text", Text: it.Text})
			}
		case "image_url", "input_image":
			mime, data := splitDataURL(it.ImageURL.URL)
			if data != "" {
				parts = append(parts, ContentPart{Type: "image", ImageMime: mime, ImageData: data})
			}
		}
	}
	return parts, nil
}

func mustParts(parts []ContentPart, _ error) []ContentPart { return parts }

// ---- Anthropic /v1/messages ----

type antRequest struct {
	Model    string          `json:"model"`
	Stream   bool            `json:"stream"`
	System   json.RawMessage `json:"system"`
	Messages []antMsg        `json:"messages"`
	Tools    []antTool       `json:"tools"`
	Thinking *struct {
		Type         string `json:"type"`
		BudgetTokens int    `json:"budget_tokens"`
	} `json:"thinking"`
	// OutputConfig 兼容 Anthropic effort beta（Claude Code 以此传思考档位，
	// 此时 thinking.type 常为 "adaptive" 而无 budget）。
	OutputConfig *struct {
		Effort string `json:"effort"`
	} `json:"output_config"`
}

type antMsg struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type antTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

func parseAnthropic(payload []byte) (*ParsedRequest, error) {
	var req antRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, fmt.Errorf("解析 Anthropic 请求失败: %w", err)
	}
	out := &ParsedRequest{Protocol: ProtoAnthropic, Model: req.Model, Stream: req.Stream}
	if req.OutputConfig != nil {
		out.ReasoningEffort = normalizeEffort(req.OutputConfig.Effort)
	}
	if out.ReasoningEffort == "" && req.Thinking != nil && strings.EqualFold(req.Thinking.Type, "enabled") {
		out.ReasoningEffort = effortFromBudget(req.Thinking.BudgetTokens)
	}
	if sys := antSystemText(req.System); sys != "" {
		out.SystemPrompts = append(out.SystemPrompts, sys)
	}
	for _, tool := range req.Tools {
		out.Tools = append(out.Tools, NToolDef{
			Name:        tool.Name,
			Description: tool.Description,
			Schema:      tool.InputSchema,
		})
	}
	for _, msg := range req.Messages {
		blocks, err := antBlocks(msg.Content)
		if err != nil {
			return nil, err
		}
		switch msg.Role {
		case "user":
			out.Messages = append(out.Messages, expandAnthropicUserBlocks(blocks)...)
		case "assistant":
			nm := NMessage{Role: "assistant"}
			for _, b := range blocks {
				switch b.Type {
				case "text":
					if b.Text != "" {
						nm.Content = append(nm.Content, ContentPart{Type: "text", Text: b.Text})
					}
				case "tool_use":
					args := b.Input
					if len(strings.TrimSpace(string(args))) == 0 {
						args = json.RawMessage("{}")
					}
					nm.ToolCalls = append(nm.ToolCalls, NToolCall{ID: b.ID, Name: b.Name, Args: args})
				}
			}
			out.Messages = append(out.Messages, nm)
		case "system":
			// mid-conversation system（Claude Code beta）：并入系统提示链，
			// 不作为对话消息参与 turns 构建。
			for _, b := range blocks {
				if b.Type == "text" && b.Text != "" {
					out.SystemPrompts = append(out.SystemPrompts, b.Text)
				}
			}
		}
	}
	return out, nil
}

type antBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
	Source    *antImageSource `json:"source"`
}

type antImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

func antBlocks(raw json.RawMessage) ([]antBlock, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("解析 Anthropic content 字符串失败: %w", err)
		}
		return []antBlock{{Type: "text", Text: s}}, nil
	}
	var blocks []antBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, fmt.Errorf("解析 Anthropic content 数组失败: %w", err)
	}
	return blocks, nil
}

// expandAnthropicUserBlocks 把一个 user turn 的 blocks 展开成中立消息：
// tool_result 块各成一条 tool 消息（保持在正文之前，对齐上一 assistant 的工具调用），
// text/image 块合并成一条 user 消息。
func expandAnthropicUserBlocks(blocks []antBlock) []NMessage {
	var msgs []NMessage
	var userParts []ContentPart
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text != "" {
				userParts = append(userParts, ContentPart{Type: "text", Text: b.Text})
			}
		case "image":
			if b.Source != nil && b.Source.Data != "" {
				userParts = append(userParts, ContentPart{Type: "image", ImageMime: b.Source.MediaType, ImageData: b.Source.Data})
			}
		case "tool_result":
			msgs = append(msgs, NMessage{
				Role:       "tool",
				ToolCallID: b.ToolUseID,
				IsError:    b.IsError,
				Content:    antToolResultParts(b.Content),
			})
		}
	}
	if len(userParts) > 0 {
		msgs = append(msgs, NMessage{Role: "user", Content: userParts})
	}
	return msgs
}

func antToolResultParts(raw json.RawMessage) []ContentPart {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	if trimmed[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil && s != "" {
			return []ContentPart{{Type: "text", Text: s}}
		}
		return nil
	}
	var items []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	var parts []ContentPart
	for _, it := range items {
		if it.Type == "text" && it.Text != "" {
			parts = append(parts, ContentPart{Type: "text", Text: it.Text})
		}
	}
	return parts
}

func antSystemText(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	if trimmed[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return strings.TrimSpace(s)
		}
		return ""
	}
	var items []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &items) != nil {
		return ""
	}
	var b strings.Builder
	for _, it := range items {
		if it.Type == "text" && it.Text != "" {
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(it.Text)
		}
	}
	return b.String()
}

// splitDataURL 解析 data:<mime>;base64,<data>，返回 mime 与纯 base64 数据。
// 非 data URL 返回空数据（远程图片 URL 无法内联，忽略）。
func splitDataURL(u string) (mime, data string) {
	if !strings.HasPrefix(u, "data:") {
		return "", ""
	}
	rest := strings.TrimPrefix(u, "data:")
	comma := strings.IndexByte(rest, ',')
	if comma < 0 {
		return "", ""
	}
	meta := rest[:comma]
	data = rest[comma+1:]
	mime = meta
	if i := strings.IndexByte(meta, ';'); i >= 0 {
		mime = meta[:i]
	}
	return mime, data
}
