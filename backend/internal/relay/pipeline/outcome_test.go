package pipeline

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestClassifyOutcome(t *testing.T) {
	keywords := defaultBanKeywords

	cases := []struct {
		name           string
		status         int
		headers        http.Header
		body           string
		netErr         error
		wantVerdict    verdict
		wantRetryAfter time.Duration // 仅 rateLimited 校验
	}{
		{
			name:        "网络错误",
			netErr:      errors.New("dial tcp: connection refused"),
			wantVerdict: verdictTransient,
		},
		{
			name:        "2xx 成功",
			status:      200,
			wantVerdict: verdictSuccess,
		},
		{
			name:           "429 无 Retry-After 用默认 60s",
			status:         429,
			body:           `{"error":{"message":"rate limited"}}`,
			wantVerdict:    verdictRateLimited,
			wantRetryAfter: 60 * time.Second,
		},
		{
			name:           "429 带 Retry-After 秒数",
			status:         429,
			headers:        http.Header{"Retry-After": []string{"120"}},
			wantVerdict:    verdictRateLimited,
			wantRetryAfter: 120 * time.Second,
		},
		{
			name:           "429 Retry-After 低于下界钳到 1s",
			status:         429,
			headers:        http.Header{"Retry-After": []string{"0"}},
			wantVerdict:    verdictRateLimited,
			wantRetryAfter: time.Second,
		},
		{
			name:           "429 Retry-After 超上界钳到 30min",
			status:         429,
			headers:        http.Header{"Retry-After": []string{"7200"}},
			wantVerdict:    verdictRateLimited,
			wantRetryAfter: 30 * time.Minute,
		},
		{
			name:        "401 认证失败",
			status:      401,
			body:        `{"error":{"message":"bad key"}}`,
			wantVerdict: verdictAuthFailed,
		},
		{
			name:        "403 认证失败",
			status:      403,
			wantVerdict: verdictAuthFailed,
		},
		{
			// 关键词只在 401/403 生效：上游 400 会回显用户输入，
			// 任意用户可构造关键词字符串，不得据此自动禁用渠道。
			name:        "400 关键词命中不再触发自动禁用（透传终止）",
			status:      400,
			body:        `{"error":{"code":"INVALID_API_KEY","message":"Incorrect API Key provided"}}`,
			wantVerdict: verdictClientError,
		},
		{
			name:        "422 关键词命中不触发自动禁用",
			status:      422,
			body:        `{"error":{"message":"Invalid value: 'insufficient_quota'. Supported values are ..."}}`,
			wantVerdict: verdictClientError,
		},
		{
			name:        "401 关键词命中仍判认证失败",
			status:      401,
			body:        `{"error":{"code":"invalid_api_key"}}`,
			wantVerdict: verdictAuthFailed,
		},
		{
			name:        "500 关键词命中仍按上游故障处理",
			status:      500,
			body:        `{"error":{"message":"insufficient_quota"}}`,
			wantVerdict: verdictTransient,
		},
		{
			name:        "500 上游故障",
			status:      500,
			body:        "internal error",
			wantVerdict: verdictTransient,
		},
		{
			name:        "503 上游故障",
			status:      503,
			wantVerdict: verdictTransient,
		},
		{
			name:        "普通 400 透传终止",
			status:      400,
			body:        `{"error":{"message":"messages is required"}}`,
			wantVerdict: verdictClientError,
		},
		{
			name:        "404 透传终止",
			status:      404,
			body:        `{"error":{"message":"model not found"}}`,
			wantVerdict: verdictClientError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			headers := tc.headers
			if headers == nil {
				headers = http.Header{}
			}
			got := classifyOutcome(tc.status, headers, []byte(tc.body), tc.netErr, keywords)
			if got.verdict != tc.wantVerdict {
				t.Fatalf("verdict = %v, want %v (reason=%q)", got.verdict, tc.wantVerdict, got.reason)
			}
			if tc.wantVerdict == verdictRateLimited && got.retryAfter != tc.wantRetryAfter {
				t.Errorf("retryAfter = %v, want %v", got.retryAfter, tc.wantRetryAfter)
			}
		})
	}
}

func TestMatchBanKeyword(t *testing.T) {
	keywords := []string{"invalid_api_key", "account deactivated"}
	cases := []struct {
		name string
		body string
		want string
	}{
		{"命中小写", `{"code":"invalid_api_key"}`, "invalid_api_key"},
		{"命中大写", `{"code":"INVALID_API_KEY"}`, "invalid_api_key"},
		{"命中混合大小写短语", `Account Deactivated by admin`, "account deactivated"},
		{"未命中", `{"message":"try later"}`, ""},
		{"空体", ``, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchBanKeyword([]byte(tc.body), keywords); got != tc.want {
				t.Errorf("matchBanKeyword = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseRetryAfter(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{"缺失用默认", "", cooldownDefault},
		{"整数秒", "30", 30 * time.Second},
		{"非法值用默认", "soon", cooldownDefault},
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
