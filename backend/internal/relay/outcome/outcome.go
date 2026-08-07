// Package outcome 提供上游一次 attempt 的判定与出口脱敏工具，
// 由同步转发（pipeline）与异步任务（task）两个子系统共用：
// 判定表（429/401·403/5xx/网络错误 → 换渠道或禁用）是渠道健康语义的唯一事实源，
// 两条转发路径不各自维护一份。
package outcome

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Verdict 单次上游 attempt 的判决。
type Verdict int

const (
	// Success 2xx：计费 + MarkRecovered（disabled_auto 渠道恢复）。
	Success Verdict = iota
	// RateLimited 429：本次请求硬排除换渠道重试；
	// 调用方按 RetryAfter 对账号或渠道设置跨请求运行时冷却。
	RateLimited
	// AuthFailed 401/403：调用方按 401 凭证级、403 协议端点级自动禁用；恒硬排除重试。
	AuthFailed
	// Transient 5xx / 网络错误：软排除重试。
	Transient
	// ClientError 其余 4xx：语义重建终止，不重试（带 usage 仍计费）。
	ClientError
)

// Retry-After 解析边界（Retry-After 头优先，缺省 5s，钳制 [1s, 30min]）。
// 同时用于上游池耗尽响应的 Retry-After 头和账号/渠道运行时冷却。
const (
	retryAfterDefault = 5 * time.Second
	retryAfterMin     = 1 * time.Second
	retryAfterMax     = 30 * time.Minute
)

// Outcome 判决结果。
type Outcome struct {
	Verdict Verdict
	// RetryAfter 仅 RateLimited 有效：上游建议的重试等待（已钳制），
	// 供全渠道耗尽时 429 响应携带 Retry-After 头。
	RetryAfter time.Duration
	// Reason 判决原因（自动禁用落库 error_msg / 失败留痕 / 日志用）。
	Reason string
}

// Classify 按判定表分类一次上游 attempt：
//
//	网络错误            → Transient（软排除）
//	2xx                 → Success
//	429                 → RateLimited（Retry-After 优先，缺省 5s，钳 [1s,30min]）
//	401/403             → AuthFailed（仅状态码触发，不做错误体关键词匹配）
//	5xx                 → Transient
//	其余 4xx/3xx        → ClientError（语义重建终止）
func Classify(statusCode int, headers http.Header, errBody []byte, netErr error) Outcome {
	if netErr != nil {
		return Outcome{Verdict: Transient, Reason: "网络错误: " + netErr.Error()}
	}
	if statusCode >= 200 && statusCode < 300 {
		return Outcome{Verdict: Success}
	}

	snippet := BodySnippet(errBody)

	if statusCode == http.StatusTooManyRequests {
		return Outcome{
			Verdict:    RateLimited,
			RetryAfter: clampRetryAfter(parseRetryAfter(headers)),
			Reason:     "HTTP 429: " + snippet,
		}
	}
	if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
		return Outcome{Verdict: AuthFailed, Reason: "HTTP " + strconv.Itoa(statusCode) + ": " + snippet}
	}
	if statusCode >= 500 {
		return Outcome{Verdict: Transient, Reason: "HTTP " + strconv.Itoa(statusCode) + ": " + snippet}
	}
	// ClientError 的 reason 带原始体片段：响应侧只出重建后的语义字段，
	// 原始上游响应体经此片段进失败留痕供排障。
	return Outcome{Verdict: ClientError, Reason: "HTTP " + strconv.Itoa(statusCode) + ": " + snippet}
}

// parseRetryAfter 解析 Retry-After 头：整数秒优先，HTTP 日期回退；缺失/非法返回默认 5s。
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

// BodySnippet 截取错误体前 200 字节做日志/落库原因（避免超长 error_msg）。
// 截断可能切在多字节字符中间，统一清洗为合法 UTF-8（非法序列直接剔除）。
func BodySnippet(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200]
	}
	return strings.ToValidUTF8(s, "")
}
