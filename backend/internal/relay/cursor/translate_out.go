package cursor

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SSEFrame 是一个 SSE 事件帧。OpenAI 只用 Data（事件名为空），Anthropic 带
// event 名。由调用方负责拼装 "event: ..\ndata: ..\n\n" 与结尾哨兵。
type SSEFrame struct {
	Event string
	Data  []byte
}

func mustJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return data
}

// mapFinishReasonAnthropic 把中立 finish reason 映射为 Anthropic stop_reason。
func mapFinishReasonAnthropic(reason string) string {
	switch reason {
	case "tool_calls":
		return "tool_use"
	case "length":
		return "max_tokens"
	default:
		return "end_turn"
	}
}

// ---- 聚合（非流式） ----

// AggToolCall 是聚合后的一次工具调用。
type AggToolCall struct {
	ID   string
	Name string
	Args string
}

// Aggregate 是一轮生成的聚合结果。
type Aggregate struct {
	Text         string
	Reasoning    string
	ToolCalls    []AggToolCall
	FinishReason string
	OutputTokens int
	Err          error
}

// CollectEvents 消费事件流直至关闭，聚合为最终结果。
func CollectEvents(events <-chan Event) *Aggregate {
	agg := &Aggregate{FinishReason: "stop"}
	var text, reasoning strings.Builder
	argsBuf := make(map[string]*strings.Builder)
	order := []string{}
	names := make(map[string]string)
	ended := make(map[string]string) // id → 完整 args JSON（exec 截获时给出）

	for e := range events {
		switch ev := e.(type) {
		case TextDelta:
			text.WriteString(ev.Text)
		case ReasoningDelta:
			reasoning.WriteString(ev.Text)
		case ToolCallStart:
			if _, ok := argsBuf[ev.ID]; !ok {
				argsBuf[ev.ID] = &strings.Builder{}
				order = append(order, ev.ID)
			}
			if names[ev.ID] == "" {
				names[ev.ID] = ev.Name
			}
		case ToolCallArgsDelta:
			if b, ok := argsBuf[ev.ID]; ok {
				b.WriteString(ev.Args)
			}
			if names[ev.ID] == "" {
				names[ev.ID] = ev.Name
			}
		case ToolCallEnd:
			if _, ok := argsBuf[ev.ID]; !ok {
				argsBuf[ev.ID] = &strings.Builder{}
				order = append(order, ev.ID)
			}
			// exec McpArgs 带来的名字最权威：Start 可能因 interaction update
			// 先到而没有名字，这里统一以 End 的非空名为准。
			if ev.Name != "" {
				names[ev.ID] = ev.Name
			}
			if len(ev.ArgsJSON) > 0 {
				ended[ev.ID] = string(ev.ArgsJSON)
			}
		case UsageDelta:
			agg.OutputTokens += ev.Tokens
		case Done:
			agg.FinishReason = ev.FinishReason
		case ErrEvent:
			agg.Err = ev.Err
		}
	}
	agg.Text = text.String()
	agg.Reasoning = reasoning.String()
	for _, id := range order {
		args := ended[id]
		if args == "" {
			args = argsBuf[id].String()
		}
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		agg.ToolCalls = append(agg.ToolCalls, AggToolCall{ID: id, Name: names[id], Args: args})
	}
	if len(agg.ToolCalls) > 0 && agg.FinishReason == "stop" {
		agg.FinishReason = "tool_calls"
	}
	return agg
}

// ---- OpenAI ----

// BuildOpenAIResponse 把聚合结果渲染成 chat.completion JSON。
func BuildOpenAIResponse(agg *Aggregate, id, model string, created int64, promptTokens int) []byte {
	message := map[string]any{"role": "assistant"}
	if agg.Text != "" || len(agg.ToolCalls) == 0 {
		message["content"] = agg.Text
	} else {
		message["content"] = nil
	}
	if agg.Reasoning != "" {
		message["reasoning_content"] = agg.Reasoning
	}
	if len(agg.ToolCalls) > 0 {
		var calls []any
		for _, tc := range agg.ToolCalls {
			calls = append(calls, map[string]any{
				"id":   tc.ID,
				"type": "function",
				"function": map[string]any{
					"name":      tc.Name,
					"arguments": tc.Args,
				},
			})
		}
		message["tool_calls"] = calls
	}
	return mustJSON(map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": created,
		"model":   model,
		"choices": []any{map[string]any{
			"index":         0,
			"message":       message,
			"finish_reason": agg.FinishReason,
		}},
		"usage": map[string]any{
			"prompt_tokens":     promptTokens,
			"completion_tokens": agg.OutputTokens,
			"total_tokens":      promptTokens + agg.OutputTokens,
		},
	})
}

