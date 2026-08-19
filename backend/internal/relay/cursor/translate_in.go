package cursor

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	agentpb "github.com/DouDOU-start/airgate-core/internal/relay/cursor/proto/agentpb"
)

// providerIdentifier 是下发工具的 MCP provider 标识；沿用 oh-my-pi 的取值，
// 避免 Cursor 服务端因未知 provider 拒绝工具。
const providerIdentifier = "pi-agent"

// toolWirePrefix 是上行 Cursor 的工具名统一前缀。Cursor 服务端对与其内置
// 工具重名的 MCP 工具定义直接报错关流（实测 Read/Write/Glob/Grep/Task/
// WebFetch/WebSearch/TodoWrite 等必挂，Claude Code 恰好全带），保留名单
// 未知且随版本变化，故全部加前缀绕开，事件截获侧再还原为下游原名。
const toolWirePrefix = "fn_"

// wireToolName 把下游工具名转为上行 Cursor 的 wire 名；空名原样返回。
func wireToolName(name string) string {
	if name == "" {
		return ""
	}
	return toolWirePrefix + name
}

// localToolName 把 Cursor 回传的 wire 工具名还原为下游原名。
func localToolName(name string) string { return strings.TrimPrefix(name, toolWirePrefix) }

// cursorUUIDNamespace 用于把历史消息内容映射成稳定 messageId（同内容同 id，
// 利于服务端 blob 缓存命中）。取 RFC 4122 OID 命名空间。
var cursorUUIDNamespace = uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")

// BuildRunRequest 把解析后的入口请求构建成 Cursor 的首帧 runRequest 字节，并
// 返回需经 exec handshake 下发的工具定义。blobStore 会写入 rootPromptMessages /
// turns 引用的 blob 内容，须在整条流期间保留以应答服务端 getBlob。
func BuildRunRequest(
	req *ParsedRequest,
	wireModel, conversationID string,
	blobStore BlobStore,
) (reqBytes []byte, tools []*agentpb.McpToolDefinition, err error) {
	if req == nil {
		return nil, nil, fmt.Errorf("请求为空")
	}
	if strings.TrimSpace(wireModel) == "" {
		return nil, nil, fmt.Errorf("缺少 model")
	}

	activeIdx := len(req.Messages) - 1
	var activeUser *NMessage
	if activeIdx >= 0 && req.Messages[activeIdx].Role == "user" && messageHasContent(req.Messages[activeIdx]) {
		activeUser = &req.Messages[activeIdx]
	}
	historyEnd := len(req.Messages)
	if activeUser != nil {
		historyEnd = activeIdx
	}

	systemPromptIDs := buildSystemPromptBlobs(req.SystemPrompts, blobStore)
	rootIDs, err := buildRootPromptMessages(req.Messages[:historyEnd], systemPromptIDs, blobStore)
	if err != nil {
		return nil, nil, err
	}
	turnIDs, err := buildConversationTurns(req.Messages[:historyEnd], blobStore)
	if err != nil {
		return nil, nil, err
	}

	action, err := buildAction(activeUser)
	if err != nil {
		return nil, nil, err
	}

	tools, err = buildToolDefinitions(req.Tools)
	if err != nil {
		return nil, nil, err
	}

	convState := &agentpb.ConversationStateStructure{
		RootPromptMessagesJson: rootIDs,
		Turns:                  turnIDs,
	}
	model := &agentpb.ModelDetails{
		ModelId:        wireModel,
		DisplayModelId: wireModel,
		DisplayName:    wireModel,
	}
	convID := conversationID
	runReq := &agentpb.AgentRunRequest{
		ConversationState: convState,
		Action:            action,
		ModelDetails:      model,
		RequestedModel:    &agentpb.RequestedModel{ModelId: wireModel},
		ConversationId:    &convID,
	}
	clientMsg := &agentpb.AgentClientMessage{
		Message: &agentpb.AgentClientMessage_RunRequest{RunRequest: runReq},
	}
	reqBytes, err = proto.Marshal(clientMsg)
	if err != nil {
		return nil, nil, fmt.Errorf("序列化 runRequest 失败: %w", err)
	}
	return reqBytes, tools, nil
}

// ---- rootPromptMessages（服务端据此构建实际 prompt） ----

type rpSystemMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type rpRoleMsg struct {
	Role    string `json:"role"`
	Content []any  `json:"content"`
}

type rpToolMsg struct {
	Role    string `json:"role"`
	ID      string `json:"id"`
	Content []any  `json:"content"`
}

