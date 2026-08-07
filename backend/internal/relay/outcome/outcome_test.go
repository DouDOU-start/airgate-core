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
		// ----- 非 HTTP / 成功 -----
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
			name:        "201 成功",
			status:      201,
			wantVerdict: Success,
		},

		// ----- 429 RateLimited -----
		{
			name:           "429 无 Retry-After 用默认 5s",
			status:         429,
			body:           `{"error":{"message":"rate limited"}}`,
			wantVerdict:    RateLimited,
			wantRetryAfter: 5 * time.Second,
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

		// ----- 401/402/403 AuthFailed（禁用 + 切号）-----
		{
			name:        "401 认证失败",
			status:      401,
			body:        `{"error":{"message":"bad key"}}`,
			wantVerdict: AuthFailed,
		},
		{
			// Codex/订阅账号配额耗尽常见 402：须 AuthFailed 禁用并切号，
			// 不能 ClientError（不换号）也不能 RateLimited（会被超额插件再放行）。
			name:        "402 配额/订阅不可用",
			status:      402,
			body:        `{"error":{"message":"insufficient_quota","type":"insufficient_quota"}}`,
			wantVerdict: AuthFailed,
		},
		{
			name:        "403 认证失败",
			status:      403,
			wantVerdict: AuthFailed,
		},

		// ----- 408 Transient -----
		{
			name:        "408 请求超时软换号",
			status:      408,
			body:        "request timeout",
			wantVerdict: Transient,
		},

		// ----- ClientError：请求侧 4xx，不禁号不换号 -----
		{
			// 自动禁用仅由 401/402/403 状态码触发：错误体内容不参与判定
			//（上游 400 会回显用户输入，据内容判定会被任意用户构造打禁渠道）。
			name:        "400 错误体含凭证类文案不触发自动禁用",
			status:      400,
			body:        `{"error":{"code":"INVALID_API_KEY","message":"Incorrect API Key provided"}}`,
			wantVerdict: ClientError,
		},
		{
			name:        "400 普通参数错误",
			status:      400,
			body:        `{"error":{"message":"messages is required"}}`,
			wantVerdict: ClientError,
		},
		{
			name:        "404 模型/路径不存在",
			status:      404,
			body:        `{"error":{"message":"model not found"}}`,
			wantVerdict: ClientError,
		},
		{
			name:        "405 方法不允许",
			status:      405,
			wantVerdict: ClientError,
		},
		{
			name:        "409 冲突",
			status:      409,
			wantVerdict: ClientError,
		},
		{
			name:        "413 请求体过大",
			status:      413,
			wantVerdict: ClientError,
		},
		{
			name:        "415 不支持的媒体类型",
			status:      415,
			wantVerdict: ClientError,
		},
		{
			name:        "422 语义校验失败",
			status:      422,
			body:        `{"error":{"message":"unprocessable"}}`,
			wantVerdict: ClientError,
		},
		{
			name:        "431 请求头过大",
			status:      431,
			wantVerdict: ClientError,
		},
		{
			name:        "451 法律原因不可用",
			status:      451,
			wantVerdict: ClientError,
		},
		{
			name:        "418 其余 4xx 默认 ClientError",
			status:      418,
			wantVerdict: ClientError,
		},

		// ----- 5xx Transient -----
		{
			name:        "500 错误体含配额文案仍按上游故障（不靠 body 判禁用）",
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
			name:        "502 上游故障",
			status:      502,
			wantVerdict: Transient,
		},
		{
			name:        "503 上游故障",
			status:      503,
			wantVerdict: Transient,
		},
		{
			name:        "529 过载按 5xx 软换号",
			status:      529,
			wantVerdict: Transient,
		},

		// ----- 3xx 保守 ClientError -----
		{
			name:        "301 重定向不按成功",
			status:      301,
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

// TestClassifyAuthFailedReasonHasStatus 401/402/403 reason 带状态码，便于禁用落库排障。
func TestClassifyAuthFailedReasonHasStatus(t *testing.T) {
	for _, code := range []int{401, 402, 403} {
		got := Classify(code, nil, []byte(`{"error":"x"}`), nil)
		if got.Verdict != AuthFailed {
			t.Fatalf("HTTP %d verdict = %v, want AuthFailed", code, got.Verdict)
		}
		prefix := "HTTP "
		switch code {
		case 401:
			prefix += "401"
		case 402:
			prefix += "402"
		case 403:
			prefix += "403"
		}
		if !strings.Contains(got.Reason, prefix) {
			t.Errorf("HTTP %d reason = %q, want 含 %q", code, got.Reason, prefix)
		}
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
