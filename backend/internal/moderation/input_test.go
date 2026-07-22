package moderation

import (
	"bytes"
	"mime/multipart"
	"testing"
)

// 输入抽取只取「最后一条用户消息」：Agent 工具循环结束于 tool/assistant 时
// 应整体跳过，不回溯历史消息（历史在此前请求已审过）。
func TestExtractInput(t *testing.T) {
	tests := []struct {
		name       string
		protocol   string
		body       string
		wantText   string
		wantImages int
	}{
		{
			name:     "anthropic 工具循环末尾非用户文本则跳过",
			protocol: ProtocolAnthropicMessages,
			body: `{"messages":[
				{"role":"user","content":"调用一下天气工具"},
				{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"weather","input":{}}]},
				{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"晴 25 度"}]}
			]}`,
			wantText: "",
		},
		{
			name:     "anthropic 首轮抽取用户消息",
			protocol: ProtocolAnthropicMessages,
			body:     `{"messages":[{"role":"user","content":"Q1"}]}`,
			wantText: "Q1",
		},
		{
			name:     "anthropic 多轮取最后一条用户消息",
			protocol: ProtocolAnthropicMessages,
			body: `{"messages":[
				{"role":"user","content":"Q1"},
				{"role":"assistant","content":"A1"},
				{"role":"user","content":"Q2"}
			]}`,
			wantText: "Q2",
		},
		{
			name:     "anthropic system-reminder 文本剔除",
			protocol: ProtocolAnthropicMessages,
			body: `{"messages":[
				{"role":"user","content":[{"type":"text","text":"<system-reminder>internal</system-reminder>"},{"type":"text","text":"真实输入"}]}
			]}`,
			wantText: "真实输入",
		},
		{
			name:     "anthropic 图片抽取",
			protocol: ProtocolAnthropicMessages,
			body: `{"messages":[
				{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGVsbG8="}},{"type":"text","text":"看图"}]}
			]}`,
			wantText:   "看图",
			wantImages: 1,
		},
		{
			name:     "openai chat 工具循环跳过",
			protocol: ProtocolOpenAIChat,
			body: `{"messages":[
				{"role":"system","content":"sys"},
				{"role":"user","content":"列出我的订单"},
				{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"orders","arguments":"{}"}}]},
				{"role":"tool","tool_call_id":"c1","content":"[]"}
			]}`,
			wantText: "",
		},
		{
			name:     "openai chat 多轮取最后一条用户消息",
			protocol: ProtocolOpenAIChat,
			body: `{"messages":[
				{"role":"user","content":"Q1"},
				{"role":"assistant","content":"A1"},
				{"role":"user","content":"Q2"}
			]}`,
			wantText: "Q2",
		},
		{
			name:     "openai chat image_url 抽取",
			protocol: ProtocolOpenAIChat,
			body: `{"messages":[
				{"role":"user","content":[{"type":"text","text":"看图"},{"type":"image_url","image_url":{"url":"https://example.com/a.png"}}]}
			]}`,
			wantText:   "看图",
			wantImages: 1,
		},
		{
			name:     "gemini 工具循环跳过",
			protocol: ProtocolGemini,
			body: `{"contents":[
				{"role":"user","parts":[{"text":"查询天气"}]},
				{"role":"model","parts":[{"functionCall":{"name":"weather","args":{}}}]},
				{"role":"user","parts":[{"functionResponse":{"name":"weather","response":{"temp":25}}}]}
			]}`,
			wantText: "",
		},
		{
			name:     "gemini 多轮取最后一条用户消息",
			protocol: ProtocolGemini,
			body: `{"contents":[
				{"role":"user","parts":[{"text":"Q1"}]},
				{"role":"model","parts":[{"text":"A1"}]},
				{"role":"user","parts":[{"text":"Q2"}]}
			]}`,
			wantText: "Q2",
		},
		{
			name:     "gemini inline_data 图片抽取",
			protocol: ProtocolGemini,
			body: `{"contents":[
				{"role":"user","parts":[{"text":"看图"},{"inline_data":{"mime_type":"image/png","data":"aGVsbG8="}}]}
			]}`,
			wantText:   "看图",
			wantImages: 1,
		},
		{
			name:     "responses 工具循环跳过",
			protocol: ProtocolOpenAIResponses,
			body: `{"input":[
				{"type":"message","role":"user","content":[{"type":"input_text","text":"运行测试"}]},
				{"type":"function_call","call_id":"c1","name":"run_tests","arguments":"{}"},
				{"type":"function_call_output","call_id":"c1","output":"all passed"}
			]}`,
			wantText: "",
		},
		{
			name:     "responses 末尾用户消息抽取",
			protocol: ProtocolOpenAIResponses,
			body: `{"input":[
				{"type":"message","role":"user","content":[{"type":"input_text","text":"first"}]},
				{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]},
				{"type":"message","role":"user","content":[{"type":"input_text","text":"latest"}]}
			]}`,
			wantText: "latest",
		},
		{
			name:     "responses 末尾 assistant 跳过",
			protocol: ProtocolOpenAIResponses,
			body: `{"input":[
				{"type":"message","role":"user","content":[{"type":"input_text","text":"q1"}]},
				{"type":"message","role":"assistant","content":[{"type":"output_text","text":"a1"}]}
			]}`,
			wantText: "",
		},
		{
			name:     "responses 纯字符串 input",
			protocol: ProtocolOpenAIResponses,
			body:     `{"input":"直接文本"}`,
			wantText: "直接文本",
		},
		{
			name:     "images prompt 抽取",
			protocol: ProtocolOpenAIImages,
			body:     `{"model":"gpt-image-2","prompt":"画一只猫"}`,
			wantText: "画一只猫",
		},
		{
			name:     "video JSON prompt 抽取",
			protocol: ProtocolOpenAIVideo,
			body:     `{"model":"sora-2","prompt":"日落海边","seconds":8}`,
			wantText: "日落海边",
		},
		{
			name:     "alpha search query 抽取",
			protocol: ProtocolOpenAISearch,
			body:     `{"model":"gpt-5","query":"如何搜索敏感内容"}`,
			wantText: "如何搜索敏感内容",
		},
		{
			name:     "alpha search 无已知字段放行",
			protocol: ProtocolOpenAISearch,
			body:     `{"model":"gpt-5","topic":{"x":1}}`,
			wantText: "",
		},
		{
			name:     "suno music 多字段拼接",
			protocol: ProtocolSuno,
			body:     `{"prompt":"[Verse] 歌词内容","tags":"pop","title":"我的歌","mv":"chirp-v4"}`,
			wantText: "[Verse] 歌词内容\n我的歌\npop",
		},
		{
			name:     "空 body 放行",
			protocol: ProtocolOpenAIChat,
			body:     ``,
			wantText: "",
		},
		{
			name:     "非 JSON body 放行",
			protocol: ProtocolOpenAIChat,
			body:     `not json`,
			wantText: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractInput(tt.protocol, "", []byte(tt.body))
			want := normalizeText(tt.wantText)
			if got.Text != want {
				t.Fatalf("Text = %q, want %q", got.Text, want)
			}
			if len(got.Images) != tt.wantImages {
				t.Fatalf("Images = %d, want %d", len(got.Images), tt.wantImages)
			}
		})
	}
}

