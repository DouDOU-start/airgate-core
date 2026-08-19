package cursor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	agentpb "github.com/DouDOU-start/airgate-core/internal/relay/cursor/proto/agentpb"
)

func newSessionPipe() (*session, *bufio.Reader, func()) {
	pr, pw := io.Pipe()
	s := &session{
		stream:  &Stream{pw: pw},
		events:  make(chan Event, 8),
		started: make(map[string]*trackedCall),
	}
	return s, bufio.NewReader(pr), func() {
		_ = pw.Close()
		_ = pr.Close()
	}
}

func readClientMessage(t *testing.T, reader *bufio.Reader, action func()) *agentpb.AgentClientMessage {
	t.Helper()
	done := make(chan struct{})
	go func() {
		action()
		close(done)
	}()
	end, payload, err := ReadFrame(reader)
	if err != nil {
		t.Fatalf("读取客户端帧失败: %v", err)
	}
	if end {
		t.Fatal("客户端响应不应提前结束 Connect 流")
	}
	<-done
	var msg agentpb.AgentClientMessage
	if err := proto.Unmarshal(payload, &msg); err != nil {
		t.Fatalf("解析客户端消息失败: %v", err)
	}
	return &msg
}

func TestSessionHeartbeat发送顶层保活消息(t *testing.T) {
	s, reader, cleanup := newSessionPipe()
	defer cleanup()
	stop := make(chan struct{})
	done := make(chan struct{})
	go s.runHeartbeat(context.Background(), 50*time.Millisecond, stop, done)

	msg := readClientMessage(t, reader, func() {
		// 帧由 heartbeat goroutine 异步写出，读取动作本身只负责等待。
	})
	if msg.GetClientHeartbeat() == nil {
		t.Fatalf("应发送 ClientHeartbeat，实际消息=%T", msg.GetMessage())
	}
	close(stop)
	_ = s.stream.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("heartbeat goroutine 未及时退出")
	}
}

