package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
)

func mustReq(t *testing.T, body string) *dto.ChatRequest {
	t.Helper()
	req, err := dto.ParseChatRequest([]byte(body))
	if err != nil {
		t.Fatalf("ParseChatRequest: %v", err)
	}
	return req
}

func info(model, upstream string, stream bool) *adaptor.RelayInfo {
	return &adaptor.RelayInfo{
		Channel:       &registry.ChannelSnapshot{BaseURL: "https://generativelanguage.googleapis.com"},
		APIKey:        "goog-test-key",
		RequestModel:  model,
		UpstreamModel: upstream,
		Stream:        stream,
		Endpoint:      adaptor.EndpointGenerateContent,
	}
}

// fieldsOf 解析 JSON 对象为顶层字段表（RawMessage 统一压缩后逐字节比对）。
func fieldsOf(t *testing.T, body []byte) map[string]json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("body 非 JSON 对象: %v", err)
	}
	for k, raw := range fields {
		var buf bytes.Buffer
		if err := json.Compact(&buf, raw); err != nil {
			t.Fatalf("字段 %q 压缩失败: %v", k, err)
		}
		fields[k] = append(json.RawMessage(nil), buf.Bytes()...)
	}
	return fields
}

func TestGenerateURL(t *testing.T) {
	cases := []struct {
		base, model string
		stream      bool
		want        string
	}{
		{"https://x.com", "gemini-2.0", false, "https://x.com/v1beta/models/gemini-2.0:generateContent"},
		{"https://x.com/", "gemini-2.0", true, "https://x.com/v1beta/models/gemini-2.0:streamGenerateContent?alt=sse"},
		{"https://x.com/v1beta", "g", false, "https://x.com/v1beta/models/g:generateContent"},
	}
	for _, c := range cases {
		if got := generateURL(c.base, c.model, c.stream); got != c.want {
			t.Errorf("generateURL(%q,%q,%v) = %q, want %q", c.base, c.model, c.stream, got, c.want)
		}
	}
}

// TestBuildRequestPassthrough 零翻译透传：model 重写只发生在 URL 层，
// 请求体除 param_override 外所有字段原始字节逐一不变（原生 Gemini 结构 / 未知字段）。
func TestBuildRequestPassthrough(t *testing.T) {
	original := `{
		"contents":[
			{"role":"user","parts":[{"text":"hi"},{"inline_data":{"mime_type":"image/png","data":"QUJD"}}]},
			{"role":"model","parts":[{"functionCall":{"name":"f","args":{"x":1}}}]}
		],
		"systemInstruction":{"parts":[{"text":"be brief"}]},
		"tools":[{"functionDeclarations":[{"name":"f"}]}],
		"generationConfig":{"temperature":0.5,"maxOutputTokens":100,"thinkingConfig":{"thinkingBudget":0}},
		"safetySettings":[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"BLOCK_NONE"}],
		"unknown_future_field":[1,2,3]
	}`
	req := mustReq(t, original)
	req.Model = "g" // 模型来自 URL path（entry 注入），不在请求体
	req.Stream = true
	in := info("g", "gemini-2.0-flash", true)

	httpReq, err := Adaptor{}.BuildRequest(context.Background(), in, req)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if got := httpReq.URL.String(); got != "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.0-flash:streamGenerateContent?alt=sse" {
		t.Errorf("url = %s（model 重写应发生在 URL 层）", got)
	}
	if httpReq.Header.Get("x-goog-api-key") != "goog-test-key" {
		t.Errorf("x-goog-api-key = %q", httpReq.Header.Get("x-goog-api-key"))
	}

	sentBody, _ := io.ReadAll(httpReq.Body)
	sent := fieldsOf(t, sentBody)
	want := fieldsOf(t, []byte(original))
	if len(sent) != len(want) {
		t.Errorf("字段数 = %d, want %d（不得增删透传字段）", len(sent), len(want))
	}
	for key, raw := range want {
		if got := sent[key]; string(got) != string(raw) {
			t.Errorf("字段 %q 被改写:\ngot  %s\nwant %s", key, got, raw)
		}
	}
	if _, ok := sent["model"]; ok {
		t.Error("请求体不得注入 model 字段（Gemini 的 model 在 URL）")
	}
}

// TestBuildRequestParamOverride param_override 在字段表层覆盖/删除（如 generationConfig 整体强制）。
func TestBuildRequestParamOverride(t *testing.T) {
	req := mustReq(t, `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"temperature":0.9},"safetySettings":[]}`)
	in := info("g", "", false)
	in.Channel.ParamOverride = map[string]any{
		"generationConfig": map[string]any{"temperature": 0.1, "maxOutputTokens": float64(64)},
		"safetySettings":   nil, // null 删除
	}
	httpReq, _ := Adaptor{}.BuildRequest(context.Background(), in, req)
	body, _ := io.ReadAll(httpReq.Body)
	sent := fieldsOf(t, body)

	var gc map[string]any
	if err := json.Unmarshal(sent["generationConfig"], &gc); err != nil {
		t.Fatalf("generationConfig 非对象: %v", err)
	}
	if gc["temperature"] != 0.1 || gc["maxOutputTokens"] != float64(64) {
		t.Errorf("generationConfig 覆盖失败: %v", gc)
	}
	if _, ok := sent["safetySettings"]; ok {
		t.Error("param_override null 未删除 safetySettings")
	}
}