func TestExtractInputVideoMultipart(t *testing.T) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("model", "sora-2")
	_ = w.WriteField("prompt", "海上日出")
	_ = w.Close()

	got := ExtractInput(ProtocolOpenAIVideo, w.FormDataContentType(), buf.Bytes())
	if got.Text != "海上日出" {
		t.Fatalf("Text = %q, want 海上日出", got.Text)
	}

	// images edits 的 multipart 体走同一 prompt 抽取路径。
	got = ExtractInput(ProtocolOpenAIImages, w.FormDataContentType(), buf.Bytes())
	if got.Text != "海上日出" {
		t.Fatalf("images multipart Text = %q, want 海上日出", got.Text)
	}

	// 坏 multipart 体：安全放行（空输入）。
	got = ExtractInput(ProtocolOpenAIVideo, "multipart/form-data; boundary=xxx", []byte("broken"))
	if !got.IsEmpty() {
		t.Fatalf("broken multipart 应返回空输入")
	}
}

func TestInputHashStable(t *testing.T) {
	a := Input{Text: "hello", Images: []string{"data:image/png;base64,aGk="}}
	b := Input{Text: "hello", Images: []string{"data:image/png;base64,aGk="}}
	if a.Hash() != b.Hash() {
		t.Fatal("同输入哈希应稳定")
	}
	c := Input{Text: "hello2"}
	if a.Hash() == c.Hash() {
		t.Fatal("不同输入哈希应不同")
	}
}
