package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/cpa"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
)

type captureChannelForwarder struct {
	mu       sync.Mutex
	requests []cpa.ForwardRequest
	result   cpa.ForwardResult
}

func (f *captureChannelForwarder) Forward(_ context.Context, _ *gin.Context, req cpa.ForwardRequest) cpa.ForwardResult {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	f.mu.Unlock()
	return f.result
}

func (f *captureChannelForwarder) calls() []cpa.ForwardRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]cpa.ForwardRequest(nil), f.requests...)
}

type channelRoundTripFunc func(*http.Request) (*http.Response, error)

func (f channelRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestChannelNeedsCPATranslation(t *testing.T) {
	tests := []struct {
		name          string
		endpoint      string
		entryProtocol string
		channelType   string
		want          bool
	}{
		{"OpenAI 文本转 Anthropic", adaptor.EndpointChatCompletions, registry.ProtocolOpenAI, "anthropic", true},
		{"Anthropic 文本转 OpenAI", adaptor.EndpointMessages, registry.ProtocolAnthropic, "openai_compatible", true},
		{"Gemini 文本转 Anthropic", adaptor.EndpointGenerateContent, registry.ProtocolGemini, "anthropic", true},
		{"同协议保持直发", adaptor.EndpointChatCompletions, registry.ProtocolOpenAI, "openai_compatible", false},
		{"OpenAI 生图不翻译", adaptor.EndpointImagesGenerations, registry.ProtocolOpenAI, "anthropic", false},
		{"Gemini 生图不翻译", adaptor.EndpointPredict, registry.ProtocolGemini, "openai_compatible", false},
		{"视频任务不翻译", adaptor.EndpointXAIVideosGenerations, registry.ProtocolOpenAI, "anthropic", false},
		{"搜索端点不翻译", adaptor.EndpointAlphaSearch, registry.ProtocolOpenAI, "anthropic", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := channelNeedsCPATranslation(tt.endpoint, tt.entryProtocol, tt.channelType); got != tt.want {
				t.Fatalf("channelNeedsCPATranslation() = %v，期望 %v", got, tt.want)
			}
		})
	}
}

func TestChannelRoutingProtocolKeepsNonTextNative(t *testing.T) {
	tests := []struct {
		endpoint string
		want     string
	}{
		{adaptor.EndpointChatCompletions, registry.ProtocolTranslatedText},
		{adaptor.EndpointMessages, registry.ProtocolTranslatedText},
		{adaptor.EndpointGenerateContent, registry.ProtocolTranslatedText},
		{adaptor.EndpointImagesGenerations, registry.ProtocolOpenAI},
		{adaptor.EndpointImagesEdits, registry.ProtocolOpenAI},
		{adaptor.EndpointPredict, registry.ProtocolGemini},
		{adaptor.EndpointXAIVideosGenerations, registry.ProtocolOpenAI},
		{adaptor.EndpointAlphaSearch, registry.ProtocolOpenAI},
	}
	for _, tt := range tests {
		if got := channelRoutingProtocolForEndpoint(tt.endpoint); got != tt.want {
			t.Errorf("端点 %s 的调度协议 = %s，期望 %s", tt.endpoint, got, tt.want)
		}
	}
}

func TestForwardCrossProtocolChannelUsesCPA(t *testing.T) {
	snap := testSnap(501, "https://anthropic.example/v1", func(s *registry.ChannelKeySnapshot) {
		s.Type = "anthropic"
	})
	env := newTestEnv(t, snap)
	forwarder := &captureChannelForwarder{result: cpa.ForwardResult{
		StatusCode:  http.StatusOK,
		ContentType: "application/json",
		Body:        []byte(`{"id":"chat-cpa","model":"gpt-4o","choices":[{"message":{"role":"assistant","content":"ok"}}]}`),
		Usage:       &dto.Usage{PromptTokens: 10, CompletionTokens: 2},
	}}
	env.pipe.cpa = forwarder

	response := env.do(t, `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	calls := forwarder.calls()
	if len(calls) != 1 {
		t.Fatalf("CPA 调用次数 = %d，期望 1", len(calls))
	}
	call := calls[0]
	if call.Account.Platform != "anthropic" {
		t.Fatalf("CPA platform = %q，期望 anthropic", call.Account.Platform)
	}
	if call.Account.Credentials["api_key"] != snap.APIKey {
		t.Fatalf("CPA 未收到渠道 API Key")
	}
	if call.Account.Credentials["base_url"] != "https://anthropic.example" {
		t.Fatalf("CPA base_url = %q", call.Account.Credentials["base_url"])
	}
	if call.EntryProtocol != registry.ProtocolOpenAI || call.Endpoint != adaptor.EndpointChatCompletions {
		t.Fatalf("CPA 入口信息异常: protocol=%s endpoint=%s", call.EntryProtocol, call.Endpoint)
	}
	if call.UpstreamModel != "gpt-4o-upstream" {
		t.Fatalf("CPA upstream model = %q", call.UpstreamModel)
	}
}

func TestForwardNativeProtocolDoesNotUseCPA(t *testing.T) {
	env := newTestEnv(t, testSnap(502, "https://openai.example"))
	forwarder := &captureChannelForwarder{}
	env.pipe.cpa = forwarder
	env.pipe.client.Transport = channelRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"id":"chat-native","model":"gpt-4o-upstream","usage":{"prompt_tokens":10,"completion_tokens":2},"choices":[{"message":{"role":"assistant","content":"ok"}}]}`,
			)),
			Request: req,
		}, nil
	})

	response := env.do(t, `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if calls := forwarder.calls(); len(calls) != 0 {
		t.Fatalf("同协议请求错误调用 CPA %d 次", len(calls))
	}
}

func TestChannelOverrideTransportAppliesTranslatedRequestOverrides(t *testing.T) {
	var capturedBody map[string]any
	base := channelRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("X-Route"); got != "translated" {
			t.Fatalf("X-Route = %q，期望 translated", got)
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("读取请求体失败: %v", err)
		}
		if err := json.Unmarshal(body, &capturedBody); err != nil {
			t.Fatalf("请求体不是合法 JSON: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Request:    req,
		}, nil
	})
	transport := &channelOverrideTransport{
		base:    base,
		headers: map[string]string{"X-Route": "translated"},
		params: map[string]any{
			"legacy_remove": nil,
			"set": map[string]any{
				"temperature": 0.2,
				"top_k":       8,
			},
			"remove": []any{"top_p"},
		},
	}
	body := []byte(`{"model":"translated","temperature":0.8,"top_p":0.9,"legacy_remove":true}`)
	req, err := http.NewRequest(http.MethodPost, "https://upstream.example/v1/messages", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if _, err := transport.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip error: %v", err)
	}
	if got := capturedBody["temperature"]; got != float64(0.2) {
		t.Fatalf("temperature = %#v，期望 0.2", got)
	}
	if got := capturedBody["top_k"]; got != float64(8) {
		t.Fatalf("top_k = %#v，期望 8", got)
	}
	if _, ok := capturedBody["top_p"]; ok {
		t.Fatal("top_p 未被删除")
	}
	if _, ok := capturedBody["legacy_remove"]; ok {
		t.Fatal("平铺 nil 覆盖未删除字段")
	}
	if _, ok := capturedBody["set"]; ok {
		t.Fatal("set 控制字段不应写入上游请求体")
	}
	if _, ok := capturedBody["remove"]; ok {
		t.Fatal("remove 控制字段不应写入上游请求体")
	}
}
