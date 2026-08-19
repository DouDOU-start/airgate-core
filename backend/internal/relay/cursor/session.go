package cursor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	agentpb "github.com/DouDOU-start/airgate-core/internal/relay/cursor/proto/agentpb"
)

// execRejectReason 是对服务端发起的本地工具执行请求的统一拒绝话术：
// 网关模式下没有本地文件系统/shell，引导模型只使用下发的 function 工具。
const execRejectReason = "This tool is not available in API gateway mode; respond using only the provided function tools or plain text."

// toolCallDrainWindow 是截获首个工具调用后继续收流的窗口，用于收齐同一
// 模型调用产生的并行 tool calls，窗口结束后关闭流并以 tool_calls 收尾。
const toolCallDrainWindow = 400 * time.Millisecond

// SessionOptions 描述一次 Run 会话所需的全部输入。
type SessionOptions struct {
	Client       *Client
	Run          RunOptions
	RequestBytes []byte
	Tools        []*agentpb.McpToolDefinition
	Store        BlobStore
}

// RunSession 打开 Connect 流并驱动完整会话，事件按序写入返回的 channel，
// 结束时（Done 或 ErrEvent 之后）关闭 channel。
func RunSession(ctx context.Context, opts SessionOptions) <-chan Event {
	events := make(chan Event, 64)
	go func() {
		defer close(events)
		s := &session{
			store:   opts.Store,
			tools:   opts.Tools,
			events:  events,
			started: make(map[string]*trackedCall),
		}
		s.run(ctx, opts)
	}()
	return events
}

type trackedCall struct {
	name      string
	argsSoFar int  // 已透出的聚合参数文本长度
	ended     bool // 已 emit ToolCallEnd
}

type session struct {
	stream *Stream
	store  BlobStore
	tools  []*agentpb.McpToolDefinition
	events chan<- Event

	sendMu sync.Mutex

	started     map[string]*trackedCall
	sawToolCall bool
	finishing   bool
	finishTimer *time.Timer
	doneSent    bool
}

func (s *session) run(ctx context.Context, opts SessionOptions) {
	stream, err := opts.Client.OpenRun(ctx, opts.Run)
	if err != nil {
		s.emit(ErrEvent{Err: err})
		return
	}
	s.stream = stream
	defer func() {
		if s.finishTimer != nil {
			s.finishTimer.Stop()
		}
		_ = stream.Close()
	}()

	if err := stream.Send(opts.RequestBytes, false); err != nil {
		s.emit(ErrEvent{Err: err})
		return
	}

	for {
		end, payload, err := stream.Recv()
		if err != nil {
			switch {
			case s.finishing:
				s.finish("tool_calls")
			case errors.Is(err, io.EOF):
				s.finish(s.defaultFinishReason())
			default:
				s.emit(ErrEvent{Err: err})
			}
			return
		}
		if end {
			if e := EndStreamError(payload); e != nil {
				s.emit(ErrEvent{Err: e})
			} else {
				s.finish(s.defaultFinishReason())
			}
			return
		}
		var sm agentpb.AgentServerMessage
		if err := proto.Unmarshal(payload, &sm); err != nil {
			// 无法解析的帧不能静默丢弃：若它是需要应答的请求，服务端会
			// 永远等待导致整条流挂死。至少留痕便于定位。
			slog.Warn("cursor_session_unmarshal_failed", "bytes", len(payload), "error", err)
			continue
		}
		slog.Debug("cursor_session_server_message", "type", serverMessageKind(&sm))
		if stop := s.handle(&sm); stop {
			return
		}
	}
}

func (s *session) defaultFinishReason() string {
	if s.sawToolCall {
		return "tool_calls"
	}
	return "stop"
}

func (s *session) emit(e Event) {
	s.events <- e
}

func (s *session) finish(reason string) {
	if s.doneSent {
		return
	}
	s.doneSent = true
	s.emit(Done{FinishReason: reason})
}

func (s *session) send(msg *agentpb.AgentClientMessage) {
	payload, err := proto.Marshal(msg)
	if err != nil {
		return
	}
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	_ = s.stream.Send(payload, false)
}

