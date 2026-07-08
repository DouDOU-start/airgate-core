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
	// verdictRateLimited 429：MarkCooldown + 硬排除重试。
	verdictRateLimited
	// verdictAuthFailed 401/403：channel_auto_ban_enabled 时 MarkAutoDisabled；恒硬排除重试。
	// 禁用关键词只在 401/403 状态下参与判定原因，其他状态一律不触发自动禁用
	//（上游 400 会回显用户输入，任意用户可构造关键词字符串打禁渠道）。
	verdictAuthFailed
	// verdictTransient 5xx / 网络错误：软排除重试。
	verdictTransient
	// verdictClientError 其余 4xx：透传终止，不重试（带 usage 仍计费）。
	verdictClientError
)

// 429 冷却时长边界（契约：Retry-After 优先，缺省 60s，钳制 [1s, 30min]）。
const (
	cooldownDefault = 60 * time.Second
	cooldownMin     = 1 * time.Second
	cooldownMax     = 30 * time.Minute
)

// outcome 判决结果。
type outcome struct {
	verdict verdict
	// retryAfter 仅 verdictRateLimited 有效：冷却时长（已钳制）。
	retryAfter time.Duration
	// reason 判决原因（自动禁用落库 error_msg / 日志用）。
	reason string
}

// classifyOutcome 按判定表分类一次上游 attempt：
//
//	网络错误            → transient（软排除）
//	2xx                 → success
//	429                 → rateLimited（Retry-After 优先，缺省 60s，钳 [1s,30min]）
//	401/403             → authFailed（关键词命中时补充进 reason）
//	5xx                 → transient
//	其余 4xx/3xx        → clientError（透传终止）
//
// 禁用关键词匹配只在 401/403 状态下生效：上游参数校验类 4xx（400/404/422 等）
// 会原样回显用户输入，若对其做关键词匹配，任意持 key 用户可构造含关键词的
// 非法参数批量打禁渠道（渠道级 DoS）。
func classifyOutcome(statusCode int, headers http.Header, errBody []byte, netErr error, banKeywords []string) outcome {
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
			retryAfter: clampCooldown(parseRetryAfter(headers)),
			reason:     "HTTP 429: " + snippet,
		}
	}
	if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
		reason := "HTTP " + strconv.Itoa(statusCode) + ": " + snippet
		if kw := matchBanKeyword(errBody, banKeywords); kw != "" {
			reason = "关键词命中 [" + kw + "] " + reason
		}
		return outcome{verdict: verdictAuthFailed, reason: reason}
	}
	if statusCode >= 500 {
		return outcome{verdict: verdictTransient, reason: "HTTP " + strconv.Itoa(statusCode) + ": " + snippet}
	}
	return outcome{verdict: verdictClientError, reason: "HTTP " + strconv.Itoa(statusCode)}
}

// matchBanKeyword 大小写不敏感地在错误体中匹配关键词表，返回命中的关键词（未命中返回空串）。
// keywords 已在 settings 解析时统一小写。仅在 401/403 状态下调用。
func matchBanKeyword(errBody []byte, keywords []string) string {
	if len(errBody) == 0 || len(keywords) == 0 {
		return ""
	}
	lower := strings.ToLower(string(errBody))
	for _, kw := range keywords {
		if kw != "" && strings.Contains(lower, kw) {
			return kw
		}
	}
	return ""
}

// parseRetryAfter 解析 Retry-After 头：整数秒优先，HTTP 日期回退；缺失/非法返回默认 60s。
func parseRetryAfter(headers http.Header) time.Duration {
	value := strings.TrimSpace(headers.Get("Retry-After"))
	if value == "" {
		return cooldownDefault
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		return time.Until(at)
	}
	return cooldownDefault
}

// clampCooldown 冷却时长钳制到 [1s, 30min]。
func clampCooldown(d time.Duration) time.Duration {
	if d < cooldownMin {
		return cooldownMin
	}
	if d > cooldownMax {
		return cooldownMax
	}
	return d
}

// bodySnippet 截取错误体前 200 字节做日志/落库原因（避免超长 error_msg）。
func bodySnippet(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
