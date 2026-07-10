package outcome

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestClassifyOutcome(t *testing.T) {
	cases := []struct {
		name           string
		status         int
		headers        http.Header
		body           string
		netErr         error
		wantVerdict    Verdict
		wantRetryAfter time.Duration // 仅 rateLimited 校验
	}{
		{
			name:        "网络错误",
			netErr:      errors.New("dial tcp: connection refused"),
			wantVerdict: Transient,
		},
		{
			name:        "2xx 成功",
			status:      200,
			wantVerdict: Success,
		},
		{
			name:           "429 无 Retry-After 用默认 60s",
			status:         429,
			body:           `{"error":{"message":"rate limited"}}`,
			wantVerdict:    RateLimited,
			wantRetryAfter: 60 * time.Second,
		},
		{
			name:           "429 带 Retry-After 秒数",
			status:         429,
			headers:        http.Header{"Retry-After": []string{"120"}},
			wantVerdict:    RateLimited,
			wantRetryAfter: 120 * time.Second,
		},
		{
			name:           "429 Retry-After 低于下界钳到 1s",
			status:         429,
			headers:        http.Header{"Retry-After": []string{"0"}},
			wantVerdict:    RateLimited,
			wantRetryAfter: time.Second,
		},
		{
			name:           "429 Retry-After 超上界钳到 30min",
			status:         429,
			headers:        http.Header{"Retry-After": []string{"7200"}},
			wantVerdict:    RateLimited,
			wantRetryAfter: 30 * time.Minute,
		},
		{
			name:        "401 认证失败",
			status:      401,
			body:        `{"error":{"message":"bad key"}}`,
			wantVerdict: AuthFailed,
		},
		{
			name:        "403 认证失败",
			status:      403,
			wantVerdict: AuthFailed,
		},
		{
			// 自动禁用仅由 401/403 状态码触发：错误体内容不参与判定
			//（上游 400 会回显用户输入，据内容判定会被任意用户构造打禁渠道）。
			name:        "400 错误体含凭证类文案不触发自动禁用（语义重建终止）",
			status:      400,
			body:        `{"error":{"code":"INVALID_API_KEY","message":"Incorrect API Key provided"}}`,
			wantVerdict: ClientError,
		},
		{
			name:        "500 错误体含凭证类文案仍按上游故障处理",
			status:      500,
			body:        `{"error":{"message":"insufficient_quota"}}`,
			wantVerdict: Transient,
		},
		{
			name:        "500 上游故障",
			status:      500,
			body:        "internal error",
			wantVerdict: Transient,
		},
		{
			name:        "503 上游故障",
			status:      503,
			wantVerdict: Transient,
		},
		{
			name:        "普通 400 语义重建终止",
			status:      400,
			body:        `{"error":{"message":"messages is required"}}`,
			wantVerdict: ClientError,
		},
		{
			name:        "404 语义重建终止",
			status:      404,
			body:        `{"error":{"message":"model not found"}}`,
			wantVerdict: ClientError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			headers := tc.headers
			if headers == nil {
				headers = http.Header{}
			}
			got := Classify(tc.status, headers, []byte(tc.body), tc.netErr)
			if got.Verdict != tc.wantVerdict {
				t.Fatalf("verdict = %v, want %v (reason=%q)", got.Verdict, tc.wantVerdict, got.Reason)
			}
			if tc.wantVerdict == RateLimited && got.RetryAfter != tc.wantRetryAfter {
				t.Errorf("retryAfter = %v, want %v", got.RetryAfter, tc.wantRetryAfter)
			}
		})
	}
}

// TestClassifyOutcomeClientErrorReasonHasSnippet clientError 的 reason 带原始体片段：
// 响应侧只出重建后的语义字段，原始上游错误体经 reason 进失败留痕供排障。
func TestClassifyOutcomeClientErrorReasonHasSnippet(t *testing.T) {
	got := Classify(400, http.Header{}, []byte(`{"error":{"message":"bad param"}}`), nil)
	if got.Verdict != ClientError {
		t.Fatalf("verdict = %v, want clientError", got.Verdict)
	}
	if !strings.Contains(got.Reason, "HTTP 400") || !strings.Contains(got.Reason, "bad param") {
		t.Errorf("reason = %q, want 含状态码与原始体片段", got.Reason)
	}
}

func TestParseRetryAfter(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{"缺失用默认", "", retryAfterDefault},
		{"整数秒", "30", 30 * time.Second},
		{"非法值用默认", "soon", retryAfterDefault},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			headers := http.Header{}
			if tc.value != "" {
				headers.Set("Retry-After", tc.value)
			}
			if got := parseRetryAfter(headers); got != tc.want {
				t.Errorf("parseRetryAfter = %v, want %v", got, tc.want)
			}
		})
	}

	t.Run("HTTP 日期格式", func(t *testing.T) {
		headers := http.Header{}
		headers.Set("Retry-After", time.Now().Add(90*time.Second).UTC().Format(http.TimeFormat))
		got := parseRetryAfter(headers)
		if got < 80*time.Second || got > 91*time.Second {
			t.Errorf("parseRetryAfter(HTTP date) = %v, want ~90s", got)
		}
	})
}