// serverMessageKind 返回服务端消息的简短类型名（含内层 oneof），仅用于日志。
func serverMessageKind(sm *agentpb.AgentServerMessage) string {
	switch m := sm.Message.(type) {
	case *agentpb.AgentServerMessage_KvServerMessage:
		return fmt.Sprintf("kv/%T", m.KvServerMessage.GetMessage())
	case *agentpb.AgentServerMessage_ExecServerMessage:
		return fmt.Sprintf("exec/%T", m.ExecServerMessage.GetMessage())
	case *agentpb.AgentServerMessage_InteractionUpdate:
		return fmt.Sprintf("update/%T", m.InteractionUpdate.GetMessage())
	case *agentpb.AgentServerMessage_InteractionQuery:
		return fmt.Sprintf("query/%T", m.InteractionQuery.GetQuery())
	default:
		return fmt.Sprintf("%T", sm.Message)
	}
}

func (s *session) handle(sm *agentpb.AgentServerMessage) (stop bool) {
	switch m := sm.Message.(type) {
	case *agentpb.AgentServerMessage_KvServerMessage:
		s.handleKv(m.KvServerMessage)
	case *agentpb.AgentServerMessage_ExecServerMessage:
		s.handleExec(m.ExecServerMessage)
	case *agentpb.AgentServerMessage_InteractionUpdate:
		return s.handleUpdate(m.InteractionUpdate)
	case *agentpb.AgentServerMessage_InteractionQuery:
		// 交互式询问（ask_question / web_search 确认等）在网关模式下无人应答，
		// 忽略；模型侧一般不会走到这里，联调若遇到再补拒绝应答。
		slog.Warn("cursor_session_query_ignored", "type", serverMessageKind(sm))
	case *agentpb.AgentServerMessage_ConversationCheckpointUpdate:
		// 无状态网关不持久化 checkpoint。
	default:
		slog.Warn("cursor_session_unhandled_message", "type", serverMessageKind(sm))
	}
	return false
}

// ---- KV：服务端按需索取/写回 blob ----

func (s *session) handleKv(kv *agentpb.KvServerMessage) {
	switch m := kv.Message.(type) {
	case *agentpb.KvServerMessage_GetBlobArgs:
		data, _ := s.store.Get(m.GetBlobArgs.GetBlobId())
		s.send(&agentpb.AgentClientMessage{Message: &agentpb.AgentClientMessage_KvClientMessage{
			KvClientMessage: &agentpb.KvClientMessage{
				Id: kv.GetId(),
				Message: &agentpb.KvClientMessage_GetBlobResult{
					GetBlobResult: &agentpb.GetBlobResult{BlobData: data},
				},
			},
		}})
	case *agentpb.KvServerMessage_SetBlobArgs:
		s.store.Set(m.SetBlobArgs.GetBlobId(), m.SetBlobArgs.GetBlobData())
		s.send(&agentpb.AgentClientMessage{Message: &agentpb.AgentClientMessage_KvClientMessage{
			KvClientMessage: &agentpb.KvClientMessage{
				Id:      kv.GetId(),
				Message: &agentpb.KvClientMessage_SetBlobResult{SetBlobResult: &agentpb.SetBlobResult{}},
			},
		}})
	}
}

// ---- exec：服务端驱动的工具执行请求 ----

