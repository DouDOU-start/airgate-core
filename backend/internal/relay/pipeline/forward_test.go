package pipeline

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
)

// TestChannelSlotTTL 渠道并发槽 TTL：流式 30min（长流无总超时防僵尸清理），
// 非流式传 0 走 concurrency 层默认 5min。
func TestChannelSlotTTL(t *testing.T) {
	cases := []struct {
		name   string
		stream bool
		want   time.Duration
	}{
		{"流式 30min", true, 30 * time.Minute},
		{"非流式用默认（0）", false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := channelSlotTTL(tc.stream); got != tc.want {
				t.Errorf("channelSlotTTL(%v) = %v, want %v", tc.stream, got, tc.want)
			}
		})
	}
}

// TestReasoningEffortOf 三协议档位取值路径：OpenAI 顶层 reasoning_effort、
// Responses 嵌套 reasoning.effort、Anthropic 嵌套 output_config.effort。
func TestReasoningEffortOf(t *testing.T) {
	cases := []struct {
		name     string
		protocol string
		body     string
		want     string
	}{
		{"openai chat completions 顶层", registry.ProtocolOpenAI, `{"model":"gpt-5","reasoning_effort":"low"}`, "low"},
		{"openai responses 嵌套", registry.ProtocolOpenAI, `{"model":"gpt-5","reasoning":{"effort":"medium"}}`, "medium"},
		{"anthropic 嵌套 output_config", registry.ProtocolAnthropic, `{"model":"claude-opus-4-8","output_config":{"effort":"xhigh"}}`, "xhigh"},
		{"anthropic 手动预算无档位字符串", registry.ProtocolAnthropic, `{"model":"claude-opus-4-5","thinking":{"type":"enabled","budget_tokens":10000}}`, ""},
		{"字段缺失", registry.ProtocolOpenAI, `{"model":"gpt-5"}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := dto.ParseChatRequest([]byte(tc.body))
			if err != nil {
				t.Fatalf("ParseChatRequest: %v", err)
			}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			setEntryProtocol(c, tc.protocol)
			if got := reasoningEffortOf(c, req); got != tc.want {
				t.Errorf("reasoningEffortOf() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestIsSSEContentType 流式分支只在上游确按 SSE 响应时进入。
func TestIsSSEContentType(t *testing.T) {
	cases := []struct {
		ct   string
		want bool
	}{
		{"text/event-stream", true},
		{"text/event-stream; charset=utf-8", true},
		{"TEXT/EVENT-STREAM", true},
		{" text/event-stream", true},
		{"application/json", false},
		{"text/html", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isSSEContentType(tc.ct); got != tc.want {
			t.Errorf("isSSEContentType(%q) = %v, want %v", tc.ct, got, tc.want)
		}
	}
}
