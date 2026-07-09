package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
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
		Channel:       &registry.ChannelSnapshot{BaseURL: "https://api.anthropic.com"},
		APIKey:        "sk-ant-test",
		RequestModel:  model,
		UpstreamModel: upstream,
		Stream:        stream,
		Endpoint:      adaptor.EndpointMessages,
	}
}

// fieldsOf 解析 JSON 对象为顶层字段表（RawMessage 统一压缩：
// json.Marshal 序列化 RawMessage 时会去空白，比较前先归一，字段内容仍逐字节比对）。
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

// TestBuildRequestPassthrough 零翻译透传：除 model 重写与 param_override 外，
// 其余顶层字段的原始字节逐一不变（含工具、未知字段、原生 Anthropic 结构）。
func TestBuildRequestPassthrough(t *testing.T) {
	original := `{
		"model":"claude",
		"stream":true,
		"max_tokens":321,
		"system":[{"type":"text","text":"be brief","cache_control":{"type":"ephemeral"}}],
		"messages":[
			{"role":"user","content":[{"type":"text","text":"hi"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"QUJD"}}]},
			{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"get_weather","input":{"city":"sh"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"sunny"}]}
		],
		"tools":[{"name":"get_weather","input_schema":{"type":"object"}}],
		"metadata":{"user_id":"u-1"},
		"unknown_future_field":{"nested":[1,2,3]}
	}`
	req := mustReq(t, original)
	in := info("claude", "claude-3-5-sonnet", true)
	in.Channel.ParamOverride = map[string]any{"temperature": 0.1}

	httpReq, err := Adaptor{}.BuildRequest(context.Background(), in, req)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}

	if got := httpReq.URL.String(); got != "https://api.anthropic.com/v1/messages" {
		t.Errorf("url = %s", got)
	}
	if httpReq.Header.Get("x-api-key") != "sk-ant-test" {
		t.Errorf("x-api-key = %q", httpReq.Header.Get("x-api-key"))
	}
	if httpReq.Header.Get("anthropic-version") != anthropicVersion {
		t.Errorf("anthropic-version = %q", httpReq.Header.Get("anthropic-version"))
	}

	sentBody, _ := io.ReadAll(httpReq.Body)
	sent := fieldsOf(t, sentBody)
	want := fieldsOf(t, []byte(original))

	// model 重写、param_override 追加，其余字段逐字节不变。
	if string(sent["model"]) != `"claude-3-5-sonnet"` {
		t.Errorf("model = %s, want 渠道映射后的上游名", sent["model"])
	}
	if string(sent["temperature"]) != "0.1" {
		t.Errorf("param_override temperature = %s", sent["temperature"])
	}
	for key, raw := range want {
		if key == "model" {
			continue
		}
		if got, ok := sent[key]; !ok || string(got) != string(raw) {
			t.Errorf("字段 %q 被改写:\ngot  %s\nwant %s", key, got, raw)
		}
	}
	if len(sent) != len(want)+1 { // +temperature
		t.Errorf("字段数 = %d, want %d（不得增删透传字段）", len(sent), len(want)+1)
	}
}

// TestBuildRequestNoMaxTokensInjection 零翻译：不再兜底注入 max_tokens
// （原生客户端自带；缺失由上游按协议报错并原样透传）。
func TestBuildRequestNoMaxTokensInjection(t *testing.T) {
	req := mustReq(t, `{"model":"c","messages":[{"role":"user","content":"hi"}]}`)
	httpReq, err := Adaptor{}.BuildRequest(context.Background(), info("c", "", false), req)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	body, _ := io.ReadAll(httpReq.Body)
	if strings.Contains(string(body), "max_tokens") {
		t.Errorf("不得注入 max_tokens: %s", body)
	}
}

// TestBuildRequestParamOverrideRemove param_override null 值删除字段。
func TestBuildRequestParamOverrideRemove(t *testing.T) {
	req := mustReq(t, `{"model":"c","messages":[],"top_k":40,"max_tokens":10}`)
	in := info("c", "", false)
	in.Channel.ParamOverride = map[string]any{"top_k": nil, "max_tokens": float64(99)}
	httpReq, _ := Adaptor{}.BuildRequest(context.Background(), in, req)
	body, _ := io.ReadAll(httpReq.Body)
	sent := fieldsOf(t, body)
	if _, ok := sent["top_k"]; ok {
		t.Error("param_override null 未删除 top_k")
	}
	if string(sent["max_tokens"]) != "99" {
		t.Errorf("max_tokens = %s, want 99", sent["max_tokens"])
	}
}