type rpText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type rpImage struct {
	Type      string `json:"type"`
	Image     string `json:"image"`
	MediaType string `json:"mediaType"`
}

type rpToolCall struct {
	Type       string          `json:"type"`
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	Args       json.RawMessage `json:"args"`
}

type rpToolResult struct {
	Type       string `json:"type"`
	ToolName   string `json:"toolName"`
	ToolCallID string `json:"toolCallId"`
	Result     string `json:"result"`
	IsError    bool   `json:"isError,omitempty"`
}

// buildSystemPromptBlobs 把全部系统提示合并为一条 blob。实测 Cursor 上游对
// rootPromptMessages 里多条独立 system 条目会稳定报 resource_exhausted（429，
// Claude Code 的 mid-conversation system 并入提示链后必现），同样内容合并为
// 单条则正常，故这里不做逐条拆分。
func buildSystemPromptBlobs(prompts []string, store BlobStore) [][]byte {
	var out [][]byte
	nonEmpty := make([]string, 0, len(prompts))
	for _, p := range prompts {
		if strings.TrimSpace(p) != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	if len(nonEmpty) == 0 {
		nonEmpty = []string{"You are a helpful assistant."}
	}
	if len(nonEmpty) > 1 {
		nonEmpty = []string{strings.Join(nonEmpty, "\n\n")}
	}
	for _, p := range nonEmpty {
		data, _ := json.Marshal(rpSystemMsg{Role: "system", Content: p})
		out = append(out, store.Store(data))
	}
	return out
}

func buildRootPromptMessages(history []NMessage, systemPromptIDs [][]byte, store BlobStore) ([][]byte, error) {
	entries := make([][]byte, 0, len(systemPromptIDs)+len(history))
	entries = append(entries, systemPromptIDs...)

	toolNames := toolNameByCallID(history)
	push := func(v any) error {
		data, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("序列化 rootPrompt 消息失败: %w", err)
		}
		entries = append(entries, store.Store(data))
		return nil
	}

	for _, msg := range history {
		switch msg.Role {
		case "user":
			content := rootUserContent(msg)
			if len(content) == 0 {
				continue
			}
			if err := push(rpRoleMsg{Role: "user", Content: content}); err != nil {
				return nil, err
			}
		case "assistant":
			content := rootAssistantContent(msg)
			if len(content) == 0 {
				continue
			}
			if err := push(rpRoleMsg{Role: "assistant", Content: content}); err != nil {
				return nil, err
			}
		case "tool":
			name := msg.ToolName
			if name == "" {
				name = toolNames[msg.ToolCallID]
			}
			result := rpToolResult{
				Type:       "tool-result",
				ToolName:   wireToolName(name),
				ToolCallID: msg.ToolCallID,
				Result:     partsText(msg.Content),
				IsError:    msg.IsError,
			}
			if err := push(rpToolMsg{Role: "tool", ID: msg.ToolCallID, Content: []any{result}}); err != nil {
				return nil, err
			}
		}
	}
	return entries, nil
}

func rootUserContent(msg NMessage) []any {
	var content []any
	for _, p := range msg.Content {
		switch p.Type {
		case "text":
			if p.Text != "" {
				content = append(content, rpText{Type: "text", Text: p.Text})
			}
		case "image":
			content = append(content, rpImage{
				Type:      "image",
				Image:     fmt.Sprintf("data:%s;base64,%s", p.ImageMime, p.ImageData),
				MediaType: p.ImageMime,
			})
		}
	}
	return content
}

func rootAssistantContent(msg NMessage) []any {
	var content []any
	for _, p := range msg.Content {
		if p.Type == "text" && p.Text != "" {
			content = append(content, rpText{Type: "text", Text: p.Text})
		}
	}
	for _, tc := range msg.ToolCalls {
		args := tc.Args
		if len(strings.TrimSpace(string(args))) == 0 {
			args = json.RawMessage("{}")
		}
		content = append(content, rpToolCall{
			Type:       "tool-call",
			ToolCallID: tc.ID,
			ToolName:   wireToolName(tc.Name),
			Args:       args,
		})
	}
	return content
}

// ---- turns（UI/展示元数据，protobuf blob） ----

