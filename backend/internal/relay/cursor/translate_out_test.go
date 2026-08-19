package cursor

import (
	"encoding/json"
	"strings"
	"testing"
)

func feedEvents(events ...Event) <-chan Event {
	ch := make(chan Event, len(events))
	for _, e := range events {
		ch <- e
	}
	close(ch)
	return ch
}

func TestCollectEventsToolCalls(t *testing.T) {
	agg := CollectEvents(feedEvents(
		TextDelta{Text: "let me check"},
		ToolCallStart{ID: "c1", Name: "get_weather"},
		ToolCallArgsDelta{ID: "c1", Args: `{"city":`},
		ToolCallArgsDelta{ID: "c1", Args: `"NY"}`},
		ToolCallEnd{ID: "c1", Name: "get_weather", ArgsJSON: []byte(`{"city":"NY"}`)},
		UsageDelta{Tokens: 7},
		Done{FinishReason: "tool_calls"},
	))
	if agg.Text != "let me check" {
		t.Errorf("text = %q", agg.Text)
	}
	if len(agg.ToolCalls) != 1 || agg.ToolCalls[0].Name != "get_weather" {
		t.Fatalf("toolCalls = %+v", agg.ToolCalls)
	}
	// exec 截获给出的完整 ArgsJSON 优先于流式聚合
	if agg.ToolCalls[0].Args != `{"city":"NY"}` {
		t.Errorf("args = %q", agg.ToolCalls[0].Args)
	}
	if agg.FinishReason != "tool_calls" || agg.OutputTokens != 7 {
		t.Errorf("finish = %q tokens = %d", agg.FinishReason, agg.OutputTokens)
	}
}

func TestCollectEvents晚到工具名回填(t *testing.T) {
	// interaction update 先到时 Start 无名，exec McpArgs 的 End 才带名。
	agg := CollectEvents(feedEvents(
		ToolCallStart{ID: "c1", Name: ""},
		ToolCallEnd{ID: "c1", Name: "get_weather", ArgsJSON: []byte(`{"city":"深圳"}`)},
		Done{FinishReason: "tool_calls"},
	))
	if len(agg.ToolCalls) != 1 || agg.ToolCalls[0].Name != "get_weather" {
		t.Fatalf("toolCalls = %+v", agg.ToolCalls)
	}
}

