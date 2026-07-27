package pipeline

import (
	"context"
	"testing"

	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
)

func TestChannelTestFailure保留渠道与密钥名称(t *testing.T) {
	errSink := &fakeErrSink{}
	pipe := &Pipeline{errSink: errSink}
	snap := &registry.ChannelKeySnapshot{
		KeyID:       23,
		KeyName:     "测试密钥",
		ChannelID:   7,
		ChannelName: "测试渠道",
		Type:        "openai_compatible",
		APIKey:      "sk-test",
	}

	if _, err := pipe.TestChannel(context.Background(), snap, "", ""); err == nil {
		t.Fatal("缺少测试模型时应返回错误")
	}
	entry := errSink.lastEntry(t)
	if len(entry.Chain) != 1 {
		t.Fatalf("测试失败重试链长度 = %d，期望 1", len(entry.Chain))
	}
	hop := entry.Chain[0]
	if hop.ChannelName != snap.ChannelName || hop.KeyName != snap.KeyName {
		t.Fatalf("渠道与密钥名称留痕异常：%+v", hop)
	}
}

// TestExtractUsageFromSSE 覆盖对 stream:false 仍回 SSE 的上游的 usage 兜底提取：
// Responses 流的 usage 只在流尾 response.completed 事件（嵌套 response.usage）。
func TestExtractUsageFromSSE(t *testing.T) {
	t.Run("responses completed event", func(t *testing.T) {
		body := "event: response.created\n" +
			"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"status\":\"in_progress\",\"usage\":null}}\n\n" +
			"event: response.output_text.delta\n" +
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n" +
			"event: response.completed\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"usage\":{\"input_tokens\":547,\"input_tokens_details\":{\"cached_tokens\":128},\"output_tokens\":16,\"total_tokens\":563}}}\n\n"
		usage := extractUsageFromSSE([]byte(body))
		if usage == nil {
			t.Fatal("usage = nil, want parsed")
			return
		}
		if usage.PromptTokens != 547 || usage.CompletionTokens != 16 || usage.CachedTokens != 128 {
			t.Fatalf("usage = %+v, want prompt=547 completion=16 cached=128", usage)
		}
	})

	t.Run("chat completions final usage chunk", func(t *testing.T) {
		body := "data: {\"choices\":[{\"delta\":{\"content\":\"h\"}}]}\n\n" +
			"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":1}}\n\n" +
			"data: [DONE]\n\n"
		usage := extractUsageFromSSE([]byte(body))
		if usage == nil {
			t.Fatal("usage = nil, want parsed")
			return
		}
		if usage.PromptTokens != 10 || usage.CompletionTokens != 1 {
			t.Fatalf("usage = %+v, want prompt=10 completion=1", usage)
		}
	})

	t.Run("no usage anywhere", func(t *testing.T) {
		body := "event: ping\ndata: {\"type\":\"ping\"}\n\ndata: [DONE]\n\n"
		if usage := extractUsageFromSSE([]byte(body)); usage != nil {
			t.Fatalf("usage = %+v, want nil", usage)
		}
	})
}
