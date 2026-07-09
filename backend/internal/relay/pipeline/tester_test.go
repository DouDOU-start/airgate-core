package pipeline

import "testing"

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
