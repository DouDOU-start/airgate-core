package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
)

func TestChatCompletionsURL(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		want    string
	}{
		{"不带 /v1", "https://api.example.com", "https://api.example.com/v1/chat/completions"},
		{"末尾斜杠归一", "https://api.example.com/", "https://api.example.com/v1/chat/completions"},
		{"已含 /v1 不重复拼", "https://api.example.com/v1", "https://api.example.com/v1/chat/completions"},
		{"已含 /v1/ 归一后不重复拼", "https://api.example.com/v1/", "https://api.example.com/v1/chat/completions"},
		{"带路径前缀", "https://gw.example.com/openai", "https://gw.example.com/openai/v1/chat/completions"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ChatCompletionsURL(tc.baseURL); got != tc.want {
				t.Errorf("ChatCompletionsURL(%q) = %q, want %q", tc.baseURL, got, tc.want)
			}
		})
	}
}

func TestResponsesURL(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		want    string
	}{
		{"不带 /v1", "https://api.example.com", "https://api.example.com/v1/responses"},
		{"末尾斜杠归一", "https://api.example.com/", "https://api.example.com/v1/responses"},
		{"已含 /v1 不重复拼", "https://api.example.com/v1", "https://api.example.com/v1/responses"},
		{"已含 /v1/ 归一后不重复拼", "https://api.example.com/v1/", "https://api.example.com/v1/responses"},
		{"带路径前缀", "https://gw.example.com/openai", "https://gw.example.com/openai/v1/responses"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResponsesURL(tc.baseURL); got != tc.want {
				t.Errorf("ResponsesURL(%q) = %q, want %q", tc.baseURL, got, tc.want)
			}
		})
	}
}

func TestCompactURL(t *testing.T) {
	for baseURL, want := range map[string]string{
		"https://api.example.com":    "https://api.example.com/v1/responses/compact",
		"https://api.example.com/v1": "https://api.example.com/v1/responses/compact",
	} {
		if got := CompactURL(baseURL); got != want {
			t.Errorf("CompactURL(%q) = %q, want %q", baseURL, got, want)
		}
	}
}