func buildConversationTurns(history []NMessage, store BlobStore) ([][]byte, error) {
	toolResults := make(map[string]*NMessage)
	paired := make(map[string]bool)
	for i := range history {
		m := &history[i]
		switch m.Role {
		case "tool":
			toolResults[m.ToolCallID] = m
		case "assistant":
			for _, tc := range m.ToolCalls {
				paired[tc.ID] = true
			}
		}
	}

	var turns [][]byte
	i := 0
	for i < len(history) {
		msg := history[i]
		if msg.Role != "user" {
			i++
			continue
		}
		if !messageHasContent(msg) {
			i++
			continue
		}
		userMsg := buildUserMessage(msg, deterministicUUID(fmt.Sprintf("u:%d:%s", len(turns), partsText(msg.Content))))
		umBytes, err := proto.Marshal(userMsg)
		if err != nil {
			return nil, err
		}
		userBlobID := store.Store(umBytes)

		var stepIDs [][]byte
		i++
		for i < len(history) && history[i].Role != "user" {
			stepMsg := history[i]
			switch stepMsg.Role {
			case "assistant":
				steps, err := assistantSteps(stepMsg, toolResults)
				if err != nil {
					return nil, err
				}
				for _, s := range steps {
					b, err := proto.Marshal(s)
					if err != nil {
						return nil, err
					}
					stepIDs = append(stepIDs, store.Store(b))
				}
			case "tool":
				if paired[stepMsg.ToolCallID] {
					break
				}
				text := partsText(stepMsg.Content)
				if text == "" {
					break
				}
				prefix := "[Tool Result]"
				if stepMsg.IsError {
					prefix = "[Tool Error]"
				}
				step := &agentpb.ConversationStep{
					Message: &agentpb.ConversationStep_AssistantMessage{
						AssistantMessage: &agentpb.AssistantMessage{Text: prefix + "\n" + text},
					},
				}
				b, err := proto.Marshal(step)
				if err != nil {
					return nil, err
				}
				stepIDs = append(stepIDs, store.Store(b))
			}
			i++
		}

		turn := &agentpb.ConversationTurnStructure{
			Turn: &agentpb.ConversationTurnStructure_AgentConversationTurn{
				AgentConversationTurn: &agentpb.AgentConversationTurnStructure{
					UserMessage: userBlobID,
					Steps:       stepIDs,
				},
			},
		}
		tb, err := proto.Marshal(turn)
		if err != nil {
			return nil, err
		}
		turns = append(turns, store.Store(tb))
	}
	return turns, nil
}

func assistantSteps(msg NMessage, toolResults map[string]*NMessage) ([]*agentpb.ConversationStep, error) {
	var steps []*agentpb.ConversationStep
	for _, p := range msg.Content {
		if p.Type == "text" && p.Text != "" {
			steps = append(steps, &agentpb.ConversationStep{
				Message: &agentpb.ConversationStep_AssistantMessage{
					AssistantMessage: &agentpb.AssistantMessage{Text: p.Text},
				},
			})
		}
	}
	for _, tc := range msg.ToolCalls {
		mcpCall, err := buildMcpToolCall(tc, toolResults[tc.ID])
		if err != nil {
			return nil, err
		}
		steps = append(steps, &agentpb.ConversationStep{
			Message: &agentpb.ConversationStep_ToolCall{
				ToolCall: &agentpb.ToolCall{
					Tool:       &agentpb.ToolCall_McpToolCall{McpToolCall: mcpCall},
					ToolCallId: &tc.ID,
				},
			},
		})
	}
	return steps, nil
}

func buildMcpToolCall(tc NToolCall, result *NMessage) (*agentpb.McpToolCall, error) {
	argsMap, err := encodeMcpArgs(tc.Args)
	if err != nil {
		return nil, err
	}
	call := &agentpb.McpToolCall{
		Args: &agentpb.McpArgs{
			Name:               wireToolName(tc.Name),
			Args:               argsMap,
			ToolCallId:         tc.ID,
			ProviderIdentifier: providerIdentifier,
			ToolName:           wireToolName(tc.Name),
		},
	}
	if result != nil {
		call.Result = buildMcpToolResult(result)
	}
	return call, nil
}

func buildMcpToolResult(result *NMessage) *agentpb.McpToolResult {
	if result.IsError {
		return &agentpb.McpToolResult{
			Result: &agentpb.McpToolResult_Error{
				Error: &agentpb.McpToolError{Error: partsText(result.Content)},
			},
		}
	}
	var items []*agentpb.McpToolResultContentItem
	for _, p := range result.Content {
		if p.Type == "text" {
			items = append(items, &agentpb.McpToolResultContentItem{
				Content: &agentpb.McpToolResultContentItem_Text{
					Text: &agentpb.McpTextContent{Text: p.Text},
				},
			})
		}
	}
	return &agentpb.McpToolResult{
		Result: &agentpb.McpToolResult_Success{
			Success: &agentpb.McpSuccess{Content: items},
		},
	}
}

