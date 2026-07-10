package pipeline

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// verdict 单次上游 attempt 的判决。
type verdict int

const (
	// verdictSuccess 2xx：计费 + MarkRecovered（disabled_auto 渠道恢复）。
	verdictSuccess verdict = iota
	// verdictRateLimited 429：本次请求硬排除换渠道重试；
	// 不设冷却状态，下次请求该渠道照常参与调度。
	verdictRateLimited
	// verdictAuthFailed 401/403：channel_auto_ban_enabled 时 MarkAutoDisabled；恒硬排除重试。
	verdictAuthFailed
	// verdictTransient 5xx / 网络错误：软排除重试。
	verdictTransient
	// verdictClientError 其余 4xx：语义重建终止，不重试（带 usage 仍计费）。
	verdictClientError
)

// Retry-After 解析边界（Retry-After 头优先，缺省 60s，钳制 [1s, 30min]）。
// 仅用于全渠道耗尽时 429 响应的 Retry-After 头，不再驱动任何渠道冷却状态。
const (
	retryAfterDefault = 60 * time.Second
	retryAfterMin     = 1 * time.Second
	retryAfterMax     = 30 * time.Minute
)

// outcome 判决结果。
type outcome struct {
	verdict verdict
	// retryAfter 仅 verdictRateLimited 有效：上游建议的重试等待（已钳制），
	// 供全渠道耗尽时 429 响应携带 Retry-After 头。
	retryAfter time.Duration
	// reason 判决原因（自动禁用落库 error_msg / 失败留痕 / 日志用）。
	reason string
}

// classifyOutcome 按判定表分类一次上游 attempt：
//
//	网络错误            → transient（软排除）
//	2xx                 → success
//	429                 → rateLimited（Retry-After 优先，缺省 60s，钳 [1s,30min]）
//	401/403             → authFailed（仅状态码触发，不做错误体关键词匹配）
//	5xx                 → transient
//	其余 4xx/3xx        → clientError（语义重建终止）
func classifyOutcome(statusCode int, headers http.Header, errBody []byte, netErr error) outcome {
	if netErr != nil {
		return outcome{verdict: verdictTransient, reason: "网络错误: " + netErr.Error()}
	}
	if statusCode >= 200 && statusCode < 300 {
		return outcome{verdict: verdictSuccess}
	}

	snippet := bodySnippet(errBody)

	if statusCode == http.StatusTooManyRequests {
		return outcome{
			verdict:    verdictRateLimited,
			retryAfter: clampRetryAfter(parseRetryAfter(headers)),
			reason:     "HTTP 429: " + snippet,
		}
	}
	if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
		return outcome{verdict: verdictAuthFailed, reason: "HTTP " + strconv.Itoa(statusCode) + ": " + snippet}
	}
	if statusCode >= 500 {
		return outcome{verdict: verdictTransient, reason: "HTTP " + strconv.Itoa(statusCode) + ": " + snippet}
	}
	// clientError 的 reason 带原始体片段：响应侧只出重建后的语义字段，
	// 原始上游响应体经此片段进失败留痕供排障。
	return outcome{verdict: verdictClientError, reason: "HTTP " + strconv.Itoa(statusCode) + ": " + snippet}
}

// parseRetryAfter 解析 Retry-After 头：整数秒优先，HTTP 日期回退；缺失/非法返回默认 60s。
func parseRetryAfter(headers http.Header) time.Duration {
	value := strings.TrimSpace(headers.Get("Retry-After"))
	if value == "" {
		return retryAfterDefault
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		return time.Until(at)
	}
	return retryAfterDefault
}

// clampRetryAfter 重试等待时长钳制到 [1s, 30min]。
func clampRetryAfter(d time.Duration) time.Duration {
	if d < retryAfterMin {
		return retryAfterMin
	}
	if d > retryAfterMax {
		return retryAfterMax
	}
	return d
}

// bodySnippet 截取错误体前 200 字节做日志/落库原因（避免超长 error_msg）。
// 截断可能切在多字节字符中间，统一清洗为合法 UTF-8（非法序列直接剔除）。
func bodySnippet(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200]
	}
	return strings.ToValidUTF8(s, "")
}
