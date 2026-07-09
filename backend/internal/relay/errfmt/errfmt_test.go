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