// TestBuildRequestEndpointGuard anthropic 渠道仅接受 messages 端点
// （Pick 协议过滤保证不会路由到，其余端点即装配错误）。
func TestBuildRequestEndpointGuard(t *testing.T) {
	req := mustReq(t, `{"model":"c","messages":[{"role":"user","content":"hi"}]}`)
	for _, endpoint := range []string{"", adaptor.EndpointChatCompletions, adaptor.EndpointResponses, adaptor.EndpointGenerateContent} {
		in := info("c", "", false)
		in.Endpoint = endpoint
		if _, err := (Adaptor{}).BuildRequest(context.Background(), in, req); err == nil {
			t.Errorf("endpoint=%q 应报错", endpoint)
		}
	}
}

// TestBuildRequestCountTokens count_tokens 端点：URL 拼 /count_tokens 后缀，
// 认证头与请求改写（model 重写）与 messages 一致。
func TestBuildRequestCountTokens(t *testing.T) {
	req := mustReq(t, `{"model":"c","messages":[{"role":"user","content":"hi"}]}`)
	in := info("c", "claude-upstream", false)
	in.Endpoint = adaptor.EndpointMessagesCountTokens

	httpReq, err := (Adaptor{}).BuildRequest(context.Background(), in, req)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if got := httpReq.URL.String(); got != "https://api.anthropic.com/v1/messages/count_tokens" {
		t.Errorf("URL = %q, want /v1/messages/count_tokens", got)
	}
	if httpReq.Header.Get("x-api-key") == "" || httpReq.Header.Get("anthropic-version") == "" {
		t.Error("count_tokens 请求缺少认证/版本头")
	}
	body, _ := io.ReadAll(httpReq.Body)
	sent := fieldsOf(t, body)
	if string(sent["model"]) != `"claude-upstream"` {
		t.Errorf("model = %s, want 重写为上游名", sent["model"])
	}
}

// TestMessagesURL base 已含 /v1 时不重复拼接。
func TestMessagesURL(t *testing.T) {
	cases := []struct{ base, want string }{
		{"https://api.anthropic.com", "https://api.anthropic.com/v1/messages"},
		{"https://api.anthropic.com/", "https://api.anthropic.com/v1/messages"},
		{"https://gw.example.com/v1", "https://gw.example.com/v1/messages"},
	}
	for _, c := range cases {
		if got := messagesURL(c.base); got != c.want {
			t.Errorf("messagesURL(%q) = %q, want %q", c.base, got, c.want)
		}
	}
}

// TestParseNonStreamResponse usage 提取表驱动（归一化语义：PromptTokens 含缓存读，
// 保留 5m/1h 双档缓存写明细）+ model 回写 + 其余字段原样。
func TestParseNonStreamResponse(t *testing.T) {
	cases := []struct {
		name string
		body string
		want *dto.Usage
	}{
		{
			name: "全量 usage 含双档缓存写",
			body: `{"id":"msg_1","model":"claude-3-5-sonnet","stop_reason":"end_turn",
				"content":[{"type":"text","text":"Hello"}],
				"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":20,
					"cache_creation_input_tokens":7,
					"cache_creation":{"ephemeral_5m_input_tokens":3,"ephemeral_1h_input_tokens":4}}}`,
			want: &dto.Usage{
				PromptTokens: 30, CompletionTokens: 5, CachedTokens: 20,
				CacheCreationTokens: 7, CacheCreation5mTokens: 3, CacheCreation1hTokens: 4,
			},
		},
		{
			name: "无缓存字段",
			body: `{"id":"m","model":"claude-3-5-sonnet","content":[],"usage":{"input_tokens":100,"output_tokens":50}}`,
			want: &dto.Usage{PromptTokens: 100, CompletionTokens: 50},
		},
		{
			name: "无 usage 字段",
			body: `{"id":"m","model":"claude-3-5-sonnet","content":[]}`,
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, usage := Adaptor{}.ParseNonStreamResponse(info("claude", "claude-3-5-sonnet", false), []byte(tc.body))
			if tc.want == nil {
				if usage != nil {
					t.Fatalf("usage = %+v, want nil", usage)
				}
			} else if usage == nil || *usage != *tc.want {
				t.Fatalf("usage = %+v, want %+v", usage, tc.want)
			}

			// model 回写为对外名，其余字段原样（含原生 content/stop_reason 结构不被翻译）。
			sent := fieldsOf(t, out)
			orig := fieldsOf(t, []byte(tc.body))
			if string(sent["model"]) != `"claude"` {
				t.Errorf("model = %s, want 回写对外名", sent["model"])
			}
			for key, raw := range orig {
				if key == "model" {
					continue
				}
				if got := sent[key]; string(got) != string(raw) {
					t.Errorf("字段 %q 被改写:\ngot  %s\nwant %s", key, got, raw)
				}
			}
		})
	}
}

