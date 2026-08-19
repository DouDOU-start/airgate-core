package cursor

import (
	"encoding/json"
	"testing"

	"google.golang.org/protobuf/proto"

	agentpb "github.com/DouDOU-start/airgate-core/internal/relay/cursor/proto/agentpb"
)

func TestParseOpenAIBasic(t *testing.T) {
	payload := []byte(`{
		"model":"claude-4",
		"stream":true,
		"messages":[
			{"role":"system","content":"You are helpful"},
			{"role":"user","content":"hi"},
			{"role":"assistant","content":"hello","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"NY\"}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":"sunny"},
			{"role":"user","content":"thanks"}
		],
		"tools":[{"type":"function","function":{"name":"get_weather","description":"weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}]
	}`)
	req, err := ParseRequest(ProtoOpenAI, payload)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if req.Model != "claude-4" || !req.Stream {
		t.Errorf("model/stream 解析错误: %q %v", req.Model, req.Stream)
	}
	if len(req.SystemPrompts) != 1 || req.SystemPrompts[0] != "You are helpful" {
		t.Errorf("system 解析错误: %v", req.SystemPrompts)
	}
	if len(req.Messages) != 4 {
		t.Fatalf("messages 数量 = %d, 期望 4", len(req.Messages))
	}
	if len(req.Tools) != 1 || req.Tools[0].Name != "get_weather" {
		t.Errorf("tools 解析错误: %v", req.Tools)
	}
	asst := req.Messages[1]
	if asst.Role != "assistant" || len(asst.ToolCalls) != 1 || asst.ToolCalls[0].Name != "get_weather" {
		t.Errorf("assistant tool_call 解析错误: %+v", asst)
	}
}

func TestBuildRunRequestOpenAI(t *testing.T) {
	payload := []byte(`{
		"model":"m",
		"messages":[
			{"role":"system","content":"SYS"},
			{"role":"user","content":"hi"},
			{"role":"assistant","content":"hello","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"NY\"}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":"sunny"},
			{"role":"user","content":"thanks"}
		],
		"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object"}}}]
	}`)
	req, err := ParseRequest(ProtoOpenAI, payload)
	if err != nil {
		t.Fatal(err)
	}
	store := NewBlobStore()
	reqBytes, tools, err := BuildRunRequest(req, "wire-model", "conv-1", store)
	if err != nil {
		t.Fatalf("构建失败: %v", err)
	}
	if len(tools) != 1 {
		t.Errorf("下发工具数 = %d, 期望 1", len(tools))
	}

	var cm agentpb.AgentClientMessage
	if err := proto.Unmarshal(reqBytes, &cm); err != nil {
		t.Fatalf("反序列化 runRequest 失败: %v", err)
	}
	rr := cm.GetRunRequest()
	if rr == nil {
		t.Fatal("runRequest 为空")
	}
	if rr.GetModelDetails().GetModelId() != "wire-model" {
		t.Errorf("model id = %q", rr.GetModelDetails().GetModelId())
	}
	if rr.GetConversationId() != "conv-1" {
		t.Errorf("conversation id = %q", rr.GetConversationId())
	}

	// action 必须是当前 user 消息 "thanks"
	um := rr.GetAction().GetUserMessageAction().GetUserMessage()
	if um == nil || um.GetText() != "thanks" {
		t.Fatalf("action user message = %+v", um)
	}

	// rootPromptMessages = system(1) + 历史(user/assistant/tool = 3)，不含当前 user
	root := rr.GetConversationState().GetRootPromptMessagesJson()
	if len(root) != 4 {
		t.Fatalf("rootPromptMessages 数量 = %d, 期望 4", len(root))
	}
	roles := make([]string, 0, 4)
	var toolEntry map[string]any
	var asstEntry map[string]any
	for _, id := range root {
		data, ok := store.Get(id)
		if !ok {
			t.Fatalf("blob 未找到: %x", id)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("blob 不是合法 JSON: %v", err)
		}
		roles = append(roles, m["role"].(string))
		if m["role"] == "tool" {
			toolEntry = m
		}
		if m["role"] == "assistant" {
			asstEntry = m
		}
	}
	want := []string{"system", "user", "assistant", "tool"}
	for i, r := range want {
		if roles[i] != r {
			t.Errorf("rootPrompt[%d] role = %q, 期望 %q", i, roles[i], r)
		}
	}
	// tool 结果内容与工具名（从配对补全）应保留
	if toolEntry == nil {
		t.Fatal("缺少 tool 条目")
	}
	// 上行侧工具名统一带 wire 前缀，避开 Cursor 内置保留名冲突
	tc := toolEntry["content"].([]any)[0].(map[string]any)
	if tc["result"] != "sunny" || tc["toolName"] != "fn_get_weather" {
		t.Errorf("tool 结果错误: %+v", tc)
	}
	// assistant 的 tool-call 应携带 toolName 与对象化 args
	if asstEntry == nil {
		t.Fatal("缺少 assistant 条目")
	}
	var foundToolCall bool
	for _, part := range asstEntry["content"].([]any) {
		p := part.(map[string]any)
		if p["type"] == "tool-call" {
			foundToolCall = true
			if p["toolName"] != "fn_get_weather" {
				t.Errorf("tool-call toolName = %v", p["toolName"])
			}
			if args, ok := p["args"].(map[string]any); !ok || args["city"] != "NY" {
				t.Errorf("tool-call args = %v", p["args"])
			}
		}
	}
	if !foundToolCall {
		t.Error("assistant 未包含 tool-call")
	}
}