func TestSessionInteractionQuery发送确定答复(t *testing.T) {
	tests := []struct {
		name  string
		query func() *agentpb.InteractionQuery
		check func(t *testing.T, response *agentpb.InteractionResponse)
	}{
		{
			name: "网页搜索批准",
			query: func() *agentpb.InteractionQuery {
				return &agentpb.InteractionQuery{Query: &agentpb.InteractionQuery_WebSearchRequestQuery{
					WebSearchRequestQuery: &agentpb.WebSearchRequestQuery{},
				}}
			},
			check: func(t *testing.T, response *agentpb.InteractionResponse) {
				if response.GetWebSearchRequestResponse().GetApproved() == nil {
					t.Fatal("web search 应返回 approved")
				}
			},
		},
		{
			name: "交互提问拒绝",
			query: func() *agentpb.InteractionQuery {
				return &agentpb.InteractionQuery{Query: &agentpb.InteractionQuery_AskQuestionInteractionQuery{
					AskQuestionInteractionQuery: &agentpb.AskQuestionInteractionQuery{},
				}}
			},
			check: func(t *testing.T, response *agentpb.InteractionResponse) {
				rejected := response.GetAskQuestionInteractionResponse().GetResult().GetRejected()
				if rejected == nil || rejected.GetReason() == "" {
					t.Fatal("ask question 应返回 rejected")
				}
			},
		},
		{
			name: "模式切换拒绝",
			query: func() *agentpb.InteractionQuery {
				return &agentpb.InteractionQuery{Query: &agentpb.InteractionQuery_SwitchModeRequestQuery{
					SwitchModeRequestQuery: &agentpb.SwitchModeRequestQuery{},
				}}
			},
			check: func(t *testing.T, response *agentpb.InteractionResponse) {
				if response.GetSwitchModeRequestResponse().GetRejected() == nil {
					t.Fatal("switch mode 应返回 rejected")
				}
			},
		},
		{
			name: "创建计划错误",
			query: func() *agentpb.InteractionQuery {
				return &agentpb.InteractionQuery{Query: &agentpb.InteractionQuery_CreatePlanRequestQuery{
					CreatePlanRequestQuery: &agentpb.CreatePlanRequestQuery{},
				}}
			},
			check: func(t *testing.T, response *agentpb.InteractionResponse) {
				errResult := response.GetCreatePlanRequestResponse().GetResult().GetError()
				if errResult == nil || errResult.GetError() == "" {
					t.Fatal("create plan 应返回 error")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, reader, cleanup := newSessionPipe()
			defer cleanup()
			query := tc.query()
			query.Id = 42
			msg := readClientMessage(t, reader, func() {
				if stop := s.handleInteractionQuery(query); stop {
					t.Fatal("普通 interaction query 不应停止会话")
				}
			})
			response := msg.GetInteractionResponse()
			if response == nil || response.GetId() != 42 {
				t.Fatalf("interaction response 不完整: %+v", response)
			}
			tc.check(t, response)
		})
	}
}

func TestSessionInteractionQuery未知字段返回同字段批准(t *testing.T) {
	s, reader, cleanup := newSessionPipe()
	defer cleanup()
	query := &agentpb.InteractionQuery{Id: 9}
	unknown := protowire.AppendTag(nil, 9, protowire.BytesType)
	unknown = protowire.AppendBytes(unknown, []byte{0x0a, 0x00})
	query.ProtoReflect().SetUnknown(unknown)

	msg := readClientMessage(t, reader, func() {
		if s.handleInteractionQuery(query) {
			t.Fatal("未知权限门应通过同字段 approved 继续")
		}
	})
	response := msg.GetInteractionResponse()
	if response == nil || response.GetId() != 9 {
		t.Fatalf("未知 query response 不完整: %+v", response)
	}
	got := response.ProtoReflect().GetUnknown()
	number, wireType, n := protowire.ConsumeTag(got)
	if n < 0 || number != 9 || wireType != protowire.BytesType {
		t.Fatalf("响应未保留未知字段 9: %v", got)
	}
	value, valueLen := protowire.ConsumeBytes(got[n:])
	if valueLen < 0 {
		t.Fatalf("响应未知字段 9 的值无法解析: %v", got)
	}
	approvedField, approvedType, approvedLen := protowire.ConsumeTag(value)
	if approvedLen < 0 || approvedField != 1 || approvedType != protowire.BytesType {
		t.Fatalf("响应未知字段 9 未编码 approved{}: %v", value)
	}
}

func TestSessionExec回复保留ExecID(t *testing.T) {
	s, reader, cleanup := newSessionPipe()
	defer cleanup()
	ex := &agentpb.ExecServerMessage{
		Id:     3,
		ExecId: "attach-3",
		Message: &agentpb.ExecServerMessage_RequestContextArgs{
			RequestContextArgs: &agentpb.RequestContextArgs{},
		},
	}
	msg := readClientMessage(t, reader, func() { s.handleExec(ex) })
	exec := msg.GetExecClientMessage()
	if exec == nil || exec.GetId() != 3 || exec.GetExecId() != "attach-3" || exec.GetRequestContextResult() == nil {
		t.Fatalf("exec 回复不完整: %+v", exec)
	}
}

func TestSession未知Exec发送Throw和StreamClose(t *testing.T) {
	s, reader, cleanup := newSessionPipe()
	defer cleanup()
	ex := &agentpb.ExecServerMessage{Id: 7}
	done := make(chan struct{})
	go func() {
		s.handleExec(ex)
		close(done)
	}()
	for i := 0; i < 2; i++ {
		end, payload, err := ReadFrame(reader)
		if err != nil || end {
			t.Fatalf("读取第 %d 个 exec 控制帧失败: end=%v err=%v", i, end, err)
		}
		var msg agentpb.AgentClientMessage
		if err := proto.Unmarshal(payload, &msg); err != nil {
			t.Fatalf("解析第 %d 个控制帧失败: %v", i, err)
		}
		control := msg.GetExecClientControlMessage()
		if control == nil {
			t.Fatalf("第 %d 个帧不是 exec control: %s", i, msg.String())
		}
		if i == 0 && (control.GetThrow() == nil || control.GetThrow().GetId() != 7) {
			t.Fatalf("第一个控制帧应为 throw: %+v", control)
		}
		if i == 1 && (control.GetStreamClose() == nil || control.GetStreamClose().GetId() != 7) {
			t.Fatalf("第二个控制帧应为 stream_close: %+v", control)
		}
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("未知 exec 处理未结束")
	}
}

func TestSession工具调用使用短静默窗口收尾(t *testing.T) {
	s, reader, cleanup := newSessionPipe()
	defer cleanup()
	s.toolCallDrainWindow = 20 * time.Millisecond
	started := time.Now()
	s.onMcpExec(&agentpb.ExecServerMessage{Id: 1}, &agentpb.McpArgs{ToolCallId: "call-1", Name: "fn_read"})
	_, _, err := ReadFrame(reader)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("工具调用窗口结束后应关闭发送方向，得到 %v", err)
	}
	if elapsed := time.Since(started); elapsed < 15*time.Millisecond || elapsed > 300*time.Millisecond {
		t.Fatalf("工具调用收尾耗时异常: %v", elapsed)
	}
}

