package dto

import (
	"encoding/json"
	"reflect"
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
			want:  Usage{PromptTokens: 36, CompletionTokens: 87, CachedTokens: 6, ReasoningTokens: 12},
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
			want:  Usage{PromptTokens: 10, CompletionTokens: 5, ReasoningTokens: 0},
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
			want:  Usage{PromptTokens: 36, CompletionTokens: 87, CachedTokens: 6, ReasoningTokens: 12},
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
