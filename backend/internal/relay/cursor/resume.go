package cursor

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"sync"

	agentpb "github.com/DouDOU-start/airgate-core/internal/relay/cursor/proto/agentpb"
)

const resumeEventBuffer = 64

// pendingMCPCall 保存 Cursor exec 通道中等待结果的一次 MCP 调用。
type pendingMCPCall struct {
	toolCallID string
	name       string
	messageID  uint32
	execID     string
}

type resumeCommand struct {
	results           []*agentpb.ExecClientMessage
	events            chan Event
	downstreamContext context.Context
	ready             chan error
}

// resumableCursorStream 表示一条仍由 Cursor 服务端持有、当前可能等待工具
// 结果的 Connect 流。等待态只在单进程内有效；进程重启或未命中时由
// conversation checkpoint 路径重新建流。
type resumableCursorStream struct {
	mu       sync.Mutex
	waiting  bool
	resuming bool
	closed   bool
	pending  map[string]pendingMCPCall
	resumeCh chan resumeCommand
	done     chan struct{}
	cancel   context.CancelFunc
}

func newResumableCursorStream(cancel context.CancelFunc) *resumableCursorStream {
	return &resumableCursorStream{
		pending:  make(map[string]pendingMCPCall),
		resumeCh: make(chan resumeCommand),
		done:     make(chan struct{}),
		cancel:   cancel,
	}
}

func (s *resumableCursorStream) setWaiting(pending map[string]pendingMCPCall) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.waiting = true
	s.resuming = false
	s.pending = clonePendingCalls(pending)
	s.mu.Unlock()
}

func (s *resumableCursorStream) markClosed() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		close(s.done)
	}
	s.mu.Unlock()
}

func (s *resumableCursorStream) abort() {
	if s != nil && s.cancel != nil {
		s.cancel()
	}
}

// tryResume 把当前请求中的全部匹配 tool_result 写入暂停流，并返回下一段
// Cursor 事件。active=true 表示存在同会话活跃流；结果缺失时返回明确错误，
// 避免同一会话继续等待 lease 而形成死锁。
func (s *resumableCursorStream) tryResume(
	ctx context.Context,
	parsed *ParsedRequest,
) (events <-chan Event, active bool, err error) {
	if s == nil {
		return nil, false, nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, false, nil
	}
	if !s.waiting {
		s.mu.Unlock()
		return nil, true, fmt.Errorf("同一 Cursor 会话的上一段响应仍在生成")
	}
	if s.resuming {
		s.mu.Unlock()
		return nil, true, fmt.Errorf("同一 Cursor 会话正在续接工具结果")
	}
	pending := clonePendingCalls(s.pending)
	results, buildErr := buildMCPResumeResults(parsed, pending)
	if buildErr != nil {
		s.mu.Unlock()
		return nil, true, buildErr
	}
	s.resuming = true
	s.mu.Unlock()
	rollback := func() {
		s.mu.Lock()
		if !s.closed {
			s.resuming = false
		}
		s.mu.Unlock()
	}

	segment := make(chan Event, resumeEventBuffer)
	ready := make(chan error, 1)
	command := resumeCommand{
		results: results, events: segment, downstreamContext: ctx, ready: ready,
	}
	select {
	case s.resumeCh <- command:
	case <-s.done:
		rollback()
		return nil, true, fmt.Errorf("cursor 原始流已结束，请重试以通过 checkpoint 恢复")
	case <-ctx.Done():
		rollback()
		return nil, true, ctx.Err()
	}
	select {
	case readyErr := <-ready:
		if readyErr != nil {
			return nil, true, readyErr
		}
		s.mu.Lock()
		s.waiting = false
		s.resuming = false
		s.pending = make(map[string]pendingMCPCall)
		s.mu.Unlock()
		return segment, true, nil
	case <-s.done:
		return nil, true, fmt.Errorf("cursor 原始流在续接时结束，请重试")
	}
}

