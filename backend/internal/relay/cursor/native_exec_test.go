package cursor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	agentpb "github.com/DouDOU-start/airgate-core/internal/relay/cursor/proto/agentpb"
)

func claudeCodeFileTools(t *testing.T) []*agentpb.McpToolDefinition {
	t.Helper()
	definitions, err := buildToolDefinitions([]NToolDef{
		{Name: "Read", Schema: json.RawMessage(`{"type":"object","properties":{"file_path":{"type":"string"},"offset":{"type":"integer"},"limit":{"type":"integer"}},"required":["file_path"]}`)},
		{Name: "Write", Schema: json.RawMessage(`{"type":"object","properties":{"file_path":{"type":"string"},"content":{"type":"string"}},"required":["file_path","content"]}`)},
		{Name: "Bash", Schema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"},"timeout":{"type":"integer"},"description":{"type":"string"}},"required":["command"]}`)},
		{Name: "Grep", Schema: json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string"},"path":{"type":"string"},"glob":{"type":"string"},"output_mode":{"type":"string"},"-i":{"type":"boolean"},"-C":{"type":"integer"},"head_limit":{"type":"integer"}},"required":["pattern"]}`)},
		{Name: "Glob", Schema: json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string"},"path":{"type":"string"}},"required":["pattern"]}`)},
		{Name: "Edit", Schema: json.RawMessage(`{"type":"object","properties":{"file_path":{"type":"string"},"old_string":{"type":"string"},"new_string":{"type":"string"}},"required":["file_path","old_string","new_string"]}`)},
		{Name: "MultiEdit", Schema: json.RawMessage(`{"type":"object","properties":{"file_path":{"type":"string"},"edits":{"type":"array"}},"required":["file_path","edits"]}`)},
		{Name: "WebFetch", Schema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"},"prompt":{"type":"string"}},"required":["url","prompt"]}`)},
		{Name: "Task", Schema: json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"},"description":{"type":"string"},"subagent_type":{"type":"string"},"run_in_background":{"type":"boolean"}},"required":["prompt","description","subagent_type"]}`)},
	})
	if err != nil {
		t.Fatalf("构造 Claude Code 工具定义失败: %v", err)
	}
	return definitions
}

func TestSession原生Exec转换为ClaudeCode工具(t *testing.T) {
	s := &session{
		tools:               claudeCodeFileTools(t),
		events:              make(chan Event, 16),
		started:             make(map[string]*trackedCall),
		pendingExec:         make(map[string]pendingExecCall),
		toolCallDrainWindow: time.Hour,
	}
	offset := int32(3)
	limit := uint32(20)
	s.handleExec(&agentpb.ExecServerMessage{Id: 1, ExecId: "read-exec", Message: &agentpb.ExecServerMessage_ReadArgs{
		ReadArgs: &agentpb.ReadArgs{Path: "/tmp/a.txt", ToolCallId: "read-call", Offset: &offset, Limit: &limit},
	}})
	s.handleExec(&agentpb.ExecServerMessage{Id: 2, ExecId: "write-exec", Message: &agentpb.ExecServerMessage_WriteArgs{
		WriteArgs: &agentpb.WriteArgs{Path: "/tmp/a.txt", FileText: "hello\n", ToolCallId: "write-call"},
	}})
	description := "查看目录"
	s.handleExec(&agentpb.ExecServerMessage{Id: 3, ExecId: "shell-exec", Message: &agentpb.ExecServerMessage_ShellArgs{
		ShellArgs: &agentpb.ShellArgs{Command: "pwd", ToolCallId: "shell-call", Timeout: 1000, Description: &description},
	}})
	if s.finishTimer != nil {
		s.finishTimer.Stop()
	}

	wants := []struct {
		id   string
		name string
		args map[string]any
	}{
		{"read-call", "Read", map[string]any{"file_path": "/tmp/a.txt", "offset": float64(3), "limit": float64(20)}},
		{"write-call", "Write", map[string]any{"file_path": "/tmp/a.txt", "content": "hello\n"}},
		{"shell-call", "Bash", map[string]any{"command": "pwd", "timeout": float64(1000), "description": "查看目录"}},
	}
	for _, want := range wants {
		if start := <-s.events; start != (ToolCallStart{ID: want.id, Name: want.name}) {
			t.Fatalf("工具开始事件错误: %+v", start)
		}
		end, ok := (<-s.events).(ToolCallEnd)
		if !ok || end.ID != want.id || end.Name != want.name {
			t.Fatalf("工具结束事件错误: %+v", end)
		}
		var args map[string]any
		if err := json.Unmarshal(end.ArgsJSON, &args); err != nil {
			t.Fatalf("解析工具参数失败: %v", err)
		}
		if !reflect.DeepEqual(args, want.args) {
			t.Fatalf("工具 %s 参数错误: got=%v want=%v", want.name, args, want.args)
		}
	}
	if s.pendingExec["read-call"].kind != pendingExecRead ||
		s.pendingExec["write-call"].kind != pendingExecWrite ||
		s.pendingExec["shell-call"].kind != pendingExecShell {
		t.Fatalf("原生 exec 未按类型登记: %+v", s.pendingExec)
	}
}

func TestSession扩展原生Exec优先映射ClaudeCode工具(t *testing.T) {
	s := &session{
		tools:               claudeCodeFileTools(t),
		events:              make(chan Event, 24),
		started:             make(map[string]*trackedCall),
		pendingExec:         make(map[string]pendingExecCall),
		toolCallDrainWindow: time.Hour,
	}
	path := "/tmp/work"
	glob := "*.go"
	outputMode := "content"
	ignoreCase := true
	s.handleExec(&agentpb.ExecServerMessage{Id: 10, ExecId: "ls-exec", Message: &agentpb.ExecServerMessage_LsArgs{
		LsArgs: &agentpb.LsArgs{Path: path, ToolCallId: "ls-call"},
	}})
	s.handleExec(&agentpb.ExecServerMessage{Id: 11, ExecId: "grep-exec", Message: &agentpb.ExecServerMessage_GrepArgs{
		GrepArgs: &agentpb.GrepArgs{
			Pattern: "hello", Path: &path, Glob: &glob, OutputMode: &outputMode,
			CaseInsensitive: &ignoreCase, ToolCallId: "grep-call",
		},
	}})
	s.handleExec(&agentpb.ExecServerMessage{Id: 12, ExecId: "delete-exec", Message: &agentpb.ExecServerMessage_DeleteArgs{
		DeleteArgs: &agentpb.DeleteArgs{Path: "/tmp/work/old.txt", ToolCallId: "delete-call"},
	}})
	s.handleExec(&agentpb.ExecServerMessage{Id: 13, ExecId: "fetch-exec", Message: &agentpb.ExecServerMessage_FetchArgs{
		FetchArgs: &agentpb.FetchArgs{Url: "https://example.com", ToolCallId: "fetch-call"},
	}})
	s.handleExec(&agentpb.ExecServerMessage{Id: 14, ExecId: "task-exec", Message: &agentpb.ExecServerMessage_SubagentArgs{
		SubagentArgs: &agentpb.SubagentArgs{Prompt: "检查代码", SubagentType: "general-purpose", ToolCallId: "task-call"},
	}})
	if s.finishTimer != nil {
		s.finishTimer.Stop()
	}

	wants := []struct {
		id   string
		name string
		args map[string]any
	}{
		{"ls-call", "Bash", map[string]any{"command": "ls -1Ap -- '/tmp/work'", "description": "列出目录内容"}},
		{"grep-call", "Grep", map[string]any{
			"pattern": "hello", "path": path, "glob": glob, "output_mode": outputMode, "-i": true,
		}},
		{"delete-call", "Bash", map[string]any{"command": "rm -f -- '/tmp/work/old.txt'", "description": "删除文件"}},
		{"fetch-call", "WebFetch", map[string]any{"url": "https://example.com", "prompt": "获取该 URL 的完整正文内容。"}},
		{"task-call", "Task", map[string]any{
			"prompt": "检查代码", "description": "执行 Cursor 请求的子代理任务",
			"subagent_type": "general-purpose", "run_in_background": false,
		}},
	}
	for _, want := range wants {
		if start := <-s.events; start != (ToolCallStart{ID: want.id, Name: want.name}) {
			t.Fatalf("工具开始事件错误: %+v", start)
		}
		end, ok := (<-s.events).(ToolCallEnd)
		if !ok {
			t.Fatalf("工具结束事件类型错误: %+v", end)
		}
		var args map[string]any
		if err := json.Unmarshal(end.ArgsJSON, &args); err != nil {
			t.Fatalf("解析工具参数失败: %v", err)
		}
		if end.ID != want.id || end.Name != want.name || !reflect.DeepEqual(args, want.args) {
			t.Fatalf("工具映射错误: end=%+v args=%v want=%v", end, args, want)
		}
	}
	if s.pendingExec["ls-call"].kind != pendingExecLs ||
		s.pendingExec["grep-call"].kind != pendingExecGrep ||
		s.pendingExec["delete-call"].kind != pendingExecDelete ||
		s.pendingExec["fetch-call"].kind != pendingExecFetch ||
		s.pendingExec["task-call"].kind != pendingExecSubagent {
		t.Fatalf("扩展原生 exec 未按类型登记: %+v", s.pendingExec)
	}
}

func TestBuildExecResumeResults还原原生读写结果(t *testing.T) {
	content := "hello\nworld"
	results, err := buildExecResumeResults(&ParsedRequest{Messages: []NMessage{
		{Role: "tool", ToolCallID: "write-call", Content: []ContentPart{{Type: "text", Text: "写入成功"}}},
		{Role: "tool", ToolCallID: "read-call", Content: []ContentPart{{Type: "text", Text: content}}},
	}}, map[string]pendingExecCall{
		"write-call": {
			toolCallID: "write-call", kind: pendingExecWrite, messageID: 1, execID: "write-exec",
			path: "/tmp/a.txt", writeContent: content, writeFileSize: len(content), returnWriteContent: true,
		},
		"read-call": {
			toolCallID: "read-call", kind: pendingExecRead, messageID: 2, execID: "read-exec", path: "/tmp/a.txt",
		},
	})
	if err != nil {
		t.Fatalf("构造原生读写结果失败: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("原生读写结果数量错误: %d", len(results))
	}
	write := results[0].GetWriteResult().GetSuccess()
	if write == nil || write.GetPath() != "/tmp/a.txt" || write.GetLinesCreated() != 2 ||
		write.GetFileContentAfterWrite() != content {
		t.Fatalf("WriteResult 错误: %+v", write)
	}
	read := results[1].GetReadResult().GetSuccess()
	if read == nil || read.GetContent() != content || read.GetTotalLines() != 2 || read.GetFileSize() != int64(len(content)) {
		t.Fatalf("ReadResult 错误: %+v", read)
	}
}

func TestBuildExecResult还原图片读取结果(t *testing.T) {
	message := buildExecResult(pendingExecCall{
		kind: pendingExecRead, path: "/tmp/image.png",
	}, NMessage{Role: "tool", Content: []ContentPart{{
		Type: "image", ImageMime: "image/png", ImageData: "AQID",
	}}})[0]
	success := message.GetReadResult().GetSuccess()
	if success == nil || !reflect.DeepEqual(success.GetData(), []byte{1, 2, 3}) || success.GetFileSize() != 3 {
		t.Fatalf("图片 ReadResult 错误: %+v", success)
	}
}

func TestNormalizeClaudeReadOutput去掉行号和系统提示(t *testing.T) {
	input := "     1→第一行\n     2→第二行\n<system-reminder>不要把提示当作正文</system-reminder>"
	if got := normalizeClaudeReadOutput(input); got != "第一行\n第二行" {
		t.Fatalf("Read 输出净化错误: %q", got)
	}
	plain := "没有行号的普通正文\n第二行"
	if got := normalizeClaudeReadOutput(plain); got != plain {
		t.Fatalf("普通正文不应被改写: %q", got)
	}
}

func TestSession诊断映射为ClaudeCodeLSP工具(t *testing.T) {
	definitions, err := buildToolDefinitions([]NToolDef{{
		Name: "LSP", Schema: json.RawMessage(`{"type":"object","properties":{"action":{"type":"string"},"file":{"type":"string"}},"required":["action","file"]}`),
	}})
	if err != nil {
		t.Fatalf("构造 LSP 工具定义失败: %v", err)
	}
	s := &session{
		tools: definitions, events: make(chan Event, 4), started: make(map[string]*trackedCall),
		pendingExec: make(map[string]pendingExecCall), toolCallDrainWindow: time.Hour,
	}
	s.handleExec(&agentpb.ExecServerMessage{Id: 20, ExecId: "diagnostics-exec", Message: &agentpb.ExecServerMessage_DiagnosticsArgs{
		DiagnosticsArgs: &agentpb.DiagnosticsArgs{Path: "/tmp/a.go", ToolCallId: "diagnostics-call"},
	}})
	if s.finishTimer != nil {
		s.finishTimer.Stop()
	}
	<-s.events
	end := (<-s.events).(ToolCallEnd)
	var args map[string]any
	if err := json.Unmarshal(end.ArgsJSON, &args); err != nil {
		t.Fatalf("解析 LSP 参数失败: %v", err)
	}
	want := map[string]any{"action": "diagnostics", "file": "/tmp/a.go"}
	if end.Name != "LSP" || !reflect.DeepEqual(args, want) {
		t.Fatalf("诊断工具映射错误: name=%q args=%v", end.Name, args)
	}
	message := buildExecResult(s.pendingExec["diagnostics-call"], NMessage{
		Role: "tool", Content: []ContentPart{{Type: "text", Text: "没有诊断"}},
	})[0]
	success := message.GetDiagnosticsResult().GetSuccess()
	if success == nil || success.GetPath() != "/tmp/a.go" || success.GetTotalDiagnostics() != 0 {
		t.Fatalf("DiagnosticsResult 错误: %+v", message.GetDiagnosticsResult())
	}
}

func TestSessionPi多处编辑在缺少MultiEdit时回退Bash(t *testing.T) {
	tools := claudeCodeFileTools(t)
	withoutMultiEdit := make([]*agentpb.McpToolDefinition, 0, len(tools)-1)
	for _, tool := range tools {
		if localToolName(tool.GetName()) != "MultiEdit" {
			withoutMultiEdit = append(withoutMultiEdit, tool)
		}
	}
	s := &session{
		tools: withoutMultiEdit, events: make(chan Event, 4), started: make(map[string]*trackedCall),
		pendingExec: make(map[string]pendingExecCall), toolCallDrainWindow: time.Hour,
	}
	s.handleExec(&agentpb.ExecServerMessage{Id: 21, ExecId: "pi-edit-exec", Message: &agentpb.ExecServerMessage_PiEditArgs{
		PiEditArgs: &agentpb.PiEditExecArgs{Path: "/tmp/a.txt", Edits: []*agentpb.PiEditReplacement{
			{OldText: "旧一", NewText: "新一"},
			{OldText: "旧二", NewText: "新二"},
		}},
	}})
	if s.finishTimer != nil {
		s.finishTimer.Stop()
	}
	start := (<-s.events).(ToolCallStart)
	end := (<-s.events).(ToolCallEnd)
	if start.Name != "Bash" || end.Name != "Bash" {
		t.Fatalf("Pi 多处编辑未回退 Bash: start=%+v end=%+v", start, end)
	}
	var args map[string]any
	if err := json.Unmarshal(end.ArgsJSON, &args); err != nil {
		t.Fatalf("解析 Bash 参数失败: %v", err)
	}
	command, _ := args["command"].(string)
	if !strings.Contains(command, "node -e") || strings.Contains(command, "旧一") {
		t.Fatalf("批量编辑命令未使用编码载荷: %q", command)
	}
	call := s.pendingExec[end.ID]
	if call.kind != pendingExecPiEdit || call.path != "/tmp/a.txt" {
		t.Fatalf("Pi 编辑等待状态错误: %+v", call)
	}
}

func TestBuildMcpStateResult按Provider分组和筛选(t *testing.T) {
	tools := []*agentpb.McpToolDefinition{
		{Name: "fn_Read", ProviderIdentifier: "claude-code"},
		{Name: "fn_Write", ProviderIdentifier: "claude-code"},
		{Name: "fn_other", ProviderIdentifier: "other"},
	}
	result := buildMcpStateResult(tools, []string{"claude-code"}).GetSuccess()
	if result == nil || len(result.GetServers()) != 1 {
		t.Fatalf("MCP 状态分组错误: %+v", result)
	}
	server := result.GetServers()[0]
	if server.GetServerIdentifier() != "claude-code" || server.GetStatus() != "connected" || len(server.GetTools()) != 2 {
		t.Fatalf("MCP 服务状态错误: %+v", server)
	}
}

func TestBuildNeutralHookResult保留请求类型(t *testing.T) {
	request := &agentpb.ExecuteHookRequest{Request: &agentpb.ExecuteHookRequest_PreToolUse{
		PreToolUse: &agentpb.PreToolUseRequestQuery{},
	}}
	result := buildNeutralHookResult(request)
	if result == nil || result.GetResponse().GetPreToolUse() == nil {
		t.Fatalf("Hook 中性响应类型错误: %+v", result)
	}
	if buildNeutralHookResult(nil) != nil {
		t.Fatal("空 Hook 请求不应伪造成功响应")
	}
}

func TestBuildExecResult还原扩展和Pi强类型结果(t *testing.T) {
	textResult := func(text string) NMessage {
		return NMessage{Role: "tool", Content: []ContentPart{{Type: "text", Text: text}}}
	}

	ls := buildExecResult(pendingExecCall{kind: pendingExecLs, path: "/tmp/work"}, textResult("sub/\na.txt\n"))[0]
	root := ls.GetLsResult().GetSuccess().GetDirectoryTreeRoot()
	if root == nil || len(root.GetChildrenDirs()) != 1 || root.GetChildrenDirs()[0].GetAbsPath() != "/tmp/work/sub" ||
		len(root.GetChildrenFiles()) != 1 || root.GetChildrenFiles()[0].GetName() != "a.txt" {
		t.Fatalf("LsResult 错误: %+v", root)
	}

	grep := buildExecResult(pendingExecCall{
		kind: pendingExecGrep, path: "/tmp/work", pattern: "hello", outputMode: "content",
	}, textResult("/tmp/work/a.go:4:hello world\n"))[0]
	content := grep.GetGrepResult().GetSuccess().GetWorkspaceResults()["/tmp/work"].GetContent()
	if content == nil || content.GetTotalMatchedLines() != 1 || content.GetMatches()[0].GetMatches()[0].GetLineNumber() != 4 {
		t.Fatalf("GrepResult 错误: %+v", content)
	}

	deleted := buildExecResult(pendingExecCall{kind: pendingExecDelete, path: "/tmp/old.txt"}, textResult(""))[0]
	if deleted.GetDeleteResult().GetSuccess().GetDeletedFile() != "/tmp/old.txt" {
		t.Fatalf("DeleteResult 错误: %+v", deleted.GetDeleteResult())
	}
	fetched := buildExecResult(pendingExecCall{kind: pendingExecFetch, url: "https://example.com"}, textResult("正文"))[0]
	if fetched.GetFetchResult().GetSuccess().GetContent() != "正文" {
		t.Fatalf("FetchResult 错误: %+v", fetched.GetFetchResult())
	}
	task := buildExecResult(pendingExecCall{kind: pendingExecSubagent}, textResult("任务完成"))[0]
	if task.GetSubagentResult().GetSuccess().GetFinalMessage() != "任务完成" {
		t.Fatalf("SubagentResult 错误: %+v", task.GetSubagentResult())
	}

	piResults := []struct {
		kind pendingExecKind
		ok   func(*agentpb.ExecClientMessage) bool
	}{
		{pendingExecPiRead, func(message *agentpb.ExecClientMessage) bool {
			return message.GetPiReadResult().GetSuccess().GetOutput() == "结果"
		}},
		{pendingExecPiBash, func(message *agentpb.ExecClientMessage) bool {
			return message.GetPiBashResult().GetSuccess().GetOutput() == "结果"
		}},
		{pendingExecPiEdit, func(message *agentpb.ExecClientMessage) bool {
			return message.GetPiEditResult().GetSuccess().GetOutput() == "结果"
		}},
		{pendingExecPiWrite, func(message *agentpb.ExecClientMessage) bool {
			return message.GetPiWriteResult().GetSuccess().GetOutput() == "结果"
		}},
		{pendingExecPiGrep, func(message *agentpb.ExecClientMessage) bool {
			return message.GetPiGrepResult().GetSuccess().GetOutput() == "结果"
		}},
		{pendingExecPiFind, func(message *agentpb.ExecClientMessage) bool {
			return message.GetPiFindResult().GetSuccess().GetOutput() == "结果"
		}},
		{pendingExecPiLs, func(message *agentpb.ExecClientMessage) bool {
			return message.GetPiLsResult().GetSuccess().GetOutput() == "结果"
		}},
	}
	for _, test := range piResults {
		message := buildExecResult(pendingExecCall{kind: test.kind}, textResult("结果"))[0]
		if !test.ok(message) {
			t.Fatalf("Pi 结果类型 %d 还原错误: %+v", test.kind, message)
		}
	}
}

func TestRunSession原生列目录写入读取经ClaudeCode续接(t *testing.T) {
	serverResults := make(chan *agentpb.ExecClientMessage, 3)
	serverErrors := make(chan error, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		report := func(err error) {
			select {
			case serverErrors <- err:
			default:
			}
		}
		reader := bufio.NewReader(r.Body)
		if _, _, err := ReadFrame(reader); err != nil {
			report(fmt.Errorf("读取 run request 失败: %w", err))
			return
		}
		w.Header().Set("content-type", "application/connect+proto")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		flusher.Flush()
		writeMessage := func(message *agentpb.AgentServerMessage) bool {
			payload, err := proto.Marshal(message)
			if err != nil {
				report(err)
				return false
			}
			if _, err := w.Write(FrameMessage(payload, false)); err != nil {
				report(err)
				return false
			}
			flusher.Flush()
			return true
		}
		readResult := func() bool {
			_, payload, err := ReadFrame(reader)
			if err != nil {
				report(err)
				return false
			}
			var message agentpb.AgentClientMessage
			if err := proto.Unmarshal(payload, &message); err != nil {
				report(err)
				return false
			}
			serverResults <- message.GetExecClientMessage()
			return true
		}

		if !writeMessage(&agentpb.AgentServerMessage{Message: &agentpb.AgentServerMessage_ExecServerMessage{
			ExecServerMessage: &agentpb.ExecServerMessage{Id: 40, ExecId: "ls-exec", Message: &agentpb.ExecServerMessage_LsArgs{
				LsArgs: &agentpb.LsArgs{Path: "/tmp", ToolCallId: "ls-call"},
			}},
		}}) || !readResult() {
			return
		}
		if !writeMessage(&agentpb.AgentServerMessage{Message: &agentpb.AgentServerMessage_ExecServerMessage{
			ExecServerMessage: &agentpb.ExecServerMessage{Id: 41, ExecId: "write-exec", Message: &agentpb.ExecServerMessage_WriteArgs{
				WriteArgs: &agentpb.WriteArgs{Path: "/tmp/rw_test.txt", FileText: "hello", ToolCallId: "write-call"},
			}},
		}}) || !readResult() {
			return
		}
		if !writeMessage(&agentpb.AgentServerMessage{Message: &agentpb.AgentServerMessage_ExecServerMessage{
			ExecServerMessage: &agentpb.ExecServerMessage{Id: 42, ExecId: "read-exec", Message: &agentpb.ExecServerMessage_ReadArgs{
				ReadArgs: &agentpb.ReadArgs{Path: "/tmp/rw_test.txt", ToolCallId: "read-call"},
			}},
		}}) || !readResult() {
			return
		}
		writeMessage(&agentpb.AgentServerMessage{Message: &agentpb.AgentServerMessage_InteractionUpdate{
			InteractionUpdate: &agentpb.InteractionUpdate{Message: &agentpb.InteractionUpdate_TextDelta{
				TextDelta: &agentpb.TextDeltaUpdate{Text: "读写成功"},
			}},
		}})
		writeMessage(&agentpb.AgentServerMessage{Message: &agentpb.AgentServerMessage_InteractionUpdate{
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
	resumable := newResumableCursorStream(cancelSession)
	events := RunSession(sessionContext, SessionOptions{
		Client:       NewClient(server.URL, "test"),
		Run:          RunOptions{AccessToken: "token", Transport: server.Client().Transport},
		RequestBytes: []byte("run"), Tools: claudeCodeFileTools(t), Store: NewBlobStore(),
		HeartbeatInterval: time.Hour, ToolCallDrainWindow: 10 * time.Millisecond,
		PausedStreamTTL: time.Second, Resumable: resumable, DownstreamContext: context.Background(),
	})

	assertToolSegment(t, events, "ls-call", "Bash")
	second := resumeToolResult(t, resumable, "ls-call", "rw_test.txt\n")
	select {
	case result := <-serverResults:
		root := result.GetLsResult().GetSuccess().GetDirectoryTreeRoot()
		if root == nil || len(root.GetChildrenFiles()) != 1 || root.GetChildrenFiles()[0].GetName() != "rw_test.txt" {
			t.Fatalf("Cursor 未收到正确的 LsResult.Success: %+v", result)
		}
	case err := <-serverErrors:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("Cursor 未收到列目录结果")
	}
	assertToolSegment(t, second, "write-call", "Write")
	third := resumeToolResult(t, resumable, "write-call", "写入成功")
	select {
	case result := <-serverResults:
		if result.GetWriteResult().GetSuccess() == nil {
			t.Fatalf("Cursor 未收到 WriteResult.Success: %+v", result)
		}
	case err := <-serverErrors:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("Cursor 未收到写入结果")
	}
	assertToolSegment(t, third, "read-call", "Read")
	fourth := resumeToolResult(t, resumable, "read-call", "hello")
	select {
	case result := <-serverResults:
		if got := result.GetReadResult().GetSuccess().GetContent(); got != "hello" {
			t.Fatalf("Cursor 收到的读取内容错误: %q", got)
		}
	case err := <-serverErrors:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("Cursor 未收到读取结果")
	}

	var gotText, gotDone bool
	for event := range fourth {
		switch value := event.(type) {
		case TextDelta:
			gotText = value.Text == "读写成功"
		case Done:
			gotDone = value.FinishReason == "stop"
		case ErrEvent:
			t.Fatal(value.Err)
		}
	}
	if !gotText || !gotDone {
		t.Fatalf("最终响应不完整: text=%v done=%v", gotText, gotDone)
	}
}

func assertToolSegment(t *testing.T, events <-chan Event, wantID, wantName string) {
	t.Helper()
	var gotTool, gotDone bool
	for event := range events {
		switch value := event.(type) {
		case ToolCallEnd:
			gotTool = value.ID == wantID && value.Name == wantName
		case Done:
			gotDone = value.FinishReason == "tool_calls"
		case ErrEvent:
			t.Fatal(value.Err)
		}
	}
	if !gotTool || !gotDone {
		t.Fatalf("工具分段不完整: id=%s name=%s tool=%v done=%v", wantID, wantName, gotTool, gotDone)
	}
}

func resumeToolResult(t *testing.T, stream *resumableCursorStream, id, text string) <-chan Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)
	events, active, err := stream.tryResume(ctx, &ParsedRequest{Messages: []NMessage{{
		Role: "tool", ToolCallID: id, Content: []ContentPart{{Type: "text", Text: text}},
	}}})
	if err != nil || !active {
		t.Fatalf("续接工具 %s 失败: active=%v err=%v", id, active, err)
	}
	return events
}