func TestBuildRunRequestResumeWhenTrailingToolResult(t *testing.T) {
	// 以工具结果结尾（无当前 user）应走 resumeAction，历史包含全部消息。
	payload := []byte(`{
		"model":"m",
		"messages":[
			{"role":"user","content":"hi"},
			{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]},
			{"role":"tool","tool_call_id":"c1","content":"done"}
		]
	}`)
	req, err := ParseRequest(ProtoOpenAI, payload)
	if err != nil {
		t.Fatal(err)
	}
	store := NewBlobStore()
	reqBytes, _, err := BuildRunRequest(req, "m", "c", store)
	if err != nil {
		t.Fatal(err)
	}
	var cm agentpb.AgentClientMessage
	if err := proto.Unmarshal(reqBytes, &cm); err != nil {
		t.Fatal(err)
	}
	if cm.GetRunRequest().GetAction().GetResumeAction() == nil {
		t.Error("期望 resumeAction")
	}
}

func TestBuildRunRequestPreservesCheckpointState(t *testing.T) {
	payload := []byte(`{"model":"m","messages":[{"role":"user","content":"当前问题"}]}`)
	req, err := ParseRequest(ProtoOpenAI, payload)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := &agentpb.ConversationStateStructure{
		Todos:            [][]byte{[]byte(`[{"text":"保留待办"}]`)},
		Summary:          []byte("保留摘要"),
		Plan:             []byte("保留计划"),
		PendingToolCalls: []string{"{\"name\":\"fn_tool\"}"},
		FileStates: map[string][]byte{
			"/workspace/a.go": []byte("file-state-blob"),
		},
	}
	store := NewBlobStore()
	raw, _, err := BuildRunRequest(req, "m", "conversation-1", store, checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	var msg agentpb.AgentClientMessage
	if err := proto.Unmarshal(raw, &msg); err != nil {
		t.Fatal(err)
	}
	state := msg.GetRunRequest().GetConversationState()
	if state == nil {
		t.Fatal("conversation state 为空")
	}
	if string(state.GetSummary()) != "保留摘要" || string(state.GetPlan()) != "保留计划" ||
		len(state.GetTodos()) != 1 || string(state.GetTodos()[0]) != `[{"text":"保留待办"}]` ||
		len(state.GetPendingToolCalls()) != 1 || state.GetFileStates()["/workspace/a.go"] == nil {
		t.Fatalf("checkpoint 状态未保留: %+v", state)
	}
	if len(state.GetRootPromptMessagesJson()) != 1 {
		t.Fatalf("当前请求只有 active user，rootPromptMessages 应仅含默认 system: %d", len(state.GetRootPromptMessagesJson()))
	}
	if len(state.GetTurns()) != 0 {
		t.Fatalf("当前请求没有历史消息，turns 应为空: %d", len(state.GetTurns()))
	}
}

func TestParseAnthropicToolBlocks(t *testing.T) {
	payload := []byte(`{
		"model":"claude",
		"system":"be brief",
		"messages":[
			{"role":"user","content":"weather?"},
			{"role":"assistant","content":[{"type":"text","text":"checking"},{"type":"tool_use","id":"tu_1","name":"get_weather","input":{"city":"NY"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_1","content":"sunny"},{"type":"text","text":"and tomorrow?"}]}
		],
		"tools":[{"name":"get_weather","description":"w","input_schema":{"type":"object"}}]
	}`)
	req, err := ParseRequest(ProtoAnthropic, payload)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(req.SystemPrompts) != 1 || req.SystemPrompts[0] != "be brief" {
		t.Errorf("system 解析错误: %v", req.SystemPrompts)
	}
	// user, assistant(tool_use), tool_result -> tool, user(active "and tomorrow?")
	if len(req.Messages) != 4 {
		t.Fatalf("messages 数量 = %d, 期望 4: %+v", len(req.Messages), req.Messages)
	}
	if req.Messages[1].Role != "assistant" || len(req.Messages[1].ToolCalls) != 1 {
		t.Errorf("assistant tool_use 解析错误: %+v", req.Messages[1])
	}
	if req.Messages[2].Role != "tool" || req.Messages[2].ToolCallID != "tu_1" {
		t.Errorf("tool_result 展开错误: %+v", req.Messages[2])
	}
	if req.Messages[3].Role != "user" {
		t.Errorf("末条应为 active user: %+v", req.Messages[3])
	}

	store := NewBlobStore()
	if _, _, err := BuildRunRequest(req, "m", "c", store); err != nil {
		t.Fatalf("构建失败: %v", err)
	}
}

func TestParseAnthropicToolResult保留图片内容(t *testing.T) {
	payload := []byte(`{
		"model":"claude",
		"messages":[
			{"role":"assistant","content":[{"type":"tool_use","id":"tu_image","name":"screenshot","input":{}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_image","content":[
				{"type":"text","text":"截图完成"},
				{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AQID"}}
			]}]}
		]
	}`)
	req, err := ParseRequest(ProtoAnthropic, payload)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(req.Messages) != 2 || req.Messages[1].Role != "tool" || len(req.Messages[1].Content) != 2 {
		t.Fatalf("图片工具结果未完整保留: %+v", req.Messages)
	}
	image := req.Messages[1].Content[1]
	if image.Type != "image" || image.ImageMime != "image/png" || image.ImageData != "AQID" {
		t.Fatalf("图片工具结果错误: %+v", image)
	}
}
