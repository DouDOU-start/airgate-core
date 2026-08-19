package cursor

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
	"sync"

	agentpb "github.com/DouDOU-start/airgate-core/internal/relay/cursor/proto/agentpb"
)

const resumeEventBuffer = 64

type pendingExecKind uint8

const (
	pendingExecMCP pendingExecKind = iota
	pendingExecRead
	pendingExecWrite
	pendingExecShell
	pendingExecShellStream
	pendingExecDelete
	pendingExecGrep
	pendingExecLs
	pendingExecFetch
	pendingExecSubagent
	pendingExecDiagnostics
	pendingExecPiRead
	pendingExecPiBash
	pendingExecPiEdit
	pendingExecPiWrite
	pendingExecPiGrep
	pendingExecPiFind
	pendingExecPiLs
	pendingExecMiniSweBash
)

// pendingExecCall 保存 Cursor exec 通道中已转交下游、等待工具结果的调用。
// 原始参数用于把 Claude Code 的通用 tool_result 还原成 Cursor 要求的类型。
type pendingExecCall struct {
	toolCallID string
	name       string
	messageID  uint32
	execID     string
	kind       pendingExecKind

	path               string
	rangeApplied       bool
	writeContent       string
	writeFileSize      int
	returnWriteContent bool
	command            string
	workingDirectory   string
	pattern            string
	outputMode         string
	grepOffset         *int32
	url                string
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
	pending  map[string]pendingExecCall
	resumeCh chan resumeCommand
	done     chan struct{}
	cancel   context.CancelFunc
}

func newResumableCursorStream(cancel context.CancelFunc) *resumableCursorStream {
	return &resumableCursorStream{
		pending:  make(map[string]pendingExecCall),
		resumeCh: make(chan resumeCommand),
		done:     make(chan struct{}),
		cancel:   cancel,
	}
}

func (s *resumableCursorStream) setWaiting(pending map[string]pendingExecCall) {
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
	results, buildErr := buildExecResumeResults(parsed, pending)
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
		s.pending = make(map[string]pendingExecCall)
		s.mu.Unlock()
		return segment, true, nil
	case <-s.done:
		return nil, true, fmt.Errorf("cursor 原始流在续接时结束，请重试")
	}
}

func clonePendingCalls(source map[string]pendingExecCall) map[string]pendingExecCall {
	cloned := make(map[string]pendingExecCall, len(source))
	for id, call := range source {
		cloned[id] = call
	}
	return cloned
}

func buildExecResumeResults(
	parsed *ParsedRequest,
	pending map[string]pendingExecCall,
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
	calls := make([]pendingExecCall, 0, len(pending))
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
		results = append(results, buildExecResult(call, result)...)
	}
	return results, nil
}