func clonePendingCalls(source map[string]pendingMCPCall) map[string]pendingMCPCall {
	cloned := make(map[string]pendingMCPCall, len(source))
	for id, call := range source {
		cloned[id] = call
	}
	return cloned
}

func buildMCPResumeResults(
	parsed *ParsedRequest,
	pending map[string]pendingMCPCall,
) ([]*agentpb.ExecClientMessage, error) {
	if parsed == nil {
		return nil, fmt.Errorf("续接 Cursor 流时请求为空")
	}
	toolResults := make(map[string]NMessage)
	for _, message := range parsed.Messages {
		if message.Role == "tool" && message.ToolCallID != "" {
			toolResults[message.ToolCallID] = message
		}
	}
	calls := make([]pendingMCPCall, 0, len(pending))
	for _, call := range pending {
		calls = append(calls, call)
	}
	sort.Slice(calls, func(i, j int) bool {
		if calls[i].messageID == calls[j].messageID {
			return calls[i].toolCallID < calls[j].toolCallID
		}
		return calls[i].messageID < calls[j].messageID
	})
	results := make([]*agentpb.ExecClientMessage, 0, len(calls))
	for _, call := range calls {
		toolCallID := call.toolCallID
		result, ok := toolResults[toolCallID]
		if !ok {
			return nil, fmt.Errorf("cursor 流正在等待工具 %q 的结果（tool_call_id=%s）", call.name, toolCallID)
		}
		mcpResult := &agentpb.McpResult{}
		if result.IsError {
			mcpResult.Result = &agentpb.McpResult_Error{Error: &agentpb.McpError{
				Error: toolResultText(result),
			}}
		} else {
			mcpResult.Result = &agentpb.McpResult_Success{Success: &agentpb.McpSuccess{
				Content: buildMCPResultContent(result.Content),
			}}
		}
		results = append(results, &agentpb.ExecClientMessage{
			Id: call.messageID, ExecId: call.execID,
			Message: &agentpb.ExecClientMessage_McpResult{McpResult: mcpResult},
		})
	}
	return results, nil
}

func buildMCPResultContent(parts []ContentPart) []*agentpb.McpToolResultContentItem {
	content := make([]*agentpb.McpToolResultContentItem, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "image":
			data, err := base64.StdEncoding.DecodeString(part.ImageData)
			if err != nil {
				continue
			}
			content = append(content, &agentpb.McpToolResultContentItem{Content: &agentpb.McpToolResultContentItem_Image{
				Image: &agentpb.McpImageContent{Data: data, MimeType: part.ImageMime},
			}})
		case "text":
			content = append(content, &agentpb.McpToolResultContentItem{Content: &agentpb.McpToolResultContentItem_Text{
				Text: &agentpb.McpTextContent{Text: part.Text},
			}})
		}
	}
	return content
}

func toolResultText(message NMessage) string {
	text := partsText(message.Content)
	if text == "" {
		return "工具执行失败"
	}
	return text
}

// resumableStreamManager 按账号和下游会话保存单实例活跃流。
type resumableStreamManager struct {
	mu      sync.Mutex
	entries map[string]*resumableCursorStream
}

func newResumableStreamManager() *resumableStreamManager {
	return &resumableStreamManager{entries: make(map[string]*resumableCursorStream)}
}

func resumableStreamKey(sessionKey, accountID string) string {
	if sessionKey == "" || accountID == "" {
		return ""
	}
	return accountID + "\x00" + sessionKey
}

func (m *resumableStreamManager) get(key string) *resumableCursorStream {
	if m == nil || key == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.entries[key]
}

func (m *resumableStreamManager) register(key string, stream *resumableCursorStream) bool {
	if m == nil || key == "" || stream == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing := m.entries[key]; existing != nil {
		return false
	}
	m.entries[key] = stream
	return true
}

func (m *resumableStreamManager) remove(key string, stream *resumableCursorStream) {
	if m == nil || key == "" || stream == nil {
		return
	}
	m.mu.Lock()
	if m.entries[key] == stream {
		delete(m.entries, key)
	}
	m.mu.Unlock()
}