func (s *session) handleExec(ex *agentpb.ExecServerMessage) {
	execID := ex.GetId()
	reply := func(result any) {
		msg := &agentpb.ExecClientMessage{Id: execID}
		switch r := result.(type) {
		case *agentpb.RequestContextResult:
			msg.Message = &agentpb.ExecClientMessage_RequestContextResult{RequestContextResult: r}
		case *agentpb.ShellResult:
			msg.Message = &agentpb.ExecClientMessage_ShellResult{ShellResult: r}
		case *agentpb.ReadResult:
			msg.Message = &agentpb.ExecClientMessage_ReadResult{ReadResult: r}
		case *agentpb.WriteResult:
			msg.Message = &agentpb.ExecClientMessage_WriteResult{WriteResult: r}
		case *agentpb.DeleteResult:
			msg.Message = &agentpb.ExecClientMessage_DeleteResult{DeleteResult: r}
		case *agentpb.GrepResult:
			msg.Message = &agentpb.ExecClientMessage_GrepResult{GrepResult: r}
		case *agentpb.LsResult:
			msg.Message = &agentpb.ExecClientMessage_LsResult{LsResult: r}
		case *agentpb.DiagnosticsResult:
			msg.Message = &agentpb.ExecClientMessage_DiagnosticsResult{DiagnosticsResult: r}
		case *agentpb.FetchResult:
			msg.Message = &agentpb.ExecClientMessage_FetchResult{FetchResult: r}
		default:
			return
		}
		s.replyExec(msg)
	}

	switch m := ex.Message.(type) {
	case *agentpb.ExecServerMessage_RequestContextArgs:
		reply(&agentpb.RequestContextResult{
			Result: &agentpb.RequestContextResult_Success{
				Success: &agentpb.RequestContextSuccess{
					RequestContext: &agentpb.RequestContext{
						Tools: s.tools,
						Env: &agentpb.RequestContextEnv{
							OsVersion: "linux",
							Shell:     "/bin/bash",
						},
					},
				},
			},
		})
	case *agentpb.ExecServerMessage_McpArgs:
		s.onMcpExec(m.McpArgs)
	case *agentpb.ExecServerMessage_ShellArgs:
		reply(rejectedShellResult())
	case *agentpb.ExecServerMessage_ShellStreamArgs:
		// 流式 shell 的应答通道是 ShellStream 事件，不是 ShellResult；回错
		// 类型服务端会永远等待，挂死整条流（实测 Claude Code 场景必现）。
		s.replyExec(&agentpb.ExecClientMessage{
			Id: execID,
			Message: &agentpb.ExecClientMessage_ShellStream{
				ShellStream: &agentpb.ShellStream{
					Event: &agentpb.ShellStream_Rejected{Rejected: &agentpb.ShellRejected{
						Command: m.ShellStreamArgs.GetCommand(),
						Reason:  execRejectReason,
					}},
				},
			},
		})
	case *agentpb.ExecServerMessage_ReadArgs:
		reply(&agentpb.ReadResult{
			Result: &agentpb.ReadResult_Rejected{Rejected: &agentpb.ReadRejected{Reason: execRejectReason}},
		})
	case *agentpb.ExecServerMessage_WriteArgs:
		reply(&agentpb.WriteResult{
			Result: &agentpb.WriteResult_Rejected{Rejected: &agentpb.WriteRejected{Reason: execRejectReason}},
		})
	case *agentpb.ExecServerMessage_DeleteArgs:
		reply(&agentpb.DeleteResult{
			Result: &agentpb.DeleteResult_Error{Error: &agentpb.DeleteError{Error: execRejectReason}},
		})
	case *agentpb.ExecServerMessage_GrepArgs:
		reply(&agentpb.GrepResult{
			Result: &agentpb.GrepResult_Error{Error: &agentpb.GrepError{Error: execRejectReason}},
		})
	case *agentpb.ExecServerMessage_LsArgs:
		reply(&agentpb.LsResult{
			Result: &agentpb.LsResult_Error{Error: &agentpb.LsError{Error: execRejectReason}},
		})
	case *agentpb.ExecServerMessage_DiagnosticsArgs:
		reply(&agentpb.DiagnosticsResult{
			Result: &agentpb.DiagnosticsResult_Error{Error: &agentpb.DiagnosticsError{Error: execRejectReason}},
		})
	case *agentpb.ExecServerMessage_FetchArgs:
		reply(&agentpb.FetchResult{
			Result: &agentpb.FetchResult_Error{Error: &agentpb.FetchError{Url: m.FetchArgs.GetUrl(), Error: execRejectReason}},
		})
	default:
		// 其余小众 exec 类型（后台 shell、子代理等）暂不应答；
		// 若联调发现服务端因此挂起，再补对应的拒绝变体。
		slog.Warn("cursor_session_exec_unanswered", "type", fmt.Sprintf("%T", ex.Message))
	}
}

func rejectedShellResult() *agentpb.ShellResult {
	return &agentpb.ShellResult{
		Result: &agentpb.ShellResult_Rejected{Rejected: &agentpb.ShellRejected{Reason: execRejectReason}},
	}
}

func (s *session) replyExec(msg *agentpb.ExecClientMessage) {
	s.send(&agentpb.AgentClientMessage{Message: &agentpb.AgentClientMessage_ExecClientMessage{
		ExecClientMessage: msg,
	}})
}

// trackTool 登记（或补全）一次工具调用的跟踪状态：首次见到该 id 时 emit
// ToolCallStart；此后任何来源带来非空工具名都回填（interaction update 常先到
// 且不带名，exec McpArgs 后到才带名，不回填会导致 tool_calls 名字为空）。
func (s *session) trackTool(id, name string) *trackedCall {
	tc := s.started[id]
	if tc == nil {
		tc = &trackedCall{name: name}
		s.started[id] = tc
		s.emit(ToolCallStart{ID: id, Name: name})
		return tc
	}
	if tc.name == "" && name != "" {
		tc.name = name
	}
	return tc
}

// mcpArgsToolName 从 McpArgs 提取下游原工具名（还原 wire 前缀）。
func mcpArgsToolName(args *agentpb.McpArgs) string {
	name := args.GetName()
	if name == "" {
		name = args.GetToolName()
	}
	return localToolName(name)
}