func TestOpenAIStream工具名晚到补发(t *testing.T) {
	r := NewOpenAIStreamRenderer("chatcmpl-1", "m", 123, 10)
	var frames []SSEFrame
	for _, e := range []Event{
		ToolCallStart{ID: "c1", Name: ""},
		ToolCallEnd{ID: "c1", Name: "f", ArgsJSON: []byte(`{"a":1}`)},
		Done{FinishReason: "tool_calls"},
	} {
		frames = append(frames, r.Render(e)...)
	}
	// End 帧应同时补发 name 与完整 arguments。
	var sawName bool
	for _, f := range frames {
		var m map[string]any
		_ = json.Unmarshal(f.Data, &m)
		delta := m["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)
		tcs, _ := delta["tool_calls"].([]any)
		for _, tc := range tcs {
			fn, _ := tc.(map[string]any)["function"].(map[string]any)
			if fn["name"] == "f" && fn["arguments"] == `{"a":1}` {
				sawName = true
			}
		}
	}
	if !sawName {
		t.Fatalf("未见补发的 name+arguments: %s", framesDump(frames))
	}
}

func TestAnthropicStream工具名晚到延迟开块(t *testing.T) {
	r := NewAnthropicStreamRenderer("msg-1", "m", 10)
	var frames []SSEFrame
	for _, e := range []Event{
		ToolCallStart{ID: "c1", Name: ""},
		ToolCallArgsDelta{ID: "c1", Name: "", Args: `{"city":`},
		ToolCallEnd{ID: "c1", Name: "get_weather", ArgsJSON: []byte(`{"city":"深圳"}`)},
		Done{FinishReason: "tool_calls"},
	} {
		frames = append(frames, r.Render(e)...)
	}
	// content_block_start 必须携带最终名字；缓存的参数增量在开块后冲刷。
	var blockName string
	var partials []string
	for _, f := range frames {
		var m map[string]any
		_ = json.Unmarshal(f.Data, &m)
		switch f.Event {
		case "content_block_start":
			cb := m["content_block"].(map[string]any)
			if cb["type"] == "tool_use" {
				blockName, _ = cb["name"].(string)
			}
		case "content_block_delta":
			d := m["delta"].(map[string]any)
			if d["type"] == "input_json_delta" {
				partials = append(partials, d["partial_json"].(string))
			}
		}
	}
	if blockName != "get_weather" {
		t.Fatalf("tool_use name = %q: %s", blockName, framesDump(frames))
	}
	// End 带完整 ArgsJSON 时丢弃不完整缓存，发全量。
	if strings.Join(partials, "") != `{"city":"深圳"}` {
		t.Fatalf("partial_json = %v", partials)
	}
}

func TestOpenAIStreamRenderer(t *testing.T) {
	r := NewOpenAIStreamRenderer("chatcmpl-1", "m", 123, 10)
	var frames []SSEFrame
	for _, e := range []Event{
		TextDelta{Text: "hi"},
		ToolCallStart{ID: "c1", Name: "f"},
		ToolCallEnd{ID: "c1", Name: "f", ArgsJSON: []byte(`{"a":1}`)},
		Done{FinishReason: "tool_calls"},
	} {
		frames = append(frames, r.Render(e)...)
	}
	// 帧序：role、text、tool start、args 补发、final
	if len(frames) != 5 {
		t.Fatalf("帧数 = %d: %s", len(frames), framesDump(frames))
	}
	var first map[string]any
	_ = json.Unmarshal(frames[0].Data, &first)
	delta := first["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)
	if delta["role"] != "assistant" {
		t.Errorf("首帧应含 role: %v", delta)
	}
	var last map[string]any
	_ = json.Unmarshal(frames[len(frames)-1].Data, &last)
	choice := last["choices"].([]any)[0].(map[string]any)
	if choice["finish_reason"] != "tool_calls" {
		t.Errorf("末帧 finish_reason = %v", choice["finish_reason"])
	}
	if last["usage"] == nil {
		t.Error("末帧应带 usage")
	}
	// args 补发帧
	var argsFrame map[string]any
	_ = json.Unmarshal(frames[3].Data, &argsFrame)
	tc := argsFrame["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
	if tc["function"].(map[string]any)["arguments"] != `{"a":1}` {
		t.Errorf("args 补发错误: %v", tc)
	}
}

func TestAnthropicStreamRenderer(t *testing.T) {
	r := NewAnthropicStreamRenderer("msg_1", "m", 5)
	var frames []SSEFrame
	for _, e := range []Event{
		ReasoningDelta{Text: "think"},
		TextDelta{Text: "hello"},
		ToolCallStart{ID: "c1", Name: "f"},
		ToolCallArgsDelta{ID: "c1", Args: `{}`},
		ToolCallEnd{ID: "c1", Name: "f"},
		Done{FinishReason: "tool_calls"},
	} {
		frames = append(frames, r.Render(e)...)
	}
	var eventNames []string
	for _, f := range frames {
		eventNames = append(eventNames, f.Event)
	}
	want := []string{
		"message_start",
		"content_block_start", "content_block_delta", // thinking
		"content_block_stop", "content_block_start", "content_block_delta", // text
		"content_block_stop", "content_block_start", "content_block_delta", "content_block_stop", // tool_use
		"message_delta", "message_stop",
	}
	if strings.Join(eventNames, ",") != strings.Join(want, ",") {
		t.Fatalf("事件序列不符:\n got %v\nwant %v", eventNames, want)
	}
	var md map[string]any
	_ = json.Unmarshal(frames[len(frames)-2].Data, &md)
	if md["delta"].(map[string]any)["stop_reason"] != "tool_use" {
		t.Errorf("stop_reason = %v", md["delta"])
	}
}

func TestBuildOpenAIResponse(t *testing.T) {
	agg := &Aggregate{
		Text:         "done",
		ToolCalls:    []AggToolCall{{ID: "c1", Name: "f", Args: `{"x":1}`}},
		FinishReason: "tool_calls",
		OutputTokens: 3,
	}
	var resp map[string]any
	_ = json.Unmarshal(BuildOpenAIResponse(agg, "id", "m", 1, 10), &resp)
	msg := resp["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if msg["content"] != "done" {
		t.Errorf("content = %v", msg["content"])
	}
	calls := msg["tool_calls"].([]any)
	if len(calls) != 1 {
		t.Fatalf("tool_calls = %v", calls)
	}
	usage := resp["usage"].(map[string]any)
	if usage["total_tokens"].(float64) != 13 {
		t.Errorf("usage = %v", usage)
	}
}

func TestBuildAnthropicResponse(t *testing.T) {
	agg := &Aggregate{
		Reasoning:    "hmm",
		Text:         "ok",
		ToolCalls:    []AggToolCall{{ID: "c1", Name: "f", Args: `{"x":1}`}},
		FinishReason: "tool_calls",
	}
	var resp map[string]any
	_ = json.Unmarshal(BuildAnthropicResponse(agg, "msg_1", "m", 5), &resp)
	content := resp["content"].([]any)
	if len(content) != 3 {
		t.Fatalf("content 块数 = %d", len(content))
	}
	types := []string{}
	for _, c := range content {
		types = append(types, c.(map[string]any)["type"].(string))
	}
	if strings.Join(types, ",") != "thinking,text,tool_use" {
		t.Errorf("块类型 = %v", types)
	}
	if resp["stop_reason"] != "tool_use" {
		t.Errorf("stop_reason = %v", resp["stop_reason"])
	}
	tu := content[2].(map[string]any)
	if tu["input"].(map[string]any)["x"].(float64) != 1 {
		t.Errorf("tool_use input = %v", tu["input"])
	}
}

func TestEstimatePromptTokens计入工具定义(t *testing.T) {
	req := &ParsedRequest{
		Messages: []NMessage{{Role: "user", Content: []ContentPart{{Type: "text", Text: strings.Repeat("x", 400)}}}},
	}
	base := EstimatePromptTokens(req)
	req.Tools = []NToolDef{{Name: "t", Description: "d", Schema: json.RawMessage(strings.Repeat("s", 398))}}
	if got := EstimatePromptTokens(req); got != base+100 {
		t.Errorf("含工具估算 = %d, want %d", got, base+100)
	}
}

func TestCollectEvents输出计量兜底(t *testing.T) {
	// 无任何 TokenDelta（短回复常见）：输出按透出内容估算托底。
	events := make(chan Event, 4)
	events <- TextDelta{Text: strings.Repeat("蓝", 20)} // 60 字节 → 15 tokens
	events <- Done{FinishReason: "stop"}
	close(events)
	agg := CollectEvents(events)
	if agg.OutputTokens != 15 {
		t.Errorf("OutputTokens = %d, want 15", agg.OutputTokens)
	}

	// TokenDelta 大于估算时以 TokenDelta 为准。
	events = make(chan Event, 4)
	events <- TextDelta{Text: "hi"}
	events <- UsageDelta{Tokens: 900} // 含隐藏 thinking
	events <- Done{FinishReason: "stop"}
	close(events)
	if agg = CollectEvents(events); agg.OutputTokens != 900 {
		t.Errorf("OutputTokens = %d, want 900", agg.OutputTokens)
	}
}

func TestAnthropicStream计量校准(t *testing.T) {
	r := NewAnthropicStreamRenderer("msg_1", "m", 100)
	var frames []SSEFrame
	frames = append(frames, r.Render(TextDelta{Text: strings.Repeat("a", 40)})...) // 10 tokens
	frames = append(frames, r.Render(Done{FinishReason: "stop"})...)
	dump := framesDump(frames)
	if !strings.Contains(dump, `"output_tokens":10`) {
		t.Errorf("终帧缺少兜底输出，dump=%s", dump)
	}
}

func TestOpenAIStream计量校准(t *testing.T) {
	r := NewOpenAIStreamRenderer("c1", "m", 0, 100)
	var frames []SSEFrame
	frames = append(frames, r.Render(TextDelta{Text: strings.Repeat("a", 40)})...)
	frames = append(frames, r.Render(UsageDelta{Tokens: 3})...) // 小于估算 10
	frames = append(frames, r.Render(Done{FinishReason: "stop"})...)
	dump := framesDump(frames)
	if !strings.Contains(dump, `"completion_tokens":10`) || !strings.Contains(dump, `"prompt_tokens":100`) {
		t.Errorf("usage 校准不符，dump=%s", dump)
	}
}

func framesDump(frames []SSEFrame) string {
	var b strings.Builder
	for _, f := range frames {
		b.WriteString(string(EncodeSSE(f)))
	}
	return b.String()
}