func TestBuildRequestCompactPreservesContractAndCodexHeaders(t *testing.T) {
	req, err := dto.ParseChatRequest([]byte(`{"model":"gpt-5","input":[{"type":"message"}],"tools":[{"type":"function"}],"unknown":{"keep":true},"stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	info := buildInfo(func(i *adaptor.RelayInfo) {
		i.Endpoint = adaptor.EndpointCompact
		i.RequestModel = "gpt-5"
		i.UpstreamModel = "gpt-5-upstream"
		i.Stream = false
		i.RequestHeaders = http.Header{
			"Authorization":       {"Bearer client-secret"},
			"Connection":          {"close"},
			"X-Codex-Turn-State":  {"turn-1"},
			"X-Client-Request-Id": {"request-1"},
		}
	})
	httpReq, err := (Adaptor{}).BuildRequest(context.Background(), info, req)
	if err != nil {
		t.Fatal(err)
	}
	if got := httpReq.URL.String(); got != "https://api.example.com/v1/responses/compact" {
		t.Fatalf("URL = %q", got)
	}
	if got := httpReq.Header.Get("Authorization"); got != "Bearer sk-upstream" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := httpReq.Header.Get("X-Codex-Turn-State"); got != "turn-1" {
		t.Fatalf("X-Codex-Turn-State = %q", got)
	}
	if got := httpReq.Header.Get("X-Client-Request-Id"); got != "request-1" {
		t.Fatalf("X-Client-Request-Id = %q", got)
	}
	if got := httpReq.Header.Get("Connection"); got != "" {
		t.Fatalf("hop-by-hop Connection leaked: %q", got)
	}
	body, _ := io.ReadAll(httpReq.Body)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["model"]) != `"gpt-5-upstream"` || len(fields["input"]) == 0 || len(fields["tools"]) == 0 || len(fields["unknown"]) == 0 {
		t.Fatalf("compact body fields were not preserved: %s", body)
	}
	if _, exists := fields["stream_options"]; exists {
		t.Fatalf("compact body must not contain stream_options: %s", body)
	}
}

func TestImagesURLs(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		wantGen string
		wantEdt string
	}{
		{"不带 /v1", "https://api.example.com", "https://api.example.com/v1/images/generations", "https://api.example.com/v1/images/edits"},
		{"已含 /v1 不重复拼", "https://api.example.com/v1", "https://api.example.com/v1/images/generations", "https://api.example.com/v1/images/edits"},
		{"带路径前缀", "https://gw.example.com/openai", "https://gw.example.com/openai/v1/images/generations", "https://gw.example.com/openai/v1/images/edits"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ImagesGenerationsURL(tc.baseURL); got != tc.wantGen {
				t.Errorf("ImagesGenerationsURL(%q) = %q, want %q", tc.baseURL, got, tc.wantGen)
			}
			if got := ImagesEditsURL(tc.baseURL); got != tc.wantEdt {
				t.Errorf("ImagesEditsURL(%q) = %q, want %q", tc.baseURL, got, tc.wantEdt)
			}
		})
	}
}

// buildInfo 构造测试 RelayInfo。
func buildInfo(mutate ...func(*adaptor.RelayInfo)) *adaptor.RelayInfo {
	info := &adaptor.RelayInfo{
		ChannelKey: &registry.ChannelKeySnapshot{
			KeyID:   1,
			Type:    "openai_compatible",
			BaseURL: "https://api.example.com",
		},
		APIKey:        "sk-upstream",
		RequestModel:  "gpt-4o",
		UpstreamModel: "gpt-4o",
		Endpoint:      adaptor.EndpointChatCompletions,
	}
	for _, m := range mutate {
		m(info)
	}
	return info
}

func TestBuildRequestRewrite(t *testing.T) {
	cases := []struct {
		name string
		body string
		info *adaptor.RelayInfo
		want map[string]any
	}{
		{
			name: "model 重写",
			body: `{"model":"gpt-4o","messages":[]}`,
			info: buildInfo(func(i *adaptor.RelayInfo) { i.UpstreamModel = "gpt-4o-upstream" }),
			want: map[string]any{"model": "gpt-4o-upstream", "messages": []any{}},
		},
		{
			name: "param_override set 与 remove",
			body: `{"model":"gpt-4o","temperature":0.9,"top_p":0.5}`,
			info: buildInfo(func(i *adaptor.RelayInfo) {
				i.ChannelKey.ParamOverride = map[string]any{
					"temperature": 0.1, // set：覆盖
					"top_p":       nil, // remove：删除
					"max_tokens":  float64(128),
				}
			}),
			want: map[string]any{"model": "gpt-4o", "temperature": 0.1, "max_tokens": float64(128)},
		},
		{
			name: "流式注入 include_usage",
			body: `{"model":"gpt-4o","stream":true}`,
			info: buildInfo(func(i *adaptor.RelayInfo) { i.Stream = true }),
			want: map[string]any{
				"model": "gpt-4o", "stream": true,
				"stream_options": map[string]any{"include_usage": true},
			},
		},
		{
			// 计费依赖尾部 usage chunk：客户端显式 include_usage=false 也强制改回 true，
			// 否则任意持 key 用户可零计费流式请求（透传层负责吞掉多出的 usage chunk）。
			name: "客户端显式 include_usage=false 强制覆盖为 true",
			body: `{"model":"gpt-4o","stream":true,"stream_options":{"include_usage":false}}`,
			info: buildInfo(func(i *adaptor.RelayInfo) { i.Stream = true }),
			want: map[string]any{
				"model": "gpt-4o", "stream": true,
				"stream_options": map[string]any{"include_usage": true},
			},
		},
		{
			name: "空 stream_options 对象注入 include_usage",
			body: `{"model":"gpt-4o","stream":true,"stream_options":{}}`,
			info: buildInfo(func(i *adaptor.RelayInfo) { i.Stream = true }),
			want: map[string]any{
				"model": "gpt-4o", "stream": true,
				"stream_options": map[string]any{"include_usage": true},
			},
		},
		{
			name: "stream_options 为 null 重建为对象",
			body: `{"model":"gpt-4o","stream":true,"stream_options":null}`,
			info: buildInfo(func(i *adaptor.RelayInfo) { i.Stream = true }),
			want: map[string]any{
				"model": "gpt-4o", "stream": true,
				"stream_options": map[string]any{"include_usage": true},
			},
		},
		{
			name: "stream_options 其他字段保留",
			body: `{"model":"gpt-4o","stream":true,"stream_options":{"include_usage":false,"future_opt":"x"}}`,
			info: buildInfo(func(i *adaptor.RelayInfo) { i.Stream = true }),
			want: map[string]any{
				"model": "gpt-4o", "stream": true,
				"stream_options": map[string]any{"include_usage": true, "future_opt": "x"},
			},
		},
		{
			name: "非流式不注入 include_usage",
			body: `{"model":"gpt-4o"}`,
			info: buildInfo(),
			want: map[string]any{"model": "gpt-4o"},
		},
		{
			name: "未知字段透传",
			body: `{"model":"gpt-4o","future_field":{"a":[1,"b",null]},"n":2}`,
			info: buildInfo(),
			want: map[string]any{
				"model":        "gpt-4o",
				"future_field": map[string]any{"a": []any{float64(1), "b", nil}},
				"n":            float64(2),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := dto.ParseChatRequest([]byte(tc.body))
			if err != nil {
				t.Fatalf("解析请求失败: %v", err)
			}
			httpReq, err := (Adaptor{}).BuildRequest(context.Background(), tc.info, req)
			if err != nil {
				t.Fatalf("BuildRequest 失败: %v", err)
			}
			sent, _ := io.ReadAll(httpReq.Body)
			var got map[string]any
			if err := json.Unmarshal(sent, &got); err != nil {
				t.Fatalf("上游请求体非 JSON: %v", err)
			}
			if !reflect.DeepEqual(normalizeJSON(t, tc.want), got) {
				t.Errorf("请求体 = %v, want %v", got, tc.want)
			}
		})
	}
}

// respInfo 构造 Responses 端点的测试 RelayInfo。
func respInfo(mutate ...func(*adaptor.RelayInfo)) *adaptor.RelayInfo {
	all := append([]func(*adaptor.RelayInfo){
		func(i *adaptor.RelayInfo) { i.Endpoint = adaptor.EndpointResponses },
	}, mutate...)
	return buildInfo(all...)
}

// TestBuildRequestResponses Responses 端点：URL 正确、绝不注入 stream_options、
// model 重写 + param_override 生效、未知字段（input/instructions/tools）透传。
func TestBuildRequestResponses(t *testing.T) {
	t.Run("URL 指向 /v1/responses", func(t *testing.T) {
		req, _ := dto.ParseChatRequest([]byte(`{"model":"gpt-4o","input":"hi"}`))
		httpReq, err := (Adaptor{}).BuildRequest(context.Background(), respInfo(), req)
		if err != nil {
			t.Fatalf("BuildRequest 失败: %v", err)
		}
		if got := httpReq.URL.String(); got != "https://api.example.com/v1/responses" {
			t.Errorf("URL = %q, want /v1/responses", got)
		}
	})

	t.Run("流式绝不注入 stream_options", func(t *testing.T) {
		req, _ := dto.ParseChatRequest([]byte(`{"model":"gpt-4o","input":"hi","stream":true}`))
		httpReq, err := (Adaptor{}).BuildRequest(context.Background(), respInfo(func(i *adaptor.RelayInfo) { i.Stream = true }), req)
		if err != nil {
			t.Fatalf("BuildRequest 失败: %v", err)
		}
		sent, _ := io.ReadAll(httpReq.Body)
		if strings.Contains(string(sent), "stream_options") {
			t.Errorf("Responses 流式请求体不得含 stream_options: %s", sent)
		}
		var got map[string]any
		if err := json.Unmarshal(sent, &got); err != nil {
			t.Fatalf("上游请求体非 JSON: %v", err)
		}
		if _, ok := got["stream_options"]; ok {
			t.Error("stream_options 字段不应存在")
		}
	})

	t.Run("model 重写 + param_override 生效", func(t *testing.T) {
		req, _ := dto.ParseChatRequest([]byte(`{"model":"gpt-4o","input":"hi","temperature":0.9,"top_p":0.5}`))
		info := respInfo(func(i *adaptor.RelayInfo) {
			i.UpstreamModel = "gpt-4o-upstream"
			i.ChannelKey.ParamOverride = map[string]any{
				"temperature": 0.1, // set：覆盖
				"top_p":       nil, // remove：删除
			}
		})
		httpReq, err := (Adaptor{}).BuildRequest(context.Background(), info, req)
		if err != nil {
			t.Fatalf("BuildRequest 失败: %v", err)
		}
		sent, _ := io.ReadAll(httpReq.Body)
		var got map[string]any
		if err := json.Unmarshal(sent, &got); err != nil {
			t.Fatalf("上游请求体非 JSON: %v", err)
		}
		want := map[string]any{"model": "gpt-4o-upstream", "input": "hi", "temperature": 0.1}
		if !reflect.DeepEqual(normalizeJSON(t, want), got) {
			t.Errorf("请求体 = %v, want %v", got, want)
		}
	})

	t.Run("未知字段透传 round-trip", func(t *testing.T) {
		body := `{"model":"gpt-4o","input":[{"role":"user","content":"hi"}],` +
			`"instructions":"be concise","tools":[{"type":"web_search"}],"max_output_tokens":256,` +
			`"reasoning":{"effort":"low"}}`
		req, _ := dto.ParseChatRequest([]byte(body))
		httpReq, err := (Adaptor{}).BuildRequest(context.Background(), respInfo(), req)
		if err != nil {
			t.Fatalf("BuildRequest 失败: %v", err)
		}
		sent, _ := io.ReadAll(httpReq.Body)
		var want, got map[string]any
		if err := json.Unmarshal([]byte(body), &want); err != nil {
			t.Fatalf("unmarshal want: %v", err)
		}
		if err := json.Unmarshal(sent, &got); err != nil {
			t.Fatalf("unmarshal got: %v", err)
		}
		if !reflect.DeepEqual(want, got) {
			t.Errorf("未知字段未透传:\nwant %v\ngot  %v", want, got)
		}
	})
}

// TestBuildRequestImagesGenerations 生图 JSON 端点：URL 正确、model 重写 +
// param_override 生效、绝不注入 stream_options（Images API 无此参数）。
func TestBuildRequestImagesGenerations(t *testing.T) {
	req, _ := dto.ParseChatRequest([]byte(`{"model":"gpt-4o","prompt":"a cat","n":2,"size":"1024x1024"}`))
	info := buildInfo(func(i *adaptor.RelayInfo) {
		i.Endpoint = adaptor.EndpointImagesGenerations
		i.UpstreamModel = "gpt-image-upstream"
	})
	httpReq, err := (Adaptor{}).BuildRequest(context.Background(), info, req)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if got := httpReq.URL.String(); got != "https://api.example.com/v1/images/generations" {
		t.Errorf("URL = %q, want /v1/images/generations", got)
	}
	sent, _ := io.ReadAll(httpReq.Body)
	var got map[string]any
	if err := json.Unmarshal(sent, &got); err != nil {
		t.Fatalf("上游请求体非 JSON: %v", err)
	}
	want := map[string]any{"model": "gpt-image-upstream", "prompt": "a cat", "n": float64(2), "size": "1024x1024"}
	if !reflect.DeepEqual(normalizeJSON(t, want), got) {
		t.Errorf("请求体 = %v, want %v", got, want)
	}
	if _, ok := got["stream_options"]; ok {
		t.Error("Images 请求体不得含 stream_options")
	}
}

// TestBuildRequestImagesEdits 图像编辑 multipart 端点：RawBody 原始字节 +
// 原 Content-Type（含 boundary）原样直发，不重组 multipart。
func TestBuildRequestImagesEdits(t *testing.T) {
	rawBody := []byte("--BOUND\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\ngpt-image-1\r\n--BOUND--\r\n")
	rawCT := `multipart/form-data; boundary=BOUND`
	req, _ := dto.ParseChatRequest([]byte(`{}`))
	req.Model = "gpt-image-1"
	info := buildInfo(func(i *adaptor.RelayInfo) {
		i.Endpoint = adaptor.EndpointImagesEdits
		i.RawBody = rawBody
		i.RawContentType = rawCT
	})

	httpReq, err := (Adaptor{}).BuildRequest(context.Background(), info, req)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if got := httpReq.URL.String(); got != "https://api.example.com/v1/images/edits" {
		t.Errorf("URL = %q, want /v1/images/edits", got)
	}
	if got := httpReq.Header.Get("Content-Type"); got != rawCT {
		t.Errorf("Content-Type = %q, want 原样透传 %q", got, rawCT)
	}
	sent, _ := io.ReadAll(httpReq.Body)
	if string(sent) != string(rawBody) {
		t.Errorf("multipart 体被改写:\ngot  %q\nwant %q", sent, rawBody)
	}

}

// TestBuildRequestImagesEditsJSON verifies the official Codex CLI image-edit
// JSON shape (images[].image_url) remains transparent while model_mapping is
// applied by the OpenAI adaptor.
func TestBuildRequestImagesEditsJSON(t *testing.T) {
	body := []byte(`{"model":"gpt-image-1","prompt":"make it blue",` +
		`"images":[{"image_url":"data:image/png;base64,QUJD"}],` +
		`"mask":{"image_url":"data:image/png;base64,REVG"},` +
		`"unknown":{"keep":true}}`)
	req, err := dto.ParseChatRequest(body)
	if err != nil {
		t.Fatalf("ParseChatRequest: %v", err)
	}
	info := buildInfo(func(i *adaptor.RelayInfo) {
		i.Endpoint = adaptor.EndpointImagesEdits
		i.RequestModel = "gpt-image-1"
		i.UpstreamModel = "gpt-image-upstream"
	})
	httpReq, err := (Adaptor{}).BuildRequest(context.Background(), info, req)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if got := httpReq.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	sent, err := io.ReadAll(httpReq.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(sent, &got); err != nil {
		t.Fatalf("upstream body is not JSON: %v", err)
	}
	if got["model"] != "gpt-image-upstream" {
		t.Errorf("model = %v, want gpt-image-upstream", got["model"])
	}
	if _, ok := got["images"]; !ok {
		t.Fatal("images field was not preserved")
	}
	if _, ok := got["mask"]; !ok {
		t.Fatal("mask field was not preserved")
	}
	if unknown, ok := got["unknown"].(map[string]any); !ok || unknown["keep"] != true {
		t.Errorf("unknown field was not preserved: %v", got["unknown"])
	}
}

func TestBuildRequestImagesEditsRawJSON(t *testing.T) {
	rawBody := []byte(`{"model":"gpt-image-1","prompt":"x","images":[]}`)
	req, _ := dto.ParseChatRequest([]byte(`{"model":"gpt-image-1"}`))
	info := buildInfo(func(i *adaptor.RelayInfo) {
		i.Endpoint = adaptor.EndpointImagesEdits
		i.RequestModel = "gpt-image-1"
		i.UpstreamModel = "gpt-image-upstream"
		i.RawBody = rawBody
		i.RawContentType = "application/json; charset=utf-8"
	})
	httpReq, err := (Adaptor{}).BuildRequest(context.Background(), info, req)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if got := httpReq.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want normalized application/json", got)
	}
	sent, _ := io.ReadAll(httpReq.Body)
	if !strings.Contains(string(sent), `"gpt-image-upstream"`) {
		t.Errorf("raw JSON model mapping missing: %s", sent)
	}
}

// TestParseNonStreamResponseImages 图像响应表驱动：
// gpt-image 系 token usage（input/output_tokens + input_tokens_details.image/text_tokens）
// 归一化到 dto.Usage；data 数组长度写入 usage.Calls（产出张数）；两者可并存。
func TestParseNonStreamResponseImages(t *testing.T) {
	cases := []struct {
		name string
		body string
		want *dto.Usage
	}{
		{
			name: "gpt-image usage + data 张数并存",
			body: `{"created":1,"data":[{"b64_json":"QUJD"},{"b64_json":"REVG"}],` +
				`"usage":{"input_tokens":150,"output_tokens":4160,"total_tokens":4310,` +
				`"input_tokens_details":{"image_tokens":100,"text_tokens":50}}}`,
			want: &dto.Usage{PromptTokens: 150, CompletionTokens: 4160, Calls: 2},
		},
		{
			name: "无 usage（DALL·E 系）：仅张数",
			body: `{"created":1,"data":[{"url":"https://x/1.png"},{"url":"https://x/2.png"},{"url":"https://x/3.png"}]}`,
			want: &dto.Usage{Calls: 3},
		},
		{
			name: "空 data 且无 usage：无 usage（计费侧按 1 次兜底）",
			body: `{"created":1,"data":[]}`,
			want: nil,
		},
		{
			name: "usage 带 cached 明细归一化",
			body: `{"data":[{"b64_json":"QUJD"}],` +
				`"usage":{"input_tokens":100,"output_tokens":200,"input_tokens_details":{"cached_tokens":30}}}`,
			want: &dto.Usage{PromptTokens: 100, CompletionTokens: 200, CachedTokens: 30, Calls: 1},
		},
		{
			name: "顶层 size/quality 提取（gpt-image 系，供分辨率价表计费）",
			body: `{"created":1,"data":[{"b64_json":"QUJD"},{"b64_json":"REVG"}],"size":"1024x1536","quality":"high",` +
				`"usage":{"input_tokens":150,"output_tokens":4160}}`,
			want: &dto.Usage{PromptTokens: 150, CompletionTokens: 4160, Calls: 2, ImageSize: "1024x1536", ImageQuality: "high"},
		},
		{
			name: "size/quality 缺失或为 null 留空（dall-e 系）",
			body: `{"created":1,"data":[{"url":"https://x/1.png"}],"quality":null}`,
			want: &dto.Usage{Calls: 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info := buildInfo(func(i *adaptor.RelayInfo) { i.Endpoint = adaptor.EndpointImagesGenerations })
			out, usage := (Adaptor{}).ParseNonStreamResponse(info, []byte(tc.body))
			if tc.want == nil {
				if usage != nil {
					t.Fatalf("usage = %+v, want nil", usage)
				}
			} else if usage == nil || *usage != *tc.want {
				t.Fatalf("usage = %+v, want %+v", usage, tc.want)
			}
			if len(out) == 0 {
				t.Error("响应体不得为空")
			}
		})
	}

	t.Run("chat 端点不提取 data 张数（Calls 恒 0）", func(t *testing.T) {
		info := buildInfo() // 默认 chat_completions
		body := `{"data":[{"x":1},{"x":2}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`
		_, usage := (Adaptor{}).ParseNonStreamResponse(info, []byte(body))
		if usage == nil || usage.Calls != 0 {
			t.Errorf("usage = %+v, want Calls=0（非图像端点不计张数）", usage)
		}
	})
}

// normalizeJSON 经一轮 JSON round-trip 归一数值类型，便于 DeepEqual。
func normalizeJSON(t *testing.T, v map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func TestBuildRequestHeaders(t *testing.T) {
	info := buildInfo(func(i *adaptor.RelayInfo) {
		i.ChannelKey.HeaderOverride = map[string]string{
			"X-Custom":      "v1",
			"Authorization": "Bearer overridden", // header_override 优先于默认 Bearer
		}
	})
	req, _ := dto.ParseChatRequest([]byte(`{"model":"gpt-4o"}`))
	httpReq, err := (Adaptor{}).BuildRequest(context.Background(), info, req)
	if err != nil {
		t.Fatalf("BuildRequest 失败: %v", err)
	}
	if got := httpReq.Header.Get("Authorization"); got != "Bearer overridden" {
		t.Errorf("Authorization = %q, want header_override 优先", got)
	}
	if got := httpReq.Header.Get("X-Custom"); got != "v1" {
		t.Errorf("X-Custom = %q", got)
	}
	if got := httpReq.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := httpReq.URL.String(); got != "https://api.example.com/v1/chat/completions" {
		t.Errorf("URL = %q", got)
	}
}

func TestParseNonStreamResponse(t *testing.T) {
	info := buildInfo(func(i *adaptor.RelayInfo) { i.UpstreamModel = "gpt-4o-upstream" })

	cases := []struct {
		name      string
		body      string
		wantModel string
		wantUsage *dto.Usage
	}{
		{
			name:      "usage 提取 + model 回写",
			body:      `{"id":"x","model":"gpt-4o-upstream","usage":{"prompt_tokens":100,"completion_tokens":50,"prompt_tokens_details":{"cached_tokens":20}}}`,
			wantModel: "gpt-4o",
			wantUsage: &dto.Usage{PromptTokens: 100, CompletionTokens: 50, CachedTokens: 20},
		},
		{
			name:      "无 usage",
			body:      `{"id":"x","model":"gpt-4o-upstream"}`,
			wantModel: "gpt-4o",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, usage := (Adaptor{}).ParseNonStreamResponse(info, []byte(tc.body))
			var fields map[string]any
			if err := json.Unmarshal(out, &fields); err != nil {
				t.Fatalf("响应非 JSON: %v", err)
			}
			if fields["model"] != tc.wantModel {
				t.Errorf("model = %v, want %v", fields["model"], tc.wantModel)
			}
			if tc.wantUsage == nil {
				if usage != nil {
					t.Errorf("usage = %+v, want nil", usage)
				}
			} else if usage == nil || *usage != *tc.wantUsage {
				t.Errorf("usage = %+v, want %+v", usage, tc.wantUsage)
			}
		})
	}

	t.Run("非 JSON 响应原样返回", func(t *testing.T) {
		raw := []byte("not-json")
		out, usage := (Adaptor{}).ParseNonStreamResponse(info, raw)
		if string(out) != "not-json" || usage != nil {
			t.Errorf("非 JSON 响应应原样返回")
		}
	})

	t.Run("realtime call response is returned byte-for-byte", func(t *testing.T) {
		realtimeInfo := buildInfo(func(i *adaptor.RelayInfo) {
			i.Endpoint = adaptor.EndpointRealtimeCalls
			i.UpstreamModel = "gpt-realtime-upstream"
		})
		raw := []byte("{\n  \"model\": \"gpt-realtime-upstream\",\n  \"sdp\": \"v=0\\r\\n\"\n}\n")
		out, usage := (Adaptor{}).ParseNonStreamResponse(realtimeInfo, raw)
		if !bytes.Equal(out, raw) {
			t.Fatalf("realtime response changed:\ngot  %q\nwant %q", out, raw)
		}
		if usage != nil {
			t.Fatalf("realtime usage = %+v, want nil", usage)
		}
	})
}

// observeImage 把一段图像 SSE 逐行喂给观察器。
func observeImage(endpoint string, lines ...string) adaptor.StreamObserver {
	info := buildInfo(func(i *adaptor.RelayInfo) { i.Endpoint = endpoint })
	obs := (Adaptor{}).NewStreamObserver(info)
	for _, line := range lines {
		obs.ObserveLine(line)
	}
	return obs
}

// TestNewStreamObserverOnlyImages 仅图像端点返回观察器；chat/responses 返回 nil
// （管线维持既有 OpenAI SSE 内联捕获语义不变）。
func TestNewStreamObserverOnlyImages(t *testing.T) {
	for _, endpoint := range []string{adaptor.EndpointChatCompletions, adaptor.EndpointResponses} {
		info := buildInfo(func(i *adaptor.RelayInfo) { i.Endpoint = endpoint })
		if obs := (Adaptor{}).NewStreamObserver(info); obs != nil {
			t.Errorf("endpoint=%q 不应返回观察器", endpoint)
		}
	}
	for _, endpoint := range []string{adaptor.EndpointImagesGenerations, adaptor.EndpointImagesEdits} {
		info := buildInfo(func(i *adaptor.RelayInfo) { i.Endpoint = endpoint })
		if obs := (Adaptor{}).NewStreamObserver(info); obs == nil {
			t.Errorf("endpoint=%q 应返回观察器", endpoint)
		}
	}
}

// TestImageStreamObserver 图像流观察器表驱动：completed 事件计次 + usage 捕获 +
// 完成信号；partial 事件不计量；垃圾行忽略；无 completed 视为未完成（静默断流可检出）。
func TestImageStreamObserver(t *testing.T) {
	cases := []struct {
		name      string
		lines     []string
		wantUsage *dto.Usage
		wantDone  bool
	}{
		{
			name: "完整流：partial ×2 + completed（usage + 1 次计次）",
			lines: []string{
				`event: image_generation.partial_image`,
				`data: {"type":"image_generation.partial_image","partial_image_index":0,"b64_json":"QQ=="}`,
				``,
				`data: {"type":"image_generation.partial_image","partial_image_index":1,"b64_json":"Qg=="}`,
				``,
				`event: image_generation.completed`,
				`data: {"type":"image_generation.completed","b64_json":"Qw==","usage":{"input_tokens":150,"output_tokens":4160,"input_tokens_details":{"image_tokens":100,"text_tokens":50}}}`,
			},
			wantUsage: &dto.Usage{PromptTokens: 150, CompletionTokens: 4160, Calls: 1},
			wantDone:  true,
		},
		{
			name: "image_edit 事件族同样识别",
			lines: []string{
				`data: {"type":"image_edit.completed","b64_json":"Qw==","usage":{"input_tokens":10,"output_tokens":20}}`,
			},
			wantUsage: &dto.Usage{PromptTokens: 10, CompletionTokens: 20, Calls: 1},
			wantDone:  true,
		},
		{
			name: "completed 无 usage：仅计次（按次计费仍正确）",
			lines: []string{
				`data: {"type":"image_generation.completed","b64_json":"Qw=="}`,
				`data: {"type":"image_generation.completed","b64_json":"RA=="}`,
			},
			wantUsage: &dto.Usage{Calls: 2},
			wantDone:  true,
		},
		{
			name: "仅 partial 无 completed：未完成（静默断流），无计量",
			lines: []string{
				`data: {"type":"image_generation.partial_image","b64_json":"QQ=="}`,
			},
			wantUsage: nil,
			wantDone:  false,
		},
		{
			name: "垃圾行/非 data 行/[DONE] 忽略不 panic",
			lines: []string{
				`event: ping`, `: comment`, ``, `data: not-json`, `data:`, `data: [DONE]`,
			},
			wantUsage: nil,
			wantDone:  false,
		},
		{
			name: "异形上游：usage 在非 completed 事件也照收并视作完成",
			lines: []string{
				`data: {"type":"weird.event","usage":{"input_tokens":5,"output_tokens":7}}`,
			},
			wantUsage: &dto.Usage{PromptTokens: 5, CompletionTokens: 7},
			wantDone:  true,
		},
		{
			name: "completed 携带 size/quality：提取供分辨率价表计费",
			lines: []string{
				`data: {"type":"image_generation.partial_image","b64_json":"QQ==","size":"1024x1024"}`,
				`data: {"type":"image_generation.completed","b64_json":"Qw==","size":"1024x1536","quality":"high",` +
					`"usage":{"input_tokens":150,"output_tokens":4160}}`,
			},
			wantUsage: &dto.Usage{PromptTokens: 150, CompletionTokens: 4160, Calls: 1, ImageSize: "1024x1536", ImageQuality: "high"},
			wantDone:  true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obs := observeImage(adaptor.EndpointImagesGenerations, tc.lines...)
			u, ok := obs.Usage()
			if tc.wantUsage == nil {
				if ok {
					t.Errorf("Usage() = %+v, want 无", u)
				}
			} else if !ok || u != *tc.wantUsage {
				t.Errorf("Usage() = %+v,%v, want %+v", u, ok, *tc.wantUsage)
			}
			if obs.Done() != tc.wantDone {
				t.Errorf("Done() = %v, want %v", obs.Done(), tc.wantDone)
			}
			if obs.Err() != nil {
				t.Errorf("Err() = %v, want nil", obs.Err())
			}
		})
	}
}

// TestRewriteMultipartModel model_mapping 生效时的 multipart 定点重写：
// 仅 model 普通字段值替换，boundary 沿用、其余字段与文件字节原样；
// 同名文件 part 不当作 model 字段。
func TestRewriteMultipartModel(t *testing.T) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("model", "img-flat"); err != nil {
		t.Fatalf("WriteField: %v", err)
	}
	if err := mw.WriteField("prompt", "a cat"); err != nil {
		t.Fatalf("WriteField: %v", err)
	}
	fw, err := mw.CreateFormFile("image", "in.png")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write([]byte("PNGDATA\x00\xff")); err != nil {
		t.Fatalf("写文件 part: %v", err)
	}
	_ = mw.Close()
	origCT := mw.FormDataContentType()

	out, err := RewriteMultipartModel(buf.Bytes(), origCT, "gpt-image-upstream")
	if err != nil {
		t.Fatalf("RewriteMultipartModel: %v", err)
	}

	// boundary 沿用 → 原 Content-Type 直接可解析重写后的体。
	_, params, err := mime.ParseMediaType(origCT)
	if err != nil {
		t.Fatalf("ParseMediaType: %v", err)
	}
	mr := multipart.NewReader(bytes.NewReader(out), params["boundary"])
	got := map[string]string{}
	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}
		val, _ := io.ReadAll(part)
		key := part.FormName()
		if part.FileName() != "" {
			key = "file:" + key
		}
		got[key] = string(val)
	}
	if got["model"] != "gpt-image-upstream" {
		t.Errorf("model = %q, want gpt-image-upstream", got["model"])
	}
	if got["prompt"] != "a cat" {
		t.Errorf("prompt = %q, want 原样", got["prompt"])
	}
	if got["file:image"] != "PNGDATA\x00\xff" {
		t.Errorf("文件字节被改写: %q", got["file:image"])
	}

	t.Run("非法 Content-Type 报错", func(t *testing.T) {
		if _, err := RewriteMultipartModel([]byte("x"), "application/json", "m"); err == nil {
			t.Error("非 multipart Content-Type 应报错")
		}
	})
}
