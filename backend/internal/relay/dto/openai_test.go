package dto

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestParseChatRequest(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantModel  string
		wantStream bool
		wantErr    bool
	}{
		{
			name:       "常规请求",
			body:       `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`,
			wantModel:  "gpt-4o",
			wantStream: true,
		},
		{
			name:      "无 stream 字段",
			body:      `{"model":"gpt-4o","messages":[]}`,
			wantModel: "gpt-4o",
		},
		{
			name:    "非 JSON 对象",
			body:    `[1,2,3]`,
			wantErr: true,
		},
		{
			name:    "非法 JSON",
			body:    `{model:`,
			wantErr: true,
		},
		{
			name:    "JSON null",
			body:    `null`,
			wantErr: true,
		},
		{
			name:      "model 非字符串保持空串",
			body:      `{"model":123}`,
			wantModel: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := ParseChatRequest([]byte(tc.body))
			if tc.wantErr {
				if err == nil {
					t.Fatal("期望解析失败")
				}
				return
			}
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if req.Model != tc.wantModel {
				t.Errorf("Model = %q, want %q", req.Model, tc.wantModel)
			}
			if req.Stream != tc.wantStream {
				t.Errorf("Stream = %v, want %v", req.Stream, tc.wantStream)
			}
		})
	}
}

func TestChatRequest未改写时复用原始请求体(t *testing.T) {
	body := []byte(" {\n  \"model\": \"gpt-5\", \"input\": \"保留原始格式\"\n} ")
	req, err := ParseChatRequest(body)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	out, err := req.Marshal()
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	if !bytes.Equal(out, body) {
		t.Fatalf("未改写请求没有复用原始字节: got=%q want=%q", out, body)
	}
}