// OpenAIStreamRenderer 把中立事件渲染成 chat.completion.chunk 序列。
type OpenAIStreamRenderer struct {
	ID           string
	Model        string
	Created      int64
	PromptTokens int

	sentRole    bool
	toolIndex   map[string]int
	argsSent    map[string]int
	namedSent   map[string]bool // 该调用的非空 name 是否已下发
	usageTokens int
}

func NewOpenAIStreamRenderer(id, model string, created int64, promptTokens int) *OpenAIStreamRenderer {
	return &OpenAIStreamRenderer{
		ID: id, Model: model, Created: created, PromptTokens: promptTokens,
		toolIndex: make(map[string]int),
		argsSent:  make(map[string]int),
		namedSent: make(map[string]bool),
	}
}

func (r *OpenAIStreamRenderer) chunk(delta map[string]any, finish any, usage map[string]any) SSEFrame {
	body := map[string]any{
		"id":      r.ID,
		"object":  "chat.completion.chunk",
		"created": r.Created,
		"model":   r.Model,
		"choices": []any{map[string]any{
			"index":         0,
			"delta":         delta,
			"finish_reason": finish,
		}},
	}
	if usage != nil {
		body["usage"] = usage
	}
	return SSEFrame{Data: mustJSON(body)}
}

func (r *OpenAIStreamRenderer) roleChunkIfNeeded(frames []SSEFrame) []SSEFrame {
	if !r.sentRole {
		r.sentRole = true
		frames = append(frames, r.chunk(map[string]any{"role": "assistant", "content": ""}, nil, nil))
	}
	return frames
}

// Render 把一个事件渲染为零或多个 SSE 帧。收到 Done 后调用方应补发 [DONE]。
func (r *OpenAIStreamRenderer) Render(e Event) []SSEFrame {
	var frames []SSEFrame
	switch ev := e.(type) {
	case TextDelta:
		frames = r.roleChunkIfNeeded(frames)
		frames = append(frames, r.chunk(map[string]any{"content": ev.Text}, nil, nil))
	case ReasoningDelta:
		frames = r.roleChunkIfNeeded(frames)
		frames = append(frames, r.chunk(map[string]any{"reasoning_content": ev.Text}, nil, nil))
	case ToolCallStart:
		frames = r.roleChunkIfNeeded(frames)
		if _, ok := r.toolIndex[ev.ID]; !ok {
			idx := len(r.toolIndex)
			r.toolIndex[ev.ID] = idx
			r.namedSent[ev.ID] = ev.Name != ""
			frames = append(frames, r.chunk(map[string]any{"tool_calls": []any{map[string]any{
				"index": idx,
				"id":    ev.ID,
				"type":  "function",
				"function": map[string]any{
					"name":      ev.Name,
					"arguments": "",
				},
			}}}, nil, nil))
		}
	case ToolCallArgsDelta:
		if idx, ok := r.toolIndex[ev.ID]; ok && ev.Args != "" {
			r.argsSent[ev.ID] += len(ev.Args)
			fn := map[string]any{"arguments": ev.Args}
			// 名字晚到（Start 时为空）：借参数增量 chunk 补发。
			if !r.namedSent[ev.ID] && ev.Name != "" {
				fn["name"] = ev.Name
				r.namedSent[ev.ID] = true
			}
			frames = append(frames, r.chunk(map[string]any{"tool_calls": []any{map[string]any{
				"index":    idx,
				"function": fn,
			}}}, nil, nil))
		}
	case ToolCallEnd:
		if idx, ok := r.toolIndex[ev.ID]; ok {
			fn := map[string]any{}
			// 若从未流过参数（exec 直达），一次性补发完整 arguments。
			if r.argsSent[ev.ID] == 0 && len(ev.ArgsJSON) > 0 {
				r.argsSent[ev.ID] = len(ev.ArgsJSON)
				fn["arguments"] = string(ev.ArgsJSON)
			}
			if !r.namedSent[ev.ID] && ev.Name != "" {
				fn["name"] = ev.Name
				r.namedSent[ev.ID] = true
			}
			if len(fn) > 0 {
				frames = append(frames, r.chunk(map[string]any{"tool_calls": []any{map[string]any{
					"index":    idx,
					"function": fn,
				}}}, nil, nil))
			}
		}
	case UsageDelta:
		r.usageTokens += ev.Tokens
	case Done:
		frames = r.roleChunkIfNeeded(frames)
		frames = append(frames, r.chunk(map[string]any{}, ev.FinishReason, map[string]any{
			"prompt_tokens":     r.PromptTokens,
			"completion_tokens": r.usageTokens,
			"total_tokens":      r.PromptTokens + r.usageTokens,
		}))
	}
	return frames
}

