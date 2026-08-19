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

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	agentpb "github.com/DouDOU-start/airgate-core/internal/relay/cursor/proto/agentpb"
)

const (
	// execRejectReason 是对服务端发起的本地工具执行请求的统一拒绝话术：
	// 网关模式下没有本地文件系统/shell，引导模型只使用下发的函数工具。
	execRejectReason = "API 网关模式不提供本地文件系统或 shell；请仅使用请求中提供的函数工具，或直接返回文本。"

	// interactionRejectReason 用于无法在无交互网关中完成的询问。
	interactionRejectReason = "API 网关模式不支持交互式确认。"

	// defaultClientHeartbeatInterval 与 Cursor CLI 的保活频率保持一致，避免
	// 长思考或等待工具期间 HTTP/2 双向流被中间网络设备判定为空闲。
	defaultClientHeartbeatInterval = 5 * time.Second

	// defaultToolCallDrainWindow 是最后一个 MCP 工具调用后的静默窗口。Cursor
	// 会连续下发同一批并行调用，无需每轮固定多等 400ms；100ms 在保留并行
	// 调用收集余量的同时，可直接减少常见单工具轮次约 300ms 的额外等待。
	defaultToolCallDrainWindow = 100 * time.Millisecond

	// 暂停的原始 Cursor 流最多等待下游工具结果 10 分钟；超时后关闭流，
	// 后续请求仍可通过 checkpoint 重建。
	defaultPausedStreamTTL = 10 * time.Minute
)

// SessionOptions 描述一次 Run 会话所需的全部输入。
type SessionOptions struct {
	Client              *Client
	Run                 RunOptions
	RequestBytes        []byte
	Tools               []*agentpb.McpToolDefinition
	Store               BlobStore
	HeartbeatInterval   time.Duration
	ToolCallDrainWindow time.Duration
	PausedStreamTTL     time.Duration
	// Resumable 非空时工具调用不会关闭 Cursor 原始流，而是分段结束当前
	// 响应并等待下一次请求写回 McpResult。
	Resumable *resumableCursorStream
	// DownstreamContext 只控制当前响应阶段；进入工具等待态后，其结束不再
	// 关闭原始流。未设置时沿用 RunSession 的 ctx。
	DownstreamContext context.Context
	// OnCheckpoint 接收 Cursor 服务端发来的最新会话快照。回调参数已克隆，
	// 调用方可持久化或继续克隆，不会与 protobuf 解码缓冲共享可变内存。
	OnCheckpoint func(*agentpb.ConversationStateStructure)
	// OnDone 在会话 goroutine 的所有退出路径执行一次，用于释放跨请求 lease。
	OnDone func()
}

// RunSession 打开 Connect 流并驱动完整会话，事件按序写入返回的 channel，
// 结束时（Done 或 ErrEvent 之后）关闭 channel。
func RunSession(ctx context.Context, opts SessionOptions) <-chan Event {
	events := make(chan Event, 64)
	go func() {
		if opts.OnDone != nil {
			defer opts.OnDone()
		}
		s := &session{
			store:               opts.Store,
			tools:               opts.Tools,
			events:              events,
			onCheckpoint:        opts.OnCheckpoint,
			started:             make(map[string]*trackedCall),
			pendingMCP:          make(map[string]pendingMCPCall),
			toolCallDrainWindow: durationOrDefault(opts.ToolCallDrainWindow, defaultToolCallDrainWindow),
			pausedStreamTTL:     durationOrDefault(opts.PausedStreamTTL, defaultPausedStreamTTL),
			resumable:           opts.Resumable,
			segmentDone:         make(chan struct{}),
			finishReady:         make(chan uint64, 16),
		}
		s.run(ctx, opts)
		s.closeSegment()
	}()
	return events
}

type trackedCall struct {
	name      string
	argsSoFar int  // 已透出的聚合参数文本长度
	ended     bool // 已 emit ToolCallEnd
}

type session struct {
	stream    *Stream
	store     BlobStore
	tools     []*agentpb.McpToolDefinition
	events    chan Event
	startedAt time.Time

	sendMu sync.Mutex

	started       map[string]*trackedCall
	sawToolCall   bool
	finishing     bool
	finishTimer   *time.Timer
	doneSent      bool
	segmentClosed bool
	pendingMCP    map[string]pendingMCPCall
	finishReady   chan uint64
	finishBatch   uint64
	resumable     *resumableCursorStream
	segmentDone   chan struct{}

	toolCallDrainWindow time.Duration
	pausedStreamTTL     time.Duration
	onCheckpoint        func(*agentpb.ConversationStateStructure)
	readyLogged         bool
	contentLogged       bool
	toolLogged          bool
}