// TestBuildRequestEndpointGuard gemini 渠道仅接受 generateContent/predict/countTokens 端点。
func TestBuildRequestEndpointGuard(t *testing.T) {
	req := mustReq(t, `{"contents":[]}`)
	for _, endpoint := range []string{"", adaptor.EndpointChatCompletions, adaptor.EndpointResponses, adaptor.EndpointMessages} {
		in := info("g", "", false)
		in.Endpoint = endpoint
		if _, err := (Adaptor{}).BuildRequest(context.Background(), in, req); err == nil {
			t.Errorf("endpoint=%q 应报错", endpoint)
		}
	}
}

// TestBuildRequestVerbURL predict / countTokens 动词的 URL 分支
// （model 重写仍在 URL 层，恒非流式、无 alt=sse）。
func TestBuildRequestVerbURL(t *testing.T) {
	cases := []struct {
		endpoint string
		want     string
	}{
		{adaptor.EndpointPredict, "https://generativelanguage.googleapis.com/v1beta/models/imagen-upstream:predict"},
		{adaptor.EndpointCountTokens, "https://generativelanguage.googleapis.com/v1beta/models/imagen-upstream:countTokens"},
	}
	for _, tc := range cases {
		t.Run(tc.endpoint, func(t *testing.T) {
			req := mustReq(t, `{"instances":[{"prompt":"a cat"}]}`)
			in := info("imagen-x", "imagen-upstream", false)
			in.Endpoint = tc.endpoint
			httpReq, err := (Adaptor{}).BuildRequest(context.Background(), in, req)
			if err != nil {
				t.Fatalf("BuildRequest: %v", err)
			}
			if got := httpReq.URL.String(); got != tc.want {
				t.Errorf("URL = %q, want %q", got, tc.want)
			}
			if httpReq.Header.Get("x-goog-api-key") != "goog-test-key" {
				t.Errorf("x-goog-api-key = %q", httpReq.Header.Get("x-goog-api-key"))
			}
		})
	}
}

// TestParseNonStreamResponse usage 提取表驱动（promptTokenCount 已含缓存读；
// thoughtsTokenCount 并入输出计费）+ modelVersion 回写 + 其余字段原样。
func TestParseNonStreamResponse(t *testing.T) {
	cases := []struct {
		name string
		body string
		want *dto.Usage
	}{
		{
			name: "含缓存读",
			body: `{"candidates":[{"content":{"parts":[{"text":"Hello"}],"role":"model"},"finishReason":"STOP"}],
				"usageMetadata":{"promptTokenCount":30,"candidatesTokenCount":5,"cachedContentTokenCount":20,"totalTokenCount":35},
				"modelVersion":"gemini-2.0-flash-001"}`,
			want: &dto.Usage{PromptTokens: 30, CompletionTokens: 5, CachedTokens: 20},
		},
		{
			name: "思考 token 并入输出（Gemini 2.5 漏计费防线）",
			body: `{"candidates":[{"content":{"parts":[{"text":"x"}],"role":"model"},"finishReason":"STOP"}],
				"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":200,"thoughtsTokenCount":500,"totalTokenCount":800}}`,
			want: &dto.Usage{PromptTokens: 100, CompletionTokens: 700},
		},
		{
			name: "无 usageMetadata",
			body: `{"candidates":[{"content":{"parts":[{"text":"x"}],"role":"model"}}]}`,
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, usage := Adaptor{}.ParseNonStreamResponse(info("g", "gemini-2.0-flash-001", false), []byte(tc.body))
			if tc.want == nil {
				if usage != nil {
					t.Fatalf("usage = %+v, want nil", usage)
				}
			} else if usage == nil || *usage != *tc.want {
				t.Fatalf("usage = %+v, want %+v", usage, tc.want)
			}

			sent := fieldsOf(t, out)
			orig := fieldsOf(t, []byte(tc.body))
			for key, raw := range orig {
				if key == "modelVersion" {
					// modelVersion 回写为对外模型名（隐藏渠道 model_mapping）。
					if string(sent[key]) != `"g"` {
						t.Errorf("modelVersion = %s, want 回写对外名", sent[key])
					}
					continue
				}
				if got := sent[key]; string(got) != string(raw) {
					t.Errorf("字段 %q 被改写:\ngot  %s\nwant %s", key, got, raw)
				}
			}
		})
	}
}

