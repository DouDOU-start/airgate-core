package openai

import (
	"context"
	"encoding/json"
	"io"
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

// buildInfo 构造测试 RelayInfo。
func buildInfo(mutate ...func(*adaptor.RelayInfo)) *adaptor.RelayInfo {
	info := &adaptor.RelayInfo{
		Channel: &registry.ChannelSnapshot{
			ID:      1,
			Type:    "openai_compatible",
			BaseURL: "https://api.example.com",
		},
		APIKey:        "sk-upstream",
		RequestModel:  "gpt-4o",
		UpstreamModel: "gpt-4o",
		EntryProtocol: "openai",
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
				i.Channel.ParamOverride = map[string]any{
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
			i.Channel.ParamOverride = map[string]any{
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
		i.Channel.HeaderOverride = map[string]string{
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
}