// onMcpExec 是 function calling 的截获点：服务端请求执行我们下发的工具时，
// 不真正执行，而是把完整参数透出为 tool_calls，随后在 drain 窗口后关流收尾，
// 由 API 调用方执行工具并在下一次请求中携带结果。
func (s *session) onMcpExec(args *agentpb.McpArgs) {
	id := args.GetToolCallId()
	tc := s.trackTool(id, mcpArgsToolName(args))
	if !tc.ended {
		tc.ended = true
		argsJSON := decodeMcpArgsMap(args.GetArgs())
		s.emit(ToolCallEnd{ID: id, Name: tc.name, ArgsJSON: argsJSON})
	}
	s.sawToolCall = true
	s.finishing = true
	if s.finishTimer != nil {
		s.finishTimer.Stop()
	}
	stream := s.stream
	s.finishTimer = time.AfterFunc(toolCallDrainWindow, func() { _ = stream.Close() })
}

// ---- 生成增量 ----

func (s *session) handleUpdate(u *agentpb.InteractionUpdate) (stop bool) {
	switch m := u.Message.(type) {
	case *agentpb.InteractionUpdate_TextDelta:
		if t := m.TextDelta.GetText(); t != "" {
			s.emit(TextDelta{Text: t})
		}
	case *agentpb.InteractionUpdate_ThinkingDelta:
		if t := m.ThinkingDelta.GetText(); t != "" {
			s.emit(ReasoningDelta{Text: t})
		}
	case *agentpb.InteractionUpdate_PartialToolCall:
		s.onPartialToolCall(m.PartialToolCall)
	case *agentpb.InteractionUpdate_ToolCallStarted:
		s.onToolCallStarted(m.ToolCallStarted)
	case *agentpb.InteractionUpdate_ToolCallCompleted:
		s.onToolCallCompleted(m.ToolCallCompleted)
	case *agentpb.InteractionUpdate_TokenDelta:
		if n := int(m.TokenDelta.GetTokens()); n > 0 {
			s.emit(UsageDelta{Tokens: n})
		}
	case *agentpb.InteractionUpdate_TurnEnded:
		s.finish(s.defaultFinishReason())
		return true
	}
	return false
}

func (s *session) onToolCallStarted(u *agentpb.ToolCallStartedUpdate) {
	mcp := u.GetToolCall().GetMcpToolCall()
	if mcp == nil {
		return
	}
	id := u.GetCallId()
	if id == "" {
		id = mcp.GetArgs().GetToolCallId()
	}
	s.trackTool(id, mcpArgsToolName(mcp.GetArgs()))
}

func (s *session) onPartialToolCall(u *agentpb.PartialToolCallUpdate) {
	mcp := u.GetToolCall().GetMcpToolCall()
	if mcp == nil {
		return
	}
	id := u.GetCallId()
	if id == "" {
		id = mcp.GetArgs().GetToolCallId()
	}
	tc := s.trackTool(id, mcpArgsToolName(mcp.GetArgs()))
	// args_text_delta 是到目前为止的聚合文本，只透出新增部分。
	agg := u.GetArgsTextDelta()
	if len(agg) > tc.argsSoFar {
		s.emit(ToolCallArgsDelta{ID: id, Name: tc.name, Args: agg[tc.argsSoFar:]})
		tc.argsSoFar = len(agg)
	}
}

func (s *session) onToolCallCompleted(u *agentpb.ToolCallCompletedUpdate) {
	mcp := u.GetToolCall().GetMcpToolCall()
	if mcp == nil {
		return
	}
	id := u.GetCallId()
	if id == "" {
		id = mcp.GetArgs().GetToolCallId()
	}
	tc := s.trackTool(id, mcpArgsToolName(mcp.GetArgs()))
	if !tc.ended {
		tc.ended = true
		s.emit(ToolCallEnd{ID: id, Name: tc.name, ArgsJSON: decodeMcpArgsMap(mcp.GetArgs().GetArgs())})
	}
	s.sawToolCall = true
}

// decodeMcpArgsMap 把 McpArgs.args（每个值是序列化的 structpb.Value）还原成
// 参数对象的 JSON 文本。
func decodeMcpArgsMap(m map[string][]byte) []byte {
	obj := make(map[string]any, len(m))
	for k, raw := range m {
		var v structpb.Value
		if proto.Unmarshal(raw, &v) == nil {
			obj[k] = v.AsInterface()
		}
	}
	data, err := json.Marshal(obj)
	if err != nil {
		return []byte("{}")
	}
	return data
}