// ---- Anthropic ----

// BuildAnthropicResponse 把聚合结果渲染成 /v1/messages 非流式 JSON。
func BuildAnthropicResponse(agg *Aggregate, id, model string, promptTokens int) []byte {
	var content []any
	if agg.Reasoning != "" {
		content = append(content, map[string]any{"type": "thinking", "thinking": agg.Reasoning})
	}
	if agg.Text != "" {
		content = append(content, map[string]any{"type": "text", "text": agg.Text})
	}
	for _, tc := range agg.ToolCalls {
		var input any = map[string]any{}
		_ = json.Unmarshal([]byte(tc.Args), &input)
		content = append(content, map[string]any{
			"type":  "tool_use",
			"id":    tc.ID,
			"name":  tc.Name,
			"input": input,
		})
	}
	if content == nil {
		content = []any{}
	}
	return mustJSON(map[string]any{
		"id":            id,
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"content":       content,
		"stop_reason":   mapFinishReasonAnthropic(agg.FinishReason),
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  promptTokens,
			"output_tokens": agg.OutputTokens,
		},
	})
}

// AnthropicStreamRenderer 把中立事件渲染成 Anthropic SSE 事件序列。
type AnthropicStreamRenderer struct {
	ID           string
	Model        string
	PromptTokens int

	started     bool
	blockOpen   bool
	blockType   string // "text" | "thinking" | "tool_use"
	blockIndex  int
	curToolID   string
	argsSent    map[string]int
	usageTokens int
	// pendingTool / pendingArgs：工具名可能晚于调用事件到达（interaction
	// update 先到且无名），content_block_start 一旦发出 name 无法更正，
	// 故名字为空时延迟开块，参数增量先缓存，拿到名字后一并冲刷。
	pendingTool map[string]bool
	pendingArgs map[string]string
}

func NewAnthropicStreamRenderer(id, model string, promptTokens int) *AnthropicStreamRenderer {
	return &AnthropicStreamRenderer{
		ID: id, Model: model, PromptTokens: promptTokens, blockIndex: -1,
		argsSent:    make(map[string]int),
		pendingTool: make(map[string]bool),
		pendingArgs: make(map[string]string),
	}
}

// openToolBlock 为一次工具调用开块并冲刷缓存的参数增量。
func (r *AnthropicStreamRenderer) openToolBlock(id, name string) []SSEFrame {
	frames := r.openBlock("tool_use", map[string]any{
		"type":  "tool_use",
		"id":    id,
		"name":  name,
		"input": map[string]any{},
	})
	r.curToolID = id
	delete(r.pendingTool, id)
	if buf := r.pendingArgs[id]; buf != "" {
		r.argsSent[id] += len(buf)
		frames = append(frames, r.delta(map[string]any{"type": "input_json_delta", "partial_json": buf}))
		delete(r.pendingArgs, id)
	}
	return frames
}

func (r *AnthropicStreamRenderer) startFrames() []SSEFrame {
	r.started = true
	return []SSEFrame{{
		Event: "message_start",
		Data: mustJSON(map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":            r.ID,
				"type":          "message",
				"role":          "assistant",
				"model":         r.Model,
				"content":       []any{},
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage":         map[string]any{"input_tokens": r.PromptTokens, "output_tokens": 0},
			},
		}),
	}}
}

func (r *AnthropicStreamRenderer) closeBlock() []SSEFrame {
	if !r.blockOpen {
		return nil
	}
	r.blockOpen = false
	return []SSEFrame{{
		Event: "content_block_stop",
		Data:  mustJSON(map[string]any{"type": "content_block_stop", "index": r.blockIndex}),
	}}
}

func (r *AnthropicStreamRenderer) openBlock(blockType string, block map[string]any) []SSEFrame {
	frames := r.closeBlock()
	r.blockIndex++
	r.blockOpen = true
	r.blockType = blockType
	frames = append(frames, SSEFrame{
		Event: "content_block_start",
		Data: mustJSON(map[string]any{
			"type":          "content_block_start",
			"index":         r.blockIndex,
			"content_block": block,
		}),
	})
	return frames
}

