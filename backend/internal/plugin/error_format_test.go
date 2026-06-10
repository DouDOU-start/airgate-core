package plugin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAnthropicErrorType(t *testing.T) {
	cases := []struct {
		name    string
		errType string
		status  int
		want    string
	}{
		{"已知类型透传", "invalid_request_error", 400, "invalid_request_error"},
		{"已知限流类型透传", "rate_limit_error", 429, "rate_limit_error"},
		{"server_error 按状态码归类为 api_error", "server_error", 502, "api_error"},
		{"429 状态码归类", "upstream_rate_limit", 429, "rate_limit_error"},
		{"404 状态码归类", "route_not_found", 404, "not_found_error"},
		{"503 状态码归类", "server_error", 503, "overloaded_error"},
		{"4xx 默认 invalid_request", "insufficient_quota", 402, "invalid_request_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := anthropicErrorType(tc.errType, tc.status); got != tc.want {
				t.Fatalf("anthropicErrorType(%q, %d) = %q, want %q", tc.errType, tc.status, got, tc.want)
			}
		})
	}
}

func TestProtocolErrorFormats(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name       string
		format     string // 写入 ctx 的格式；空 = 不写（默认 OpenAI）
		wantOpenAI bool
	}{
		{"默认 OpenAI 格式", "", true},
		{"显式 openai", errorFormatOpenAI, true},
		{"anthropic 格式", errorFormatAnthropic, false},
		{"未知取值回退 OpenAI", "unknown-format", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			setRequestErrorFormat(c, tc.format)

			protocolError(c, http.StatusBadGateway, "server_error", "upstream_error", "上游服务暂不可用")

			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("响应不是合法 JSON: %v", err)
			}
			if tc.wantOpenAI {
				errObj, ok := body["error"].(map[string]any)
				if !ok {
					t.Fatalf("OpenAI 格式应有 error 对象，got %v", body)
				}
				if errObj["code"] != "upstream_error" || errObj["type"] != "server_error" {
					t.Fatalf("OpenAI 错误体字段不符: %v", errObj)
				}
				if _, hasTopType := body["type"]; hasTopType {
					t.Fatalf("OpenAI 格式不应有顶层 type 字段: %v", body)
				}
			} else {
				if body["type"] != "error" {
					t.Fatalf("Anthropic 格式顶层 type 应为 error: %v", body)
				}
				errObj, ok := body["error"].(map[string]any)
				if !ok {
					t.Fatalf("Anthropic 格式应有 error 对象，got %v", body)
				}
				if errObj["type"] != "api_error" {
					t.Fatalf("server_error/502 应映射为 api_error: %v", errObj)
				}
				if _, hasCode := errObj["code"]; hasCode {
					t.Fatalf("Anthropic 错误体不应有 code 字段: %v", errObj)
				}
			}
		})
	}
}

func TestProtocolRateLimitErrorRetryAfter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	protocolRateLimitError(c, http.StatusTooManyRequests, "upstream_rate_limit", "限流", 1500*1000*1000) // 1.5s

	if got := w.Header().Get("Retry-After"); got != "2" {
		t.Fatalf("Retry-After 应向上取整为 2，got %q", got)
	}
	if got := w.Header().Get("Retry-After-Ms"); got != "1500" {
		t.Fatalf("Retry-After-Ms 应为 1500，got %q", got)
	}
}