// TestParseNonStreamResponsePredict :predict（Imagen）响应表驱动：
// predictions 数组长度写入 usage.Calls（按次×张数计费的计次数）；
// 无 usageMetadata 时 token 用量全 0；响应体原样返回。
func TestParseNonStreamResponsePredict(t *testing.T) {
	cases := []struct {
		name string
		body string
		want *dto.Usage
	}{
		{
			name: "多张产出：Calls=predictions 长度、token 全 0",
			body: `{"predictions":[{"bytesBase64Encoded":"QUJD"},{"bytesBase64Encoded":"REVG"},{"bytesBase64Encoded":"R0hJ"}]}`,
			want: &dto.Usage{Calls: 3},
		},
		{
			name: "空 predictions：无 usage（计费侧按 1 次兜底）",
			body: `{"predictions":[]}`,
			want: nil,
		},
		{
			name: "无 predictions 字段：无 usage",
			body: `{"error_ish":"x"}`,
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := info("imagen-x", "imagen-upstream", false)
			in.Endpoint = adaptor.EndpointPredict
			out, usage := Adaptor{}.ParseNonStreamResponse(in, []byte(tc.body))
			if tc.want == nil {
				if usage != nil {
					t.Fatalf("usage = %+v, want nil", usage)
				}
			} else if usage == nil || *usage != *tc.want {
				t.Fatalf("usage = %+v, want %+v", usage, tc.want)
			}
			// 响应体原样透传（predict 无 modelVersion 回写点）。
			sent := fieldsOf(t, out)
			orig := fieldsOf(t, []byte(tc.body))
			for key, raw := range orig {
				if got := sent[key]; string(got) != string(raw) {
					t.Errorf("字段 %q 被改写:\ngot  %s\nwant %s", key, got, raw)
				}
			}
		})
	}
}

// observe 把一段 Gemini SSE 逐行喂给观察器。
func observe(lines ...string) *streamObserver {
	obs := (Adaptor{}).NewStreamObserver(info("g", "", true)).(*streamObserver)
	for _, line := range lines {
		obs.ObserveLine(line)
	}
	return obs
}

// TestStreamObserver 透传型观察器表驱动：usageMetadata 捕获（末 chunk）、
// finishReason 完成信号、流内 error 对象、中途断连 usage 兜底。
func TestStreamObserver(t *testing.T) {
	cases := []struct {
		name      string
		lines     []string
		wantUsage *dto.Usage
		wantDone  bool
		wantErr   bool
	}{
		{
			name: "完整流：末 chunk 带 usage 与 finishReason",
			lines: []string{
				`data: {"candidates":[{"content":{"parts":[{"text":"Hel"}],"role":"model"}}]}`,
				``,
				`data: {"candidates":[{"content":{"parts":[{"text":"lo"}],"role":"model"}}]}`,
				`data: {"candidates":[{"content":{"parts":[{"text":""}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":2,"totalTokenCount":10}}`,
			},
			wantUsage: &dto.Usage{PromptTokens: 8, CompletionTokens: 2},
			wantDone:  true,
		},
		{
			name: "缓存读与思考 token（thoughts 并入输出）",
			lines: []string{
				`data: {"candidates":[{"content":{"parts":[{"text":"x"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":20,"thoughtsTokenCount":30,"cachedContentTokenCount":40,"totalTokenCount":150}}`,
			},
			wantUsage: &dto.Usage{PromptTokens: 100, CompletionTokens: 50, CachedTokens: 40},
			wantDone:  true,
		},
		{
			name: "流内 error 对象：Err 非 nil、不算完成、已捕获 usage 兜底可取",
			lines: []string{
				`data: {"candidates":[{"content":{"parts":[{"text":"x"}]}}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":1,"totalTokenCount":6}}`,
				`data: {"error":{"code":429,"message":"quota"}}`,
			},
			wantUsage: &dto.Usage{PromptTokens: 5, CompletionTokens: 1},
			wantDone:  false,
			wantErr:   true,
		},
		{
			name: "中途断连（无 finishReason）：不算完成",
			lines: []string{
				`data: {"candidates":[{"content":{"parts":[{"text":"x"}]}}]}`,
			},
			wantUsage: nil,
			wantDone:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obs := observe(tc.lines...)
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
			if (obs.Err() != nil) != tc.wantErr {
				t.Errorf("Err() = %v, wantErr %v", obs.Err(), tc.wantErr)
			}
		})
	}
}

// TestStreamObserverIgnoresNonData 非 data 行与非法 JSON 一律忽略、不 panic。
func TestStreamObserverIgnoresNonData(t *testing.T) {
	obs := observe(`event: ping`, `: comment`, ``, `data: not-json`, `data:`)
	if _, ok := obs.Usage(); ok {
		t.Error("垃圾行不应产生 usage")
	}
	if obs.Done() || obs.Err() != nil {
		t.Errorf("垃圾行不应产生完成/错误信号: done=%v err=%v", obs.Done(), obs.Err())
	}
}