func (s *session) run(ctx context.Context, opts SessionOptions) {
	s.startedAt = time.Now()
	stream, err := opts.Client.OpenRun(ctx, opts.Run)
	if err != nil {
		s.emit(ErrEvent{Err: err})
		return
	}
	s.stream = stream
	defer func() {
		s.stopSegmentWatcher()
		if s.finishTimer != nil {
			s.finishTimer.Stop()
		}
		_ = stream.Close()
	}()

	if err := stream.Send(opts.RequestBytes, false); err != nil {
		s.emit(ErrEvent{Err: err})
		return
	}
	heartbeatStop := make(chan struct{})
	heartbeatDone := make(chan struct{})
	go s.runHeartbeat(ctx, durationOrDefault(opts.HeartbeatInterval, defaultClientHeartbeatInterval), heartbeatStop, heartbeatDone)
	defer func() {
		close(heartbeatStop)
		// 心跳可能正阻塞在 HTTP 请求体写入；先关闭流以解除写阻塞，再等待
		// goroutine 退出，避免请求结束阶段泄漏或卡死。
		_ = stream.Close()
		<-heartbeatDone
	}()
	downstreamContext := opts.DownstreamContext
	if downstreamContext == nil {
		downstreamContext = ctx
	}
	s.watchDownstream(downstreamContext)

	ready := false
	type receiveResult struct {
		end     bool
		payload []byte
		err     error
	}
	receiveCh := make(chan receiveResult, 1)
	receiveStop := make(chan struct{})
	receiveDone := make(chan struct{})
	go func() {
		defer close(receiveDone)
		for {
			end, payload, recvErr := stream.Recv()
			select {
			case receiveCh <- receiveResult{end: end, payload: payload, err: recvErr}:
			case <-receiveStop:
				return
			}
			if recvErr != nil || end {
				return
			}
		}
	}()
	defer func() {
		close(receiveStop)
		_ = stream.Close()
		<-receiveDone
	}()
	for {
		var end bool
		var payload []byte
		var err error
		var received receiveResult
		// 收集窗口到期与新帧同时就绪时优先处理帧，确保已经抵达的并行
		// 工具调用进入同一批次；否则 select 的随机选择会偶发漏掉最后一项。
		select {
		case received = <-receiveCh:
		default:
			select {
			case received = <-receiveCh:
			case batch := <-s.finishReady:
				if batch != s.finishBatch {
					continue
				}
				if s.resumable != nil {
					if s.pauseAndResume(ctx) {
						continue
					}
					return
				}
				_ = s.stream.Close()
				s.finish("tool_calls")
				return
			}
		}
		end, payload, err = received.end, received.payload, received.err
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
		if !ready {
			ready = true
			s.emit(Ready{})
			s.logPhaseOnce(&s.readyLogged, "ready")
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

func (s *session) watchDownstream(ctx context.Context) {
	if ctx == nil || s.stream == nil {
		return
	}
	done := s.segmentDone
	stream := s.stream
	go func() {
		select {
		case <-ctx.Done():
			// 分段正常结束时先关闭 done、再关闭事件通道。若请求上下文随后
			// 取消，两者可能同时可读；这里二次确认，避免误关等待工具结果的流。
			select {
			case <-done:
				return
			default:
			}
			_ = stream.Close()
		case <-done:
		}
	}()
}

func (s *session) stopSegmentWatcher() {
	if s.segmentDone == nil {
		return
	}
	close(s.segmentDone)
	s.segmentDone = nil
}

func (s *session) closeSegment() {
	if s.segmentClosed || s.events == nil {
		return
	}
	close(s.events)
	s.segmentClosed = true
}

func (s *session) pauseAndResume(ctx context.Context) bool {
	if s.resumable == nil || len(s.pendingMCP) == 0 {
		_ = s.stream.Close()
		return false
	}
	s.stopSegmentWatcher()
	s.resumable.setWaiting(s.pendingMCP)
	s.finish("tool_calls")
	s.closeSegment()

	timer := time.NewTimer(durationOrDefault(s.pausedStreamTTL, defaultPausedStreamTTL))
	defer timer.Stop()
	select {
	case command := <-s.resumable.resumeCh:
		s.events = command.events
		s.segmentClosed = false
		s.segmentDone = make(chan struct{})
		s.started = make(map[string]*trackedCall)
		s.pendingMCP = make(map[string]pendingMCPCall)
		s.sawToolCall = false
		s.finishing = false
		s.doneSent = false
		s.readyLogged = false
		s.contentLogged = false
		s.toolLogged = false
		s.watchDownstream(command.downstreamContext)
		for _, result := range command.results {
			if err := s.replyExec(result); err != nil {
				command.ready <- fmt.Errorf("写回 Cursor 工具结果失败: %w", err)
				return false
			}
		}
		command.ready <- nil
		s.emit(Ready{})
		return true
	case <-ctx.Done():
		return false
	case <-timer.C:
		slog.Debug("cursor_session_resume_timeout")
		_ = s.stream.Close()
		return false
	}
}

func (s *session) logPhaseOnce(done *bool, phase string) {
	if *done {
		return
	}
	*done = true
	fields := []any{"phase", phase}
	if !s.startedAt.IsZero() {
		fields = append(fields, "elapsed_ms", time.Since(s.startedAt).Milliseconds())
	}
	slog.Debug("cursor_session_phase", fields...)
}

func durationOrDefault(value, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return value
}

func (s *session) runHeartbeat(ctx context.Context, interval time.Duration, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			_ = s.send(&agentpb.AgentClientMessage{Message: &agentpb.AgentClientMessage_ClientHeartbeat{
				ClientHeartbeat: &agentpb.ClientHeartbeat{},
			}})
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

func (s *session) send(msg *agentpb.AgentClientMessage) error {
	payload, err := proto.Marshal(msg)
	if err != nil {
		slog.Warn("cursor_session_client_message_marshal_failed", "error", err)
		return err
	}
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	if err := s.stream.Send(payload, false); err != nil {
		slog.Debug("cursor_session_client_message_send_failed", "error", err)
		return err
	}
	return nil
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
		return s.handleInteractionQuery(m.InteractionQuery)
	case *agentpb.AgentServerMessage_ConversationCheckpointUpdate:
		// token_details.used_tokens 是上游真实上下文口径，但包含 Cursor 注入
		// 的约 25K 平台系统提示，直接用于下游计费会把小请求的输入抬到数万，
		// 故仍只记日志；完整 checkpoint 则用于同一粘性会话的后续请求。
		if m.ConversationCheckpointUpdate != nil {
			if n := int(m.ConversationCheckpointUpdate.GetTokenDetails().GetUsedTokens()); n > 0 {
				slog.Debug("cursor_session_context_tokens", "used_tokens", n)
			}
		}
		if s.onCheckpoint != nil && m.ConversationCheckpointUpdate != nil {
			checkpoint := proto.Clone(m.ConversationCheckpointUpdate).(*agentpb.ConversationStateStructure)
			s.onCheckpoint(checkpoint)
		}
	default:
		slog.Warn("cursor_session_unhandled_message", "type", serverMessageKind(sm))
	}
	return false
}

// handleInteractionQuery 为 Cursor 的交互式权限门发送确定答复。托管搜索无需
// 访问网关本机资源，可直接批准；需要真人选择或本地文件的操作明确拒绝，避免
// 服务端一直等待 interaction_response。
func (s *session) handleInteractionQuery(query *agentpb.InteractionQuery) bool {
	if query == nil {
		return false
	}
	response := &agentpb.InteractionResponse{Id: query.GetId()}
	switch query.Query.(type) {
	case *agentpb.InteractionQuery_WebSearchRequestQuery:
		response.Result = &agentpb.InteractionResponse_WebSearchRequestResponse{
			WebSearchRequestResponse: &agentpb.WebSearchRequestResponse{Result: &agentpb.WebSearchRequestResponse_Approved{
				Approved: &agentpb.WebSearchRequestResponse_ApprovedMsg{},
			}},
		}
	case *agentpb.InteractionQuery_ExaSearchRequestQuery:
		response.Result = &agentpb.InteractionResponse_ExaSearchRequestResponse{
			ExaSearchRequestResponse: &agentpb.ExaSearchRequestResponse{Result: &agentpb.ExaSearchRequestResponse_Approved{
				Approved: &agentpb.ExaSearchRequestResponse_ApprovedMsg{},
			}},
		}
	case *agentpb.InteractionQuery_ExaFetchRequestQuery:
		response.Result = &agentpb.InteractionResponse_ExaFetchRequestResponse{
			ExaFetchRequestResponse: &agentpb.ExaFetchRequestResponse{Result: &agentpb.ExaFetchRequestResponse_Approved{
				Approved: &agentpb.ExaFetchRequestResponse_ApprovedMsg{},
			}},
		}
	case *agentpb.InteractionQuery_AskQuestionInteractionQuery:
		response.Result = &agentpb.InteractionResponse_AskQuestionInteractionResponse{
			AskQuestionInteractionResponse: &agentpb.AskQuestionInteractionResponse{Result: &agentpb.AskQuestionResult{
				Result: &agentpb.AskQuestionResult_Rejected{Rejected: &agentpb.AskQuestionRejected{Reason: interactionRejectReason}},
			}},
		}
	case *agentpb.InteractionQuery_SwitchModeRequestQuery:
		response.Result = &agentpb.InteractionResponse_SwitchModeRequestResponse{
			SwitchModeRequestResponse: &agentpb.SwitchModeRequestResponse{Result: &agentpb.SwitchModeRequestResponse_Rejected{
				Rejected: &agentpb.SwitchModeRequestResponse_RejectedMsg{Reason: interactionRejectReason},
			}},
		}
	case *agentpb.InteractionQuery_CreatePlanRequestQuery:
		response.Result = &agentpb.InteractionResponse_CreatePlanRequestResponse{
			CreatePlanRequestResponse: &agentpb.CreatePlanRequestResponse{Result: &agentpb.CreatePlanResult{
				Result: &agentpb.CreatePlanResult_Error{Error: &agentpb.CreatePlanError{Error: interactionRejectReason}},
			}},
		}
	case *agentpb.InteractionQuery_SetupVmEnvironmentArgs:
		err := fmt.Errorf("cursor 请求建立本地 VM 环境，但 API 网关不提供该能力")
		s.emit(ErrEvent{Err: err})
		_ = s.stream.Close()
		return true
	default:
		field, ok := unknownInteractionQueryField(query)
		if !ok {
			err := fmt.Errorf("cursor 返回了无法识别的交互询问")
			s.emit(ErrEvent{Err: err})
			_ = s.stream.Close()
			return true
		}
		// 新版 Cursor 偶尔会先于本地 proto 增加权限门（例如托管 WebFetch）。
		// 沿用同字段号返回 approved{}，既保持前向兼容，也不会授权访问网关本机。
		unknown := protowire.AppendTag(nil, field, protowire.BytesType)
		unknown = protowire.AppendBytes(unknown, []byte{0x0a, 0x00})
		response.ProtoReflect().SetUnknown(unknown)
		slog.Warn("cursor_session_unknown_query_approved", "field", field)
	}
	_ = s.send(&agentpb.AgentClientMessage{Message: &agentpb.AgentClientMessage_InteractionResponse{
		InteractionResponse: response,
	}})
	return false
}

func unknownInteractionQueryField(query *agentpb.InteractionQuery) (protowire.Number, bool) {
	raw := query.ProtoReflect().GetUnknown()
	for len(raw) > 0 {
		number, wireType, tagLen := protowire.ConsumeTag(raw)
		if tagLen < 0 {
			return 0, false
		}
		raw = raw[tagLen:]
		valueLen := protowire.ConsumeFieldValue(number, wireType, raw)
		if valueLen < 0 {
			return 0, false
		}
		if number >= 2 && wireType == protowire.BytesType {
			return number, true
		}
		raw = raw[valueLen:]
	}
	return 0, false
}

// ---- KV：服务端按需索取/写回 blob ----

func (s *session) handleKv(kv *agentpb.KvServerMessage) {
	switch m := kv.Message.(type) {
	case *agentpb.KvServerMessage_GetBlobArgs:
		data, _ := s.store.Get(m.GetBlobArgs.GetBlobId())
		_ = s.send(&agentpb.AgentClientMessage{Message: &agentpb.AgentClientMessage_KvClientMessage{
			KvClientMessage: &agentpb.KvClientMessage{
				Id: kv.GetId(),
				Message: &agentpb.KvClientMessage_GetBlobResult{
					GetBlobResult: &agentpb.GetBlobResult{BlobData: data},
				},
			},
		}})
	case *agentpb.KvServerMessage_SetBlobArgs:
		s.store.Set(m.SetBlobArgs.GetBlobId(), m.SetBlobArgs.GetBlobData())
		_ = s.send(&agentpb.AgentClientMessage{Message: &agentpb.AgentClientMessage_KvClientMessage{
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
		msg := &agentpb.ExecClientMessage{Id: execID, ExecId: ex.GetExecId()}
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
		_ = s.replyExec(msg)
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
		s.onMcpExec(ex, m.McpArgs)
	case *agentpb.ExecServerMessage_ShellArgs:
		reply(rejectedShellResult())
	case *agentpb.ExecServerMessage_ShellStreamArgs:
		// 流式 shell 的应答通道是 ShellStream 事件，不是 ShellResult；回错
		// 类型服务端会永远等待，挂死整条流（实测 Claude Code 场景必现）。
		_ = s.replyExec(&agentpb.ExecClientMessage{
			Id:     execID,
			ExecId: ex.GetExecId(),
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
		// 未实现的 exec 必须在控制通道内失败并关闭对应子流；静默丢弃会让
		// Cursor 服务端永久等待一个永远不会到来的 typed result。
		s.replyExecFailure(ex, fmt.Sprintf("API 网关不支持 Cursor exec 类型 %T", ex.Message))
	}
}

func rejectedShellResult() *agentpb.ShellResult {
	return &agentpb.ShellResult{
		Result: &agentpb.ShellResult_Rejected{Rejected: &agentpb.ShellRejected{Reason: execRejectReason}},
	}
}

func (s *session) replyExec(msg *agentpb.ExecClientMessage) error {
	return s.send(&agentpb.AgentClientMessage{Message: &agentpb.AgentClientMessage_ExecClientMessage{
		ExecClientMessage: msg,
	}})
}

func (s *session) replyExecFailure(ex *agentpb.ExecServerMessage, reason string) {
	errorCode := "UNSUPPORTED_EXEC"
	_ = s.send(&agentpb.AgentClientMessage{Message: &agentpb.AgentClientMessage_ExecClientControlMessage{
		ExecClientControlMessage: &agentpb.ExecClientControlMessage{Message: &agentpb.ExecClientControlMessage_Throw{
			Throw: &agentpb.ExecClientThrow{Id: ex.GetId(), Error: reason, ErrorCode: &errorCode},
		}},
	}})
	_ = s.send(&agentpb.AgentClientMessage{Message: &agentpb.AgentClientMessage_ExecClientControlMessage{
		ExecClientControlMessage: &agentpb.ExecClientControlMessage{Message: &agentpb.ExecClientControlMessage_StreamClose{
			StreamClose: &agentpb.ExecClientStreamClose{Id: ex.GetId()},
		}},
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
// 不真正执行，而是把完整参数透出为 tool_calls。收集完同批并行调用后结束当前
// 下游分段；可续接会话保持原始流，等待 API 调用方在下一请求中携带结果。
func (s *session) onMcpExec(ex *agentpb.ExecServerMessage, args *agentpb.McpArgs) {
	s.logPhaseOnce(&s.toolLogged, "tool_call")
	id := args.GetToolCallId()
	if ex != nil && id != "" {
		if s.pendingMCP == nil {
			s.pendingMCP = make(map[string]pendingMCPCall)
		}
		s.pendingMCP[id] = pendingMCPCall{
			toolCallID: id,
			name:       mcpArgsToolName(args),
			messageID:  ex.GetId(),
			execID:     ex.GetExecId(),
		}
	}
	tc := s.trackTool(id, mcpArgsToolName(args))
	if !tc.ended {
		tc.ended = true
		argsJSON := decodeMcpArgsMap(args.GetArgs())
		s.emit(ToolCallEnd{ID: id, Name: tc.name, ArgsJSON: argsJSON})
	}
	s.sawToolCall = true
	s.finishing = true
	s.finishBatch++
	batch := s.finishBatch
	if s.finishTimer != nil {
		s.finishTimer.Stop()
	}
	window := durationOrDefault(s.toolCallDrainWindow, defaultToolCallDrainWindow)
	s.finishTimer = time.AfterFunc(window, func() {
		if s.finishReady == nil {
			_ = s.stream.Close()
			return
		}
		select {
		case s.finishReady <- batch:
		default:
			slog.Warn("cursor_session_tool_finish_signal_dropped", "batch", batch)
		}
	})
}

// ---- 生成增量 ----

func (s *session) handleUpdate(u *agentpb.InteractionUpdate) (stop bool) {
	switch m := u.Message.(type) {
	case *agentpb.InteractionUpdate_TextDelta:
		if t := m.TextDelta.GetText(); t != "" {
			s.logPhaseOnce(&s.contentLogged, "content")
			s.emit(TextDelta{Text: t})
		}
	case *agentpb.InteractionUpdate_ThinkingDelta:
		if t := m.ThinkingDelta.GetText(); t != "" {
			s.logPhaseOnce(&s.contentLogged, "thinking")
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
			slog.Debug("cursor_session_token_delta", "tokens", n)
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