func (r *AnthropicStreamRenderer) delta(delta map[string]any) SSEFrame {
	return SSEFrame{
		Event: "content_block_delta",
		Data: mustJSON(map[string]any{
			"type":  "content_block_delta",
			"index": r.blockIndex,
			"delta": delta,
		}),
	}
}

// Render 把一个事件渲染为零或多个 SSE 帧。
func (r *AnthropicStreamRenderer) Render(e Event) []SSEFrame {
	var frames []SSEFrame
	if !r.started {
		frames = append(frames, r.startFrames()...)
	}
	switch ev := e.(type) {
	case TextDelta:
		if !r.blockOpen || r.blockType != "text" {
			frames = append(frames, r.openBlock("text", map[string]any{"type": "text", "text": ""})...)
		}
		frames = append(frames, r.delta(map[string]any{"type": "text_delta", "text": ev.Text}))
	case ReasoningDelta:
		if !r.blockOpen || r.blockType != "thinking" {
			frames = append(frames, r.openBlock("thinking", map[string]any{"type": "thinking", "thinking": ""})...)
		}
		frames = append(frames, r.delta(map[string]any{"type": "thinking_delta", "thinking": ev.Text}))
	case ToolCallStart:
		if ev.Name == "" {
			// 名字未知：延迟开块，等 ArgsDelta/End 带来名字再冲刷。
			r.pendingTool[ev.ID] = true
			break
		}
		frames = append(frames, r.openToolBlock(ev.ID, ev.Name)...)
	case ToolCallArgsDelta:
		if r.pendingTool[ev.ID] {
			if ev.Name == "" {
				r.pendingArgs[ev.ID] += ev.Args
				break
			}
			frames = append(frames, r.openToolBlock(ev.ID, ev.Name)...)
		}
		if r.blockOpen && r.blockType == "tool_use" && r.curToolID == ev.ID && ev.Args != "" {
			r.argsSent[ev.ID] += len(ev.Args)
			frames = append(frames, r.delta(map[string]any{"type": "input_json_delta", "partial_json": ev.Args}))
		}
	case ToolCallEnd:
		if r.pendingTool[ev.ID] {
			if len(ev.ArgsJSON) > 0 {
				// 已有完整参数：丢弃可能不完整的缓存增量，直接发全量。
				delete(r.pendingArgs, ev.ID)
			}
			frames = append(frames, r.openToolBlock(ev.ID, ev.Name)...)
		}
		if r.blockOpen && r.blockType == "tool_use" && r.curToolID == ev.ID {
			if r.argsSent[ev.ID] == 0 && len(ev.ArgsJSON) > 0 {
				r.argsSent[ev.ID] = len(ev.ArgsJSON)
				frames = append(frames, r.delta(map[string]any{"type": "input_json_delta", "partial_json": string(ev.ArgsJSON)}))
			}
			frames = append(frames, r.closeBlock()...)
		}
	case UsageDelta:
		r.usageTokens += ev.Tokens
	case Done:
		frames = append(frames, r.closeBlock()...)
		frames = append(frames,
			SSEFrame{
				Event: "message_delta",
				Data: mustJSON(map[string]any{
					"type":  "message_delta",
					"delta": map[string]any{"stop_reason": mapFinishReasonAnthropic(ev.FinishReason), "stop_sequence": nil},
					"usage": map[string]any{"output_tokens": r.usageTokens},
				}),
			},
			SSEFrame{Event: "message_stop", Data: mustJSON(map[string]any{"type": "message_stop"})},
		)
	}
	return frames
}

// EncodeSSE 把帧编码为 SSE 文本（带可选 event 行）。
func EncodeSSE(f SSEFrame) []byte {
	if f.Event == "" {
		return []byte(fmt.Sprintf("data: %s\n\n", f.Data))
	}
	return []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", f.Event, f.Data))
}

// EstimatePromptTokens 粗估 prompt token 数（Cursor 不回报输入侧用量）。
func EstimatePromptTokens(req *ParsedRequest) int {
	total := 0
	for _, sp := range req.SystemPrompts {
		total += len(sp)
	}
	for _, m := range req.Messages {
		total += len(partsText(m.Content))
		for _, tc := range m.ToolCalls {
			total += len(tc.Name) + len(tc.Args)
		}
	}
	if total == 0 {
		return 0
	}
	tokens := total / 4
	if tokens < 1 {
		tokens = 1
	}
	return tokens
}