func buildExecResult(call pendingExecCall, result NMessage) []*agentpb.ExecClientMessage {
	base := func() *agentpb.ExecClientMessage {
		return &agentpb.ExecClientMessage{Id: call.messageID, ExecId: call.execID}
	}
	switch call.kind {
	case pendingExecRead:
		readResult := &agentpb.ReadResult{}
		text := normalizeClaudeReadOutput(partsText(result.Content))
		if result.IsError {
			readResult.Result = &agentpb.ReadResult_Error{Error: &agentpb.ReadError{
				Path: call.path, Error: errorResultText(result, "读取文件失败"),
			}}
		} else {
			success := &agentpb.ReadSuccess{
				Path: call.path, TotalLines: textLineCount(text),
				FileSize: int64(len([]byte(text))), RangeApplied: call.rangeApplied,
				Output: &agentpb.ReadSuccess_Content{Content: text},
			}
			if data := firstImageData(result.Content); len(data) > 0 {
				success.Output = &agentpb.ReadSuccess_Data{Data: data}
				success.TotalLines = 0
				success.FileSize = int64(len(data))
			}
			readResult.Result = &agentpb.ReadResult_Success{Success: success}
		}
		message := base()
		message.Message = &agentpb.ExecClientMessage_ReadResult{ReadResult: readResult}
		return []*agentpb.ExecClientMessage{message}
	case pendingExecWrite:
		writeResult := &agentpb.WriteResult{}
		if result.IsError {
			writeResult.Result = &agentpb.WriteResult_Error{Error: &agentpb.WriteError{
				Path: call.path, Error: errorResultText(result, "写入文件失败"),
			}}
		} else {
			success := &agentpb.WriteSuccess{
				Path: call.path, LinesCreated: textLineCount(call.writeContent), FileSize: int32(call.writeFileSize),
			}
			if call.returnWriteContent {
				content := call.writeContent
				success.FileContentAfterWrite = &content
			}
			writeResult.Result = &agentpb.WriteResult_Success{Success: success}
		}
		message := base()
		message.Message = &agentpb.ExecClientMessage_WriteResult{WriteResult: writeResult}
		return []*agentpb.ExecClientMessage{message}
	case pendingExecShell:
		shellResult := buildShellResumeResult(call, result)
		message := base()
		message.Message = &agentpb.ExecClientMessage_ShellResult{ShellResult: shellResult}
		return []*agentpb.ExecClientMessage{message}
	case pendingExecShellStream:
		return buildShellStreamResumeResults(call, result)
	case pendingExecDelete:
		message := base()
		message.Message = &agentpb.ExecClientMessage_DeleteResult{DeleteResult: buildDeleteResumeResult(call, result)}
		return []*agentpb.ExecClientMessage{message}
	case pendingExecGrep:
		message := base()
		message.Message = &agentpb.ExecClientMessage_GrepResult{GrepResult: buildGrepResumeResult(call, result)}
		return []*agentpb.ExecClientMessage{message}
	case pendingExecLs:
		message := base()
		message.Message = &agentpb.ExecClientMessage_LsResult{LsResult: buildLsResumeResult(call, result)}
		return []*agentpb.ExecClientMessage{message}
	case pendingExecFetch:
		message := base()
		message.Message = &agentpb.ExecClientMessage_FetchResult{FetchResult: buildFetchResumeResult(call, result)}
		return []*agentpb.ExecClientMessage{message}
	case pendingExecSubagent:
		message := base()
		message.Message = &agentpb.ExecClientMessage_SubagentResult{SubagentResult: buildSubagentResumeResult(result)}
		return []*agentpb.ExecClientMessage{message}
	case pendingExecDiagnostics:
		message := base()
		message.Message = &agentpb.ExecClientMessage_DiagnosticsResult{
			DiagnosticsResult: buildDiagnosticsResumeResult(call, result),
		}
		return []*agentpb.ExecClientMessage{message}
	case pendingExecPiRead:
		message := base()
		message.Message = &agentpb.ExecClientMessage_PiReadResult{PiReadResult: buildPiReadResumeResult(result)}
		return []*agentpb.ExecClientMessage{message}
	case pendingExecPiBash:
		message := base()
		message.Message = &agentpb.ExecClientMessage_PiBashResult{PiBashResult: buildPiBashResumeResult(result)}
		return []*agentpb.ExecClientMessage{message}
	case pendingExecPiEdit:
		message := base()
		message.Message = &agentpb.ExecClientMessage_PiEditResult{PiEditResult: buildPiEditResumeResult(result)}
		return []*agentpb.ExecClientMessage{message}
	case pendingExecPiWrite:
		message := base()
		message.Message = &agentpb.ExecClientMessage_PiWriteResult{PiWriteResult: buildPiWriteResumeResult(result)}
		return []*agentpb.ExecClientMessage{message}
	case pendingExecPiGrep:
		message := base()
		message.Message = &agentpb.ExecClientMessage_PiGrepResult{PiGrepResult: buildPiGrepResumeResult(result)}
		return []*agentpb.ExecClientMessage{message}
	case pendingExecPiFind:
		message := base()
		message.Message = &agentpb.ExecClientMessage_PiFindResult{PiFindResult: buildPiFindResumeResult(result)}
		return []*agentpb.ExecClientMessage{message}
	case pendingExecPiLs:
		message := base()
		message.Message = &agentpb.ExecClientMessage_PiLsResult{PiLsResult: buildPiLsResumeResult(result)}
		return []*agentpb.ExecClientMessage{message}
	case pendingExecMiniSweBash:
		message := base()
		message.Message = &agentpb.ExecClientMessage_MiniSweAgentBashResult{
			MiniSweAgentBashResult: buildShellResumeResult(call, result),
		}
		return []*agentpb.ExecClientMessage{message}
	default:
		mcpResult := &agentpb.McpResult{}
		if result.IsError {
			mcpResult.Result = &agentpb.McpResult_Error{Error: &agentpb.McpError{
				Error: errorResultText(result, "工具执行失败"),
			}}
		} else {
			mcpResult.Result = &agentpb.McpResult_Success{Success: &agentpb.McpSuccess{
				Content: buildMCPResultContent(result.Content),
			}}
		}
		message := base()
		message.Message = &agentpb.ExecClientMessage_McpResult{McpResult: mcpResult}
		return []*agentpb.ExecClientMessage{message}
	}
}