func TestChatRequest改写后重新序列化且不污染原请求(t *testing.T) {
	body := []byte(`{"model":"gpt-5","input":"原始内容","temperature":0.5}`)
	req, err := ParseChatRequest(body)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	clone := req.Clone()
	if err := clone.Set("model", "gpt-5-upstream"); err != nil {
		t.Fatalf("改写模型失败: %v", err)
	}
	clone.Remove("temperature")

	out, err := clone.Marshal()
	if err != nil {
		t.Fatalf("序列化改写请求失败: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(out, &fields); err != nil {
		t.Fatalf("改写结果不是合法 JSON: %v", err)
	}
	if string(fields["model"]) != `"gpt-5-upstream"` {
		t.Fatalf("模型改写未生效: %s", out)
	}
	if _, ok := fields["temperature"]; ok {
		t.Fatalf("字段删除未生效: %s", out)
	}
	original, err := req.Marshal()
	if err != nil || !bytes.Equal(original, body) {
		t.Fatalf("Clone 改写污染原请求: body=%q err=%v", original, err)
	}
}

func BenchmarkChatRequestMarshal大请求(b *testing.B) {
	body := []byte(`{"model":"gpt-5.6","stream":true,"input":"` + strings.Repeat("x", 1<<20) + `"}`)
	req, err := ParseChatRequest(body)
	if err != nil {
		b.Fatal(err)
	}
	b.Run("未改写直接复用", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(body)))
		for b.Loop() {
			if _, err := req.Marshal(); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("字段改写后编码", func(b *testing.B) {
		clone := req.Clone()
		if err := clone.Set("model", "gpt-5.6-upstream"); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.SetBytes(int64(len(body)))
		for b.Loop() {
			if _, err := clone.Marshal(); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func TestChatRequest顶层字段零拷贝索引(t *testing.T) {
	body := []byte(`{"model":"旧模型","input":{"nested":[1,2,3]},"model":"gpt-5.6","stream":true}`)
	req, err := ParseChatRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if req.Model != "gpt-5.6" || !req.Stream {
		t.Fatalf("重复字段应沿用 JSON 对象最后值，model=%q stream=%v", req.Model, req.Stream)
	}
	raw, ok := req.Get("input")
	if !ok || string(raw) != `{"nested":[1,2,3]}` {
		t.Fatalf("input 索引错误：%q", raw)
	}
	inputOffset := bytes.Index(body, raw)
	if inputOffset < 0 || &raw[0] != &body[inputOffset] {
		t.Fatal("顶层字段应直接引用原始请求切片")
	}
}

func BenchmarkParseChatRequest大请求(b *testing.B) {
	body := []byte(`{"model":"gpt-5.6","stream":true,"input":"` + strings.Repeat("x", 2<<20) + `"}`)
	b.Run("零拷贝顶层索引", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(body)))
		for b.Loop() {
			if _, err := ParseChatRequest(body); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("旧全量RawMessage解析", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(body)))
		for b.Loop() {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(body, &fields); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// TestChatRequestRoundTrip 未知字段透传断言：解析 → 序列化后所有字段语义等价。
func TestChatRequestRoundTrip(t *testing.T) {
	body := `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}],` +
		`"unknown_future_field":{"nested":[1,2.5,"x",null,true]},"tool_choice":"auto","n":3}`
	req, err := ParseChatRequest([]byte(body))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	out, err := req.Marshal()
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}

	var want, got map[string]any
	if err := json.Unmarshal([]byte(body), &want); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal got: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("round-trip 不等价:\nwant %v\ngot  %v", want, got)
	}
}

// TestChatRequestCloneIsolation Clone 后改写不污染原请求。
func TestChatRequestCloneIsolation(t *testing.T) {
	req, err := ParseChatRequest([]byte(`{"model":"gpt-4o","temperature":0.5}`))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	clone := req.Clone()
	if err := clone.Set("model", "upstream-x"); err != nil {
		t.Fatalf("Set 失败: %v", err)
	}
	clone.Remove("temperature")

	out, _ := req.Marshal()
	var fields map[string]any
	_ = json.Unmarshal(out, &fields)
	if fields["model"] != "gpt-4o" {
		t.Errorf("原请求 model 被污染: %v", fields["model"])
	}
	if _, ok := fields["temperature"]; !ok {
		t.Error("原请求 temperature 被误删")
	}
}

func TestParseUsage(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		want  Usage
		found bool
	}{
		{
			name:  "OpenAI 基本命名",
			raw:   `{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150}`,
			want:  Usage{PromptTokens: 100, CompletionTokens: 50},
			found: true,
		},
		{
			name:  "OpenAI 带 prompt_tokens_details.cached_tokens",
			raw:   `{"prompt_tokens":100,"completion_tokens":50,"prompt_tokens_details":{"cached_tokens":30}}`,
			want:  Usage{PromptTokens: 100, CompletionTokens: 50, CachedTokens: 30},
			found: true,
		},
		{
			name:  "Anthropic 风格回退",
			raw:   `{"input_tokens":80,"output_tokens":40,"cache_read_input_tokens":20,"cache_creation_input_tokens":10}`,
			want:  Usage{PromptTokens: 80, CompletionTokens: 40, CachedTokens: 20, CacheCreationTokens: 10},
			found: true,
		},
		{
			name: "Responses 结构（input/output_tokens_details）",
			raw: `{"input_tokens":36,"input_tokens_details":{"cached_tokens":6},` +
				`"output_tokens":87,"output_tokens_details":{"reasoning_tokens":12},"total_tokens":123}`,
			want:  Usage{PromptTokens: 36, CompletionTokens: 87, CachedTokens: 6},
			found: true,
		},
		{
			name: "Responses 缓存写从总输入中独立拆分",
			raw: `{"input_tokens":100,"input_tokens_details":{"cached_tokens":30,"cache_write_tokens":40},` +
				`"output_tokens":20,"total_tokens":120}`,
			want:  Usage{PromptTokens: 60, CompletionTokens: 20, CachedTokens: 30, CacheCreationTokens: 40},
			found: true,
		},
		{
			name: "Chat Completions 兼容 cache_creation_tokens 命名",
			raw: `{"prompt_tokens":80,"prompt_tokens_details":{"cached_tokens":20,"cache_creation_tokens":10},` +
				`"completion_tokens":5}`,
			want:  Usage{PromptTokens: 70, CompletionTokens: 5, CachedTokens: 20, CacheCreationTokens: 10},
			found: true,
		},
		{
			name:  "Responses 无 details 子对象",
			raw:   `{"input_tokens":10,"output_tokens":5,"total_tokens":15}`,
			want:  Usage{PromptTokens: 10, CompletionTokens: 5},
			found: true,
		},
		{
			name:  "chat 与 Responses cached 命名不串味（chat 优先）",
			raw:   `{"prompt_tokens":100,"completion_tokens":50,"prompt_tokens_details":{"cached_tokens":30},"input_tokens_details":{"cached_tokens":999}}`,
			want:  Usage{PromptTokens: 100, CompletionTokens: 50, CachedTokens: 30},
			found: true,
		},
		{
			name:  "Responses reasoning 负值钳 0",
			raw:   `{"input_tokens":10,"output_tokens":5,"output_tokens_details":{"reasoning_tokens":-3}}`,
			want:  Usage{PromptTokens: 10, CompletionTokens: 5},
			found: true,
		},
		{
			name:  "OpenAI 命名优先于 Anthropic",
			raw:   `{"prompt_tokens":100,"input_tokens":999,"completion_tokens":50,"output_tokens":999}`,
			want:  Usage{PromptTokens: 100, CompletionTokens: 50},
			found: true,
		},
		{
			name:  "无任何计数字段",
			raw:   `{"foo":1}`,
			found: false,
		},
		{
			name:  "非对象",
			raw:   `"usage"`,
			found: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, found := ParseUsage([]byte(tc.raw))
			if found != tc.found {
				t.Fatalf("found = %v, want %v", found, tc.found)
			}
			if found && got != tc.want {
				t.Errorf("Usage = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseAnthropicUsage补回缓存读取(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want Usage
	}{
		{
			name: "缓存读写分列",
			raw:  `{"input_tokens":80,"output_tokens":40,"cache_read_input_tokens":20,"cache_creation_input_tokens":10}`,
			want: Usage{PromptTokens: 100, CompletionTokens: 40, CachedTokens: 20, CacheCreationTokens: 10},
		},
		{
			name: "大量缓存读取不会把新鲜输入重复扣减",
			raw:  `{"input_tokens":13,"output_tokens":4,"cache_read_input_tokens":22000}`,
			want: Usage{PromptTokens: 22013, CompletionTokens: 4, CachedTokens: 22000},
		},
		{
			name: "OpenAI Responses 明细仍按总输入解释",
			raw:  `{"input_tokens":36,"output_tokens":4,"input_tokens_details":{"cached_tokens":6}}`,
			want: Usage{PromptTokens: 36, CompletionTokens: 4, CachedTokens: 6},
		},
		{
			name: "缓存写双档明细回填总量",
			raw:  `{"input_tokens":3,"output_tokens":1,"cache_creation":{"ephemeral_5m_input_tokens":11,"ephemeral_1h_input_tokens":20}}`,
			want: Usage{PromptTokens: 3, CompletionTokens: 1, CacheCreationTokens: 31, CacheCreation5mTokens: 11, CacheCreation1hTokens: 20},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := ParseAnthropicUsage([]byte(tt.raw))
			if !found || got != tt.want {
				t.Fatalf("Usage = %+v, found=%v，期望 %+v", got, found, tt.want)
			}
		})
	}
}

func TestParseGeminiTranslatedOpenAIUsage补回思考与工具提示(t *testing.T) {
	raw := []byte(`{"prompt_tokens":100,"completion_tokens":20,"total_tokens":155,
		"prompt_tokens_details":{"cached_tokens":40},
		"completion_tokens_details":{"reasoning_tokens":30}}`)
	got, found := ParseGeminiTranslatedOpenAIUsage(raw)
	want := Usage{PromptTokens: 105, CompletionTokens: 50, CachedTokens: 40}
	if !found || got != want {
		t.Fatalf("Usage = %+v, found=%v，期望 %+v", got, found, want)
	}

	// 原生 OpenAI 的 completion_tokens 已包含 reasoning，通用解析器不得套用 CPA 补偿。
	generic, found := ParseUsage(raw)
	genericWant := Usage{PromptTokens: 100, CompletionTokens: 20, CachedTokens: 40}
	if !found || generic != genericWant {
		t.Fatalf("通用 Usage = %+v, found=%v，期望 %+v", generic, found, genericWant)
	}
}

func TestParseGeminiUsage补齐特殊用量(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want Usage
	}{
		{
			name: "缓存读取工具提示与思考分列",
			raw:  `{"promptTokenCount":100,"toolUsePromptTokenCount":5,"candidatesTokenCount":20,"thoughtsTokenCount":30,"cachedContentTokenCount":40,"totalTokenCount":155}`,
			want: Usage{PromptTokens: 105, CompletionTokens: 50, CachedTokens: 40},
		},
		{
			name: "仅有总量时补回输出",
			raw:  `{"promptTokenCount":10,"toolUsePromptTokenCount":5,"totalTokenCount":18}`,
			want: Usage{PromptTokens: 15, CompletionTokens: 3},
		},
		{
			name: "已有部分输出时总量补足缺失明细",
			raw:  `{"promptTokenCount":100,"candidatesTokenCount":20,"totalTokenCount":150}`,
			want: Usage{PromptTokens: 100, CompletionTokens: 50},
		},
		{
			name: "负数钳零",
			raw:  `{"promptTokenCount":-10,"candidatesTokenCount":-2,"thoughtsTokenCount":-3,"cachedContentTokenCount":-4}`,
			want: Usage{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := ParseGeminiUsage([]byte(tt.raw))
			if !found || got != tt.want {
				t.Fatalf("Usage = %+v, found=%v，期望 %+v", got, found, tt.want)
			}
		})
	}
}

func TestParseTranslatedGeminiUsage按来源区分口径(t *testing.T) {
	openAIRaw := []byte(`{"promptTokenCount":100,"candidatesTokenCount":50,
		"thoughtsTokenCount":30,"cachedContentTokenCount":40,"totalTokenCount":150}`)
	got, found := ParseOpenAITranslatedGeminiUsage(openAIRaw)
	want := Usage{PromptTokens: 100, CompletionTokens: 50, CachedTokens: 40}
	if !found || got != want {
		t.Fatalf("OpenAI -> Gemini Usage = %+v, found=%v，期望 %+v", got, found, want)
	}

	claudeRaw := []byte(`{"promptTokenCount":13,"candidatesTokenCount":4,
		"thoughtsTokenCount":2,"cachedContentTokenCount":22000,"totalTokenCount":17}`)
	got, found = ParseClaudeTranslatedGeminiUsage(claudeRaw)
	want = Usage{PromptTokens: 22013, CompletionTokens: 4, CachedTokens: 22000}
	if !found || got != want {
		t.Fatalf("Claude -> Gemini Usage = %+v, found=%v，期望 %+v", got, found, want)
	}
}

func TestExtractGeminiUsage兼容原生与包装响应(t *testing.T) {
	tests := []struct {
		name string
		data string
		want Usage
	}{
		{
			name: "原生响应",
			data: `{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":20,
				"thoughtsTokenCount":30,"cachedContentTokenCount":40,"totalTokenCount":150}}`,
			want: Usage{PromptTokens: 100, CompletionTokens: 50, CachedTokens: 40},
		},
		{
			name: "Antigravity 包装响应",
			data: `{"response":{"usageMetadata":{"promptTokenCount":10,"toolUsePromptTokenCount":5,
				"candidatesTokenCount":2,"totalTokenCount":17}}}`,
			want: Usage{PromptTokens: 15, CompletionTokens: 2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := ExtractGeminiUsage([]byte(tt.data))
			if !found || got != tt.want {
				t.Fatalf("Usage = %+v, found=%v，期望 %+v", got, found, tt.want)
			}
		})
	}
}

func TestExtractUsage(t *testing.T) {
	cases := []struct {
		name  string
		data  string
		want  Usage
		found bool
	}{
		{
			name:  "顶层 usage",
			data:  `{"id":"x","usage":{"prompt_tokens":10,"completion_tokens":5}}`,
			want:  Usage{PromptTokens: 10, CompletionTokens: 5},
			found: true,
		},
		{name: "usage 为 null", data: `{"id":"x","usage":null}`, found: false},
		{name: "无 usage 字段", data: `{"id":"x"}`, found: false},
		{name: "非 JSON", data: `not-json`, found: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, found := ExtractUsage([]byte(tc.data))
			if found != tc.found {
				t.Fatalf("found = %v, want %v", found, tc.found)
			}
			if found && got != tc.want {
				t.Errorf("Usage = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestExtractAnthropicUsage兼容流式嵌套与顶层(t *testing.T) {
	tests := []struct {
		name string
		data string
		want Usage
	}{
		{
			name: "message_start 嵌套用量",
			data: `{"type":"message_start","message":{"usage":{"input_tokens":13,"output_tokens":1,"cache_read_input_tokens":22000,"cache_creation_input_tokens":31}}}`,
			want: Usage{PromptTokens: 22013, CompletionTokens: 1, CachedTokens: 22000, CacheCreationTokens: 31},
		},
		{
			name: "message_delta 顶层用量",
			data: `{"type":"message_delta","usage":{"output_tokens":4}}`,
			want: Usage{CompletionTokens: 4},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := ExtractAnthropicUsage([]byte(tt.data))
			if !found || got != tt.want {
				t.Fatalf("Usage = %+v, found=%v，期望 %+v", got, found, tt.want)
			}
		})
	}
}

// TestExtractResponsesUsage Responses 流式 usage 提取：优先 response.usage（completed 事件），
// 无 response 包装回退顶层 usage，缺失/null/非 JSON 返回 false。
func TestExtractResponsesUsage(t *testing.T) {
	cases := []struct {
		name  string
		data  string
		want  Usage
		found bool
	}{
		{
			name: "completed 事件 response.usage 嵌套",
			data: `{"type":"response.completed","response":{"id":"resp_1","model":"gpt-4.1",` +
				`"usage":{"input_tokens":36,"input_tokens_details":{"cached_tokens":6},` +
				`"output_tokens":87,"output_tokens_details":{"reasoning_tokens":12},"total_tokens":123}}}`,
			want:  Usage{PromptTokens: 36, CompletionTokens: 87, CachedTokens: 6},
			found: true,
		},
		{
			name:  "无 response 包装回退顶层 usage",
			data:  `{"usage":{"input_tokens":10,"output_tokens":5}}`,
			want:  Usage{PromptTokens: 10, CompletionTokens: 5},
			found: true,
		},
		{
			name:  "delta 事件无 usage",
			data:  `{"type":"response.output_text.delta","delta":"Hello"}`,
			found: false,
		},
		{
			name:  "response 存在但 usage 为 null 且无顶层回退",
			data:  `{"response":{"id":"resp_1","usage":null}}`,
			found: false,
		},
		{
			name:  "response.usage 为 null 时回退顶层 usage",
			data:  `{"response":{"usage":null},"usage":{"input_tokens":7,"output_tokens":3}}`,
			want:  Usage{PromptTokens: 7, CompletionTokens: 3},
			found: true,
		},
		{name: "非 JSON", data: `not-json`, found: false},
		{name: "空对象", data: `{}`, found: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, found := ExtractResponsesUsage([]byte(tc.data))
			if found != tc.found {
				t.Fatalf("found = %v, want %v", found, tc.found)
			}
			if found && got != tc.want {
				t.Errorf("Usage = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestParseUsageClampsNegative 上游为不可信第三方：负数 token 计数一律钳 0，
// 防止 cached 为负虚增 input 费用、completion 为负写出负成本。
func TestParseUsageClampsNegative(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want Usage
	}{
		{
			name: "负 cached 钳 0（不虚增 input）",
			raw:  `{"prompt_tokens":1000,"completion_tokens":10,"prompt_tokens_details":{"cached_tokens":-500}}`,
			want: Usage{PromptTokens: 1000, CompletionTokens: 10, CachedTokens: 0},
		},
		{
			name: "负 completion 钳 0",
			raw:  `{"prompt_tokens":100,"completion_tokens":-42}`,
			want: Usage{PromptTokens: 100, CompletionTokens: 0},
		},
		{
			name: "负 prompt 钳 0",
			raw:  `{"prompt_tokens":-1,"completion_tokens":5}`,
			want: Usage{PromptTokens: 0, CompletionTokens: 5},
		},
		{
			name: "Anthropic 命名负值同样钳 0",
			raw:  `{"input_tokens":-10,"output_tokens":-20,"cache_read_input_tokens":-30,"cache_creation_input_tokens":-40}`,
			want: Usage{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, found := ParseUsage([]byte(tc.raw))
			if !found {
				t.Fatal("应解析成功")
			}
			if got != tc.want {
				t.Errorf("Usage = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestIncludeUsageRequested 判定客户端是否显式请求 usage chunk（决定透传层是否下发）。
func TestIncludeUsageRequested(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"未带 stream_options", `{"model":"m","stream":true}`, false},
		{"显式 true", `{"model":"m","stream_options":{"include_usage":true}}`, true},
		{"显式 false", `{"model":"m","stream_options":{"include_usage":false}}`, false},
		{"空对象", `{"model":"m","stream_options":{}}`, false},
		{"null", `{"model":"m","stream_options":null}`, false},
		{"非对象", `{"model":"m","stream_options":5}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := ParseChatRequest([]byte(tc.body))
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if got := req.IncludeUsageRequested(); got != tc.want {
				t.Errorf("IncludeUsageRequested = %v, want %v", got, tc.want)
			}
		})
	}
}
