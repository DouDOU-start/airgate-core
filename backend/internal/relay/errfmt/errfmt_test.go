package errfmt

import (
	"net/http"
	"testing"

	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
)

func TestProtocolForPath(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/v1/messages", registry.ProtocolAnthropic},
		{"/v1/messages/count_tokens", registry.ProtocolAnthropic},
		{"/v1beta/models/gemini-2.5-pro:generateContent", registry.ProtocolGemini},
		{"/v1beta/models/gemini-2.5-pro:countTokens", registry.ProtocolGemini},
		{"/v1/chat/completions", registry.ProtocolOpenAI},
		{"/v1/responses", registry.ProtocolOpenAI},
		{"/v1/images/generations", registry.ProtocolOpenAI},
		{"/v1/models", registry.ProtocolOpenAI},
		{"/unknown", registry.ProtocolOpenAI}, // 未知路径缺省 openai 形态
	}
	for _, tc := range cases {
		if got := ProtocolForPath(tc.path); got != tc.want {
			t.Errorf("ProtocolForPath(%q) = %q, 期望 %q", tc.path, got, tc.want)
		}
	}
}

func TestAnthropicErrorType(t *testing.T) {
	cases := []struct {
		name    string
		errType string
		status  int
		want    string
	}{
		{"分类直映射 authentication", "authentication_error", http.StatusBadRequest, "authentication_error"},
		{"分类直映射 rate_limit", "rate_limit_error", http.StatusOK, "rate_limit_error"},
		{"401 归 authentication", "server_error", http.StatusUnauthorized, "authentication_error"},
		{"403 归 permission", "", http.StatusForbidden, "permission_error"},
		{"404 归 not_found", "", http.StatusNotFound, "not_found_error"},
		{"413 归 request_too_large", "", http.StatusRequestEntityTooLarge, "request_too_large"},
		{"429 归 rate_limit", "", http.StatusTooManyRequests, "rate_limit_error"},
		{"500 归 api_error", "server_error", http.StatusInternalServerError, "api_error"},
		{"503 归 api_error", "server_error", http.StatusServiceUnavailable, "api_error"},
		{"402 无对应形态归 invalid_request", "insufficient_quota", http.StatusPaymentRequired, "invalid_request_error"},
		{"400 缺省 invalid_request", "invalid_request_error", http.StatusBadRequest, "invalid_request_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := anthropicErrorType(tc.errType, tc.status); got != tc.want {
				t.Errorf("anthropicErrorType(%q, %d) = %q, 期望 %q", tc.errType, tc.status, got, tc.want)
			}
		})
	}
}

func TestParseUpstream(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   UpstreamError
	}{
		{
			name:   "openai 形态",
			status: 400,
			body:   `{"error":{"message":"bad param","type":"invalid_request_error","code":"invalid_value"}}`,
			want:   UpstreamError{Message: "bad param", Type: "invalid_request_error", Code: "invalid_value"},
		},
		{
			name:   "openai 形态数字 code",
			status: 400,
			body:   `{"error":{"message":"oops","type":"invalid_request_error","code":1210}}`,
			want:   UpstreamError{Message: "oops", Type: "invalid_request_error", Code: "1210"},
		},
		{
			name:   "anthropic 形态",
			status: 400,
			body:   `{"type":"error","error":{"type":"invalid_request_error","message":"messages required"}}`,
			want:   UpstreamError{Message: "messages required", Type: "invalid_request_error"},
		},
		{
			name:   "gemini 形态（status 归入 Code）",
			status: 400,
			body:   `{"error":{"code":400,"message":"invalid argument","status":"INVALID_ARGUMENT"}}`,
			want:   UpstreamError{Message: "invalid argument", Code: "INVALID_ARGUMENT"},
		},
		{
			name:   "顶层 message 兜底",
			status: 404,
			body:   `{"message":"route not found"}`,
			want:   UpstreamError{Message: "route not found"},
		},
		{
			name:   "纯文本截断兜底",
			status: 404,
			body:   `404 page not found`,
			want:   UpstreamError{Message: "404 page not found"},
		},
		{
			name:   "有 code 无 message 时 message 回落状态码文案",
			status: 404,
			body:   `{"error":{"code":"model_not_found"}}`,
			want:   UpstreamError{Message: "上游返回状态码 404", Code: "model_not_found"},
		},
		{
			name:   "空体回落状态码文案",
			status: 404,
			body:   ``,
			want:   UpstreamError{Message: "上游返回状态码 404"},
		},
		{
			name:   "JSON 无可用字段回落状态码文案",
			status: 418,
			body:   `{"detail":"whatever"}`,
			want:   UpstreamError{Message: "上游返回状态码 418"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseUpstream(tc.status, []byte(tc.body)); got != tc.want {
				t.Errorf("ParseUpstream = %+v, 期望 %+v", got, tc.want)
			}
		})
	}
}

