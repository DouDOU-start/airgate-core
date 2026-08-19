package cursor

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestBuildMCPResumeResults保留关联字段并转换成功结果(t *testing.T) {
	pending := map[string]pendingExecCall{
		"call-2": {toolCallID: "call-2", name: "second", messageID: 12, execID: "exec-12"},
		"call-1": {toolCallID: "call-1", name: "first", messageID: 11, execID: "exec-11"},
	}
	parsed := &ParsedRequest{Messages: []NMessage{
		{Role: "tool", ToolCallID: "call-2", Content: []ContentPart{{Type: "text", Text: "第二个结果"}}},
		{Role: "tool", ToolCallID: "call-1", Content: []ContentPart{{Type: "text", Text: "第一个结果"}}},
	}}

	results, err := buildExecResumeResults(parsed, pending)
	if err != nil {
		t.Fatalf("构造 MCP 续接结果失败: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("结果数量错误: %d", len(results))
	}
	for i, want := range []struct {
		id     uint32
		execID string
		text   string
	}{{11, "exec-11", "第一个结果"}, {12, "exec-12", "第二个结果"}} {
		got := results[i]
		if got.GetId() != want.id || got.GetExecId() != want.execID {
			t.Fatalf("第 %d 个结果关联字段错误: %+v", i, got)
		}
		content := got.GetMcpResult().GetSuccess().GetContent()
		if len(content) != 1 || content[0].GetText().GetText() != want.text {
			t.Fatalf("第 %d 个成功结果内容错误: %+v", i, content)
		}
	}
}

func TestBuildMCPResumeResults转换错误结果(t *testing.T) {
	results, err := buildExecResumeResults(&ParsedRequest{Messages: []NMessage{{
		Role: "tool", ToolCallID: "call-error", IsError: true,
		Content: []ContentPart{{Type: "text", Text: "执行失败"}},
	}}}, map[string]pendingExecCall{
		"call-error": {toolCallID: "call-error", name: "broken", messageID: 7, execID: "exec-7"},
	})
	if err != nil {
		t.Fatalf("构造错误结果失败: %v", err)
	}
	if got := results[0].GetMcpResult().GetError().GetError(); got != "执行失败" {
		t.Fatalf("错误内容不符: %q", got)
	}
}

func TestBuildMCPResumeResults支持OpenAI和Anthropic请求(t *testing.T) {
	tests := []struct {
		name      string
		protocol  Protocol
		payload   string
		wantText  string
		wantError bool
	}{
		{
			name:     "OpenAI工具消息",
			protocol: ProtoOpenAI,
			payload: `{"model":"test","stream":true,"messages":[` +
				`{"role":"assistant","tool_calls":[{"id":"call-1","type":"function","function":{"name":"read","arguments":"{}"}}]},` +
				`{"role":"tool","tool_call_id":"call-1","content":"openai-result"}]}`,
			wantText: "openai-result",
		},
		{
			name:      "Anthropic工具结果块",
			protocol:  ProtoAnthropic,
			payload:   `{"model":"test","stream":true,"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"call-1","content":"anthropic-error","is_error":true}]}]}`,
			wantText:  "anthropic-error",
			wantError: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed, err := ParseRequest(test.protocol, []byte(test.payload))
			if err != nil {
				t.Fatalf("解析请求失败: %v", err)
			}
			results, err := buildExecResumeResults(parsed, map[string]pendingExecCall{
				"call-1": {toolCallID: "call-1", messageID: 8, execID: "exec-8"},
			})
			if err != nil {
				t.Fatalf("转换续接结果失败: %v", err)
			}
			result := results[0].GetMcpResult()
			if test.wantError {
				if got := result.GetError().GetError(); got != test.wantText {
					t.Fatalf("错误结果不符: %q", got)
				}
				return
			}
			content := result.GetSuccess().GetContent()
			if len(content) != 1 || content[0].GetText().GetText() != test.wantText {
				t.Fatalf("成功结果不符: %+v", content)
			}
		})
	}
}

func TestBuildMCPResumeResults缺少工具结果时明确报错(t *testing.T) {
	_, err := buildExecResumeResults(&ParsedRequest{}, map[string]pendingExecCall{
		"call-missing": {toolCallID: "call-missing", name: "read"},
	})
	if err == nil || !strings.Contains(err.Error(), "call-missing") {
		t.Fatalf("应返回包含 tool_call_id 的错误，实际为 %v", err)
	}
}

func TestResumableStream缺失结果不会清空等待状态(t *testing.T) {
	stream := newResumableCursorStream(func() {})
	stream.setWaiting(map[string]pendingExecCall{
		"call-1": {toolCallID: "call-1", name: "read", messageID: 9, execID: "exec-9"},
	})
	if _, active, err := stream.tryResume(context.Background(), &ParsedRequest{}); !active || err == nil {
		t.Fatalf("缺失结果应命中等待流并报错: active=%v err=%v", active, err)
	}

	received := make(chan resumeCommand, 1)
	go func() {
		command := <-stream.resumeCh
		received <- command
		command.ready <- nil
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	events, active, err := stream.tryResume(ctx, &ParsedRequest{Messages: []NMessage{{
		Role: "tool", ToolCallID: "call-1", Content: []ContentPart{{Type: "text", Text: "ok"}},
	}}})
	if err != nil || !active || events == nil {
		t.Fatalf("补齐结果后应可续接: active=%v events=%v err=%v", active, events != nil, err)
	}
	command := <-received
	if len(command.results) != 1 || command.results[0].GetMcpResult().GetSuccess() == nil {
		t.Fatalf("续接命令未携带成功结果: %+v", command.results)
	}
}

func TestResumableStream投递取消后可重试(t *testing.T) {
	stream := newResumableCursorStream(func() {})
	stream.setWaiting(map[string]pendingExecCall{
		"call-1": {toolCallID: "call-1", messageID: 1},
	})
	parsed := &ParsedRequest{Messages: []NMessage{{
		Role: "tool", ToolCallID: "call-1", Content: []ContentPart{{Type: "text", Text: "ok"}},
	}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, active, err := stream.tryResume(ctx, parsed); !active || err == nil {
		t.Fatalf("取消的投递应报错且保持活跃: active=%v err=%v", active, err)
	}

	go func() {
		command := <-stream.resumeCh
		command.ready <- nil
	}()
	retryContext, retryCancel := context.WithTimeout(context.Background(), time.Second)
	defer retryCancel()
	if _, active, err := stream.tryResume(retryContext, parsed); err != nil || !active {
		t.Fatalf("取消后应允许重试: active=%v err=%v", active, err)
	}
}

func TestBuildMCPResultContent支持图片(t *testing.T) {
	content := buildMCPResultContent([]ContentPart{{
		Type: "image", ImageMime: "image/png", ImageData: "AQID",
	}})
	if len(content) != 1 || string(content[0].GetImage().GetData()) != "\x01\x02\x03" || content[0].GetImage().GetMimeType() != "image/png" {
		t.Fatalf("图片工具结果转换错误: %+v", content)
	}
}

func TestResumableStream生成中拒绝并发续接(t *testing.T) {
	stream := newResumableCursorStream(func() {})
	if _, active, err := stream.tryResume(context.Background(), &ParsedRequest{}); !active || err == nil {
		t.Fatalf("尚未进入等待态时应拒绝同会话并发请求: active=%v err=%v", active, err)
	}
	stream.markClosed()
	if _, active, err := stream.tryResume(context.Background(), &ParsedRequest{}); active || err != nil {
		t.Fatalf("关闭后不应再视为活跃流: active=%v err=%v", active, err)
	}
}