func buildShellResumeResult(call pendingExecCall, result NMessage) *agentpb.ShellResult {
	text := partsText(result.Content)
	if result.IsError {
		return &agentpb.ShellResult{Result: &agentpb.ShellResult_Failure{Failure: &agentpb.ShellFailure{
			Command: call.command, WorkingDirectory: call.workingDirectory, ExitCode: 1,
			Stderr: errorResultText(result, "命令执行失败"),
		}}}
	}
	return &agentpb.ShellResult{Result: &agentpb.ShellResult_Success{Success: &agentpb.ShellSuccess{
		Command: call.command, WorkingDirectory: call.workingDirectory, Stdout: text,
	}}}
}

func buildShellStreamResumeResults(call pendingExecCall, result NMessage) []*agentpb.ExecClientMessage {
	message := func(stream *agentpb.ShellStream) *agentpb.ExecClientMessage {
		return &agentpb.ExecClientMessage{
			Id: call.messageID, ExecId: call.execID,
			Message: &agentpb.ExecClientMessage_ShellStream{ShellStream: stream},
		}
	}
	results := []*agentpb.ExecClientMessage{message(&agentpb.ShellStream{
		Event: &agentpb.ShellStream_Start{Start: &agentpb.ShellStreamStart{}},
	})}
	text := partsText(result.Content)
	if text != "" {
		if result.IsError {
			results = append(results, message(&agentpb.ShellStream{
				Event: &agentpb.ShellStream_Stderr{Stderr: &agentpb.ShellStreamStderr{Data: text}},
			}))
		} else {
			results = append(results, message(&agentpb.ShellStream{
				Event: &agentpb.ShellStream_Stdout{Stdout: &agentpb.ShellStreamStdout{Data: text}},
			}))
		}
	}
	code := uint32(0)
	if result.IsError {
		code = 1
	}
	results = append(results, message(&agentpb.ShellStream{Event: &agentpb.ShellStream_Exit{
		Exit: &agentpb.ShellStreamExit{Code: code, Cwd: call.workingDirectory},
	}}))
	return results
}

func textLineCount(text string) int32 {
	if text == "" {
		return 0
	}
	return int32(strings.Count(text, "\n") + 1)
}

func errorResultText(message NMessage, fallback string) string {
	if text := partsText(message.Content); text != "" {
		return text
	}
	return fallback
}

func firstImageData(parts []ContentPart) []byte {
	for _, part := range parts {
		if part.Type != "image" || part.ImageData == "" {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(part.ImageData)
		if err == nil {
			return data
		}
	}
	return nil
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