func TestRenderUpstream(t *testing.T) {
	t.Run("openai：type/code 原样保留", func(t *testing.T) {
		got := RenderUpstream(registry.ProtocolOpenAI, 400,
			UpstreamError{Message: "m", Type: "invalid_request_error", Code: "c"}, "req-1").(OpenAIError)
		if got.Error.Message != "m" || got.Error.Type != "invalid_request_error" || got.Error.Code != "c" {
			t.Errorf("OpenAIError = %+v", got)
		}
		if got.RequestID != "req-1" {
			t.Errorf("RequestID = %q", got.RequestID)
		}
	})

	t.Run("openai：type 缺省按状态码取默认", func(t *testing.T) {
		cases := []struct {
			status int
			want   string
		}{
			{401, "authentication_error"},
			{429, "rate_limit_error"},
			{500, "api_error"},
			{400, "invalid_request_error"},
		}
		for _, tc := range cases {
			got := RenderUpstream(registry.ProtocolOpenAI, tc.status, UpstreamError{Message: "m"}, "").(OpenAIError)
			if got.Error.Type != tc.want {
				t.Errorf("status %d type = %q, 期望 %q", tc.status, got.Error.Type, tc.want)
			}
		}
	})

	t.Run("anthropic：原生 type 保留，非原生按状态码归类", func(t *testing.T) {
		got := RenderUpstream(registry.ProtocolAnthropic, 529,
			UpstreamError{Message: "m", Type: "overloaded_error"}, "").(AnthropicError)
		if got.Error.Type != "overloaded_error" || got.Type != "error" {
			t.Errorf("原生 type 未保留: %+v", got)
		}
		got = RenderUpstream(registry.ProtocolAnthropic, 404,
			UpstreamError{Message: "m", Type: "some_vendor_error"}, "").(AnthropicError)
		if got.Error.Type != "not_found_error" {
			t.Errorf("非原生 type 未按状态码归类: %+v", got)
		}
	})

	t.Run("gemini：canonical status 保留，映射不上按状态码推导", func(t *testing.T) {
		got := RenderUpstream(registry.ProtocolGemini, 400,
			UpstreamError{Message: "m", Code: "FAILED_PRECONDITION"}, "").(GeminiError)
		if got.Error.Status != "FAILED_PRECONDITION" || got.Error.Code != 400 {
			t.Errorf("canonical status 未保留: %+v", got)
		}
		got = RenderUpstream(registry.ProtocolGemini, 404,
			UpstreamError{Message: "m", Code: "not-canonical"}, "").(GeminiError)
		if got.Error.Status != "NOT_FOUND" {
			t.Errorf("非 canonical code 未按状态码推导: %+v", got)
		}
	})
}

func TestGeminiStatus(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   string
	}{
		{"400", http.StatusBadRequest, "INVALID_ARGUMENT"},
		{"413 同归 INVALID_ARGUMENT", http.StatusRequestEntityTooLarge, "INVALID_ARGUMENT"},
		{"401", http.StatusUnauthorized, "UNAUTHENTICATED"},
		{"403", http.StatusForbidden, "PERMISSION_DENIED"},
		{"404", http.StatusNotFound, "NOT_FOUND"},
		{"429", http.StatusTooManyRequests, "RESOURCE_EXHAUSTED"},
		{"500", http.StatusInternalServerError, "INTERNAL"},
		{"501", http.StatusNotImplemented, "UNIMPLEMENTED"},
		{"504", http.StatusGatewayTimeout, "DEADLINE_EXCEEDED"},
		{"502 兜底 UNAVAILABLE", http.StatusBadGateway, "UNAVAILABLE"},
		{"503 兜底 UNAVAILABLE", http.StatusServiceUnavailable, "UNAVAILABLE"},
		{"402 无对应 4xx 兜底 FAILED_PRECONDITION", http.StatusPaymentRequired, "FAILED_PRECONDITION"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := geminiStatus(tc.status); got != tc.want {
				t.Errorf("geminiStatus(%d) = %q, 期望 %q", tc.status, got, tc.want)
			}
		})
	}
}