// TestParseNonStreamResponseInvalidJSON 解析失败时 body 原样返回。
func TestParseNonStreamResponseInvalidJSON(t *testing.T) {
	out, usage := Adaptor{}.ParseNonStreamResponse(info("c", "", false), []byte("not-json"))
	if string(out) != "not-json" || usage != nil {
		t.Errorf("解析失败应原样返回: out=%s usage=%v", out, usage)
	}
}

// observe 把一段 Anthropic SSE 逐行喂给观察器。
func observe(lines ...string) *streamObserver {
	obs := (Adaptor{}).NewStreamObserver(info("claude", "", true)).(*streamObserver)
	for _, line := range lines {
		obs.ObserveLine(line)
	}
	return obs
}

// TestStreamObserver 透传型观察器表驱动：usage 累积（message_start input 侧 +
// message_delta output 侧，含双档缓存写）、message_stop 完成信号、
// 流内 error 事件、中途断连 usage 兜底。
func TestStreamObserver(t *testing.T) {
	cases := []struct {
		name      string
		lines     []string
		wantUsage *dto.Usage
		wantDone  bool
		wantErr   bool
	}{
		{
			name: "完整流：start+delta+stop 累积 usage",
			lines: []string{
				`event: message_start`,
				`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":10,"cache_read_input_tokens":20}}}`,
				``,
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}`,
				`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
				`data: {"type":"message_stop"}`,
			},
			wantUsage: &dto.Usage{PromptTokens: 30, CompletionTokens: 5, CachedTokens: 20},
			wantDone:  true,
		},
		{
			name: "双档缓存写明细保留",
			lines: []string{
				`data: {"type":"message_start","message":{"id":"m","usage":{"input_tokens":10,"cache_creation_input_tokens":7,"cache_creation":{"ephemeral_5m_input_tokens":3,"ephemeral_1h_input_tokens":4}}}}`,
				`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
				`data: {"type":"message_stop"}`,
			},
			wantUsage: &dto.Usage{
				PromptTokens: 10, CompletionTokens: 2,
				CacheCreationTokens: 7, CacheCreation5mTokens: 3, CacheCreation1hTokens: 4,
			},
			wantDone: true,
		},
		{
			name: "流内 error 事件（overloaded_error）：Err 非 nil、不算完成、usage 兜底可取",
			lines: []string{
				`data: {"type":"message_start","message":{"id":"m","usage":{"input_tokens":10}}}`,
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}`,
				`data: {"type":"error","error":{"type":"overloaded_error","message":"overloaded"}}`,
			},
			wantUsage: &dto.Usage{PromptTokens: 10},
			wantDone:  false,
			wantErr:   true,
		},
		{
			name: "中途断连（无 message_stop）：不算完成，start 送达的 usage 兜底可取",
			lines: []string{
				`data: {"type":"message_start","message":{"id":"m","usage":{"input_tokens":100,"cache_read_input_tokens":50}}}`,
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"x"}}`,
			},
			wantUsage: &dto.Usage{PromptTokens: 150, CachedTokens: 50},
			wantDone:  false,
		},
		{
			name: "无 usage 事件：Usage 返回 false",
			lines: []string{
				`data: {"type":"ping"}`,
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