// ---- action / user message ----

func buildAction(activeUser *NMessage) (*agentpb.ConversationAction, error) {
	if activeUser == nil {
		return &agentpb.ConversationAction{
			Action: &agentpb.ConversationAction_ResumeAction{ResumeAction: &agentpb.ResumeAction{}},
		}, nil
	}
	um := buildUserMessage(*activeUser, uuid.NewString())
	return &agentpb.ConversationAction{
		Action: &agentpb.ConversationAction_UserMessageAction{
			UserMessageAction: &agentpb.UserMessageAction{UserMessage: um},
		},
	}, nil
}

func buildUserMessage(msg NMessage, messageID string) *agentpb.UserMessage {
	um := &agentpb.UserMessage{
		Text:      partsText(msg.Content),
		MessageId: messageID,
	}
	var images []*agentpb.SelectedImage
	for _, p := range msg.Content {
		if p.Type != "image" {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(p.ImageData)
		if err != nil {
			continue
		}
		images = append(images, &agentpb.SelectedImage{
			Uuid:         uuid.NewString(),
			MimeType:     p.ImageMime,
			DataOrBlobId: &agentpb.SelectedImage_Data{Data: data},
		})
	}
	if len(images) > 0 {
		um.SelectedContext = &agentpb.SelectedContext{SelectedImages: images}
	}
	return um
}

// ---- tools ----

func buildToolDefinitions(tools []NToolDef) ([]*agentpb.McpToolDefinition, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	out := make([]*agentpb.McpToolDefinition, 0, len(tools))
	for _, t := range tools {
		schemaBytes, err := encodeToolSchema(t.Schema)
		if err != nil {
			return nil, fmt.Errorf("编码工具 %s 的 schema 失败: %w", t.Name, err)
		}
		out = append(out, &agentpb.McpToolDefinition{
			Name:               wireToolName(t.Name),
			Description:        t.Description,
			ProviderIdentifier: providerIdentifier,
			ToolName:           wireToolName(t.Name),
			InputSchema:        schemaBytes,
		})
	}
	return out, nil
}

func encodeToolSchema(schema json.RawMessage) ([]byte, error) {
	var v any
	trimmed := strings.TrimSpace(string(schema))
	if trimmed == "" || trimmed == "null" {
		v = map[string]any{"type": "object", "properties": map[string]any{}, "required": []any{}}
	} else if err := json.Unmarshal(schema, &v); err != nil {
		return nil, err
	}
	return encodeStructpbValue(v)
}

func encodeMcpArgs(args json.RawMessage) (map[string][]byte, error) {
	trimmed := strings.TrimSpace(string(args))
	if trimmed == "" || trimmed == "null" {
		return map[string][]byte{}, nil
	}
	var obj map[string]any
	if err := json.Unmarshal(args, &obj); err != nil {
		// 非对象参数无法映射为 McpArgs.args；忽略而非报错，交由服务端处理。
		return map[string][]byte{}, nil
	}
	out := make(map[string][]byte, len(obj))
	for k, val := range obj {
		encoded, err := encodeStructpbValue(val)
		if err != nil {
			return nil, err
		}
		out[k] = encoded
	}
	return out, nil
}

func encodeStructpbValue(v any) ([]byte, error) {
	val, err := structpb.NewValue(v)
	if err != nil {
		return nil, err
	}
	return proto.Marshal(val)
}

// ---- helpers ----

func partsText(parts []ContentPart) string {
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

func messageHasContent(msg NMessage) bool {
	if partsText(msg.Content) != "" {
		return true
	}
	for _, p := range msg.Content {
		if p.Type == "image" && p.ImageData != "" {
			return true
		}
	}
	return len(msg.ToolCalls) > 0
}

func toolNameByCallID(history []NMessage) map[string]string {
	names := make(map[string]string)
	for _, m := range history {
		if m.Role != "assistant" {
			continue
		}
		for _, tc := range m.ToolCalls {
			if tc.ID != "" && tc.Name != "" {
				names[tc.ID] = tc.Name
			}
		}
	}
	return names
}

func deterministicUUID(seed string) string {
	return uuid.NewSHA1(cursorUUIDNamespace, []byte(seed)).String()
}