func TestSessionCheckpoint回调收到克隆快照(t *testing.T) {
	var got *agentpb.ConversationStateStructure
	s := &session{
		events:       make(chan Event, 1),
		onCheckpoint: func(checkpoint *agentpb.ConversationStateStructure) { got = checkpoint },
	}
	checkpoint := &agentpb.ConversationStateStructure{
		Summary: []byte("summary"),
	}
	s.handle(&agentpb.AgentServerMessage{Message: &agentpb.AgentServerMessage_ConversationCheckpointUpdate{
		ConversationCheckpointUpdate: checkpoint,
	}})
	if got == nil || string(got.GetSummary()) != "summary" {
		t.Fatalf("未收到 checkpoint 回调: %+v", got)
	}
	checkpoint.Summary[0] = 'X'
	if string(got.GetSummary()) != "summary" {
		t.Fatal("checkpoint 回调不应与 protobuf 原对象共享可变字节")
	}
}

func TestRunSession工具结果续接原始Cursor流(t *testing.T) {
	serverResult := make(chan *agentpb.ExecClientMessage, 1)
	serverError := make(chan error, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reportError := func(format string, args ...any) {
			select {
			case serverError <- fmt.Errorf(format, args...):
			default:
			}
		}
		if r.URL.Path != runPath {
			http.Error(w, "路径错误", http.StatusNotFound)
			return
		}
		reader := bufio.NewReader(r.Body)
		if _, _, err := ReadFrame(reader); err != nil {
			reportError("服务端读取 run request 失败: %v", err)
			return
		}
		w.Header().Set("content-type", "application/connect+proto")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		flusher.Flush()
		writeServerMessage := func(message *agentpb.AgentServerMessage) bool {
			payload, err := proto.Marshal(message)
			if err != nil {
				reportError("序列化服务端消息失败: %v", err)
				return false
			}
			if _, err := w.Write(FrameMessage(payload, false)); err != nil {
				reportError("写入服务端消息失败: %v", err)
				return false
			}
			flusher.Flush()
			return true
		}
		if !writeServerMessage(&agentpb.AgentServerMessage{Message: &agentpb.AgentServerMessage_ExecServerMessage{
			ExecServerMessage: &agentpb.ExecServerMessage{
				Id: 31, ExecId: "exec-31",
				Message: &agentpb.ExecServerMessage_McpArgs{McpArgs: &agentpb.McpArgs{
					ToolCallId: "call-31", Name: "fn_read", ToolName: "fn_read",
				}},
			},
		}}) {
			return
		}

		_, payload, err := ReadFrame(reader)
		if err != nil {
			reportError("服务端读取 MCP result 失败: %v", err)
			return
		}
		var clientMessage agentpb.AgentClientMessage
		if err := proto.Unmarshal(payload, &clientMessage); err != nil {
			reportError("解析 MCP result 失败: %v", err)
			return
		}
		serverResult <- clientMessage.GetExecClientMessage()

		if !writeServerMessage(&agentpb.AgentServerMessage{Message: &agentpb.AgentServerMessage_InteractionUpdate{
			InteractionUpdate: &agentpb.InteractionUpdate{Message: &agentpb.InteractionUpdate_TextDelta{
				TextDelta: &agentpb.TextDeltaUpdate{Text: "续接成功"},
			}},
		}}) {
			return
		}
		writeServerMessage(&agentpb.AgentServerMessage{Message: &agentpb.AgentServerMessage_InteractionUpdate{
			InteractionUpdate: &agentpb.InteractionUpdate{Message: &agentpb.InteractionUpdate_TurnEnded{
				TurnEnded: &agentpb.TurnEndedUpdate{},
			}},
		}})
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	sessionContext, cancelSession := context.WithCancel(context.Background())
	defer cancelSession()
	firstContext, cancelFirst := context.WithCancel(context.Background())
	resumable := newResumableCursorStream(cancelSession)
	client := NewClient(server.URL, "test")
	events := RunSession(sessionContext, SessionOptions{
		Client: client,
		Run: RunOptions{
			AccessToken: "test-token",
			Transport:   server.Client().Transport,
		},
		RequestBytes:        []byte("run-request"),
		Store:               NewBlobStore(),
		HeartbeatInterval:   time.Hour,
		ToolCallDrainWindow: 10 * time.Millisecond,
		PausedStreamTTL:     time.Second,
		Resumable:           resumable,
		DownstreamContext:   firstContext,
	})

	var sawToolCall, sawFirstDone bool
	for event := range events {
		switch value := event.(type) {
		case ToolCallEnd:
			sawToolCall = value.ID == "call-31" && value.Name == "read"
		case Done:
			sawFirstDone = value.FinishReason == "tool_calls"
		case ErrEvent:
			t.Fatalf("首段响应异常: %v", value.Err)
		}
	}
	if !sawToolCall || !sawFirstDone {
		t.Fatalf("首段事件不完整: tool_call=%v done=%v", sawToolCall, sawFirstDone)
	}
	// 模拟 HTTP handler 在首段 SSE 结束后取消请求 context；原始 Cursor 流
	// 此时已经进入等待态，不应被该取消动作关闭。
	cancelFirst()

	secondContext, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	secondEvents, active, err := resumable.tryResume(secondContext, &ParsedRequest{Messages: []NMessage{{
		Role: "tool", ToolCallID: "call-31", Content: []ContentPart{{Type: "text", Text: "文件内容"}},
	}}})
	if err != nil || !active {
		t.Fatalf("续接原始流失败: active=%v err=%v", active, err)
	}

	select {
	case result := <-serverResult:
		if result == nil || result.GetId() != 31 || result.GetExecId() != "exec-31" {
			t.Fatalf("服务端收到的结果关联字段错误: %+v", result)
		}
		content := result.GetMcpResult().GetSuccess().GetContent()
		if len(content) != 1 || content[0].GetText().GetText() != "文件内容" {
			t.Fatalf("服务端收到的 MCP 内容错误: %+v", content)
		}
	case <-time.After(time.Second):
		t.Fatal("服务端未收到续接工具结果")
	case err := <-serverError:
		t.Fatal(err)
	}

	var sawReady, sawText, sawSecondDone bool
	for event := range secondEvents {
		switch value := event.(type) {
		case Ready:
			sawReady = true
		case TextDelta:
			sawText = value.Text == "续接成功"
		case Done:
			sawSecondDone = value.FinishReason == "stop"
		case ErrEvent:
			t.Fatalf("续接段响应异常: %v", value.Err)
		}
	}
	if !sawReady || !sawText || !sawSecondDone {
		t.Fatalf("续接段事件不完整: ready=%v text=%v done=%v", sawReady, sawText, sawSecondDone)
	}
	select {
	case err := <-serverError:
		t.Fatal(err)
	default:
	}
}
