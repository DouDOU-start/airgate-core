// Package outcome 提供上游一次 attempt 的判定与出口脱敏工具，
// 由同步转发（pipeline）与异步任务（task）两个子系统共用。
//
// # 调度判定总表（唯一事实源）
//
// 判定只回答「这个调度单元（账号/渠道 key）接下来怎么办」，不负责出口文案形态
// （出口由 errfmt / writeAllFailed 处理）。铁律：
//
//   - 仅按 HTTP 状态码判定，禁止错误体关键词触发禁用（防用户构造文案打禁）。
//   - 「账号/凭证已死」→ 禁用 + 本轮硬排除换号；绝不能 ClientError（不换号）或
//     RateLimited（会被超额插件 AllowRateLimited 再次放行）。
//   - 「请求本身有问题」→ ClientError 终止，换号无意义。
//   - 「临时限流」→ 冷却，不永久禁用。
//
// | 状态码 | Verdict | 本轮 | 跨请求（账号） | 跨请求（渠道） | 说明 |
// |--------|---------|------|----------------|----------------|------|
// | 2xx | Success | 成功 | MarkActive | MarkRecovered | |
// | 400 | ClientError | 终止 | — | — | 参数/协议错误 |
// | 401 | AuthFailed | 硬排除换号 | MarkDisabled | 凭证级禁用 | token/key 失效 |
// | 402 | AuthFailed | 硬排除换号 | MarkDisabled | 端点级禁用 | 配额/订阅不可用；Codex 超额常见 |
// | 403 | AuthFailed | 硬排除换号 | MarkDisabled | 端点级禁用 | 权限拒绝；部分 OAuth 用 403 表 token 过期（CPA 可先 refresh） |
// | 404 | ClientError | 终止* | — | — | *Responses 端点 404 由 pipeline 特判 soft 换渠道 |
// | 405–407 | ClientError | 终止 | — | — | |
// | 408 | Transient | 软排除换号 | — | — | 请求超时，换单元可能恢复 |
// | 409–428 | ClientError | 终止 | — | — | 含 413/415/422 等请求侧错误 |
// | 429 | RateLimited | 硬排除换号 | MarkRateLimited | MarkRateLimited | Retry-After 冷却；超额插件可例外放行 |
// | 431/451 等其余 4xx | ClientError | 终止 | — | — | 默认保守：不禁号、不换号 |
// | 5xx | Transient | 软排除换号 | — | — | |
// | 网络错误 | Transient | 软排除换号 | — | — | 无 HTTP 状态 |
//
// 调用方动作约定（pipeline / task 共用）：
//
//	Success     → 计费（若适用）+ 恢复标记 + 写出成功体
//	RateLimited → 硬排除 + 按 RetryAfter 冷却；本请求 failover
//	AuthFailed  → 硬排除 + AutoBan 时禁用；本请求 failover
//	Transient   → 软排除；本请求 failover
//	ClientError → 语义重建终止（保留上游状态码）；不 failover
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
	// RateLimited 429：本次请求硬排除换渠道/账号重试；
	// 调用方按 RetryAfter 对账号或渠道设置跨请求运行时冷却。
	// 注意：超额插件可显式 AllowRateLimited 放行 Codex OAuth；因此「账号已死」
	// 类错误（402）绝不可归入本桶。
	RateLimited
	// AuthFailed 401/402/403：调度单元不可用——调用方禁用并硬排除换号。
	// 账号 MarkDisabled；渠道 401 凭证级、402/403 端点级禁用。
	AuthFailed
	// Transient 5xx / 408 / 网络错误：软排除重试，不永久禁用。
	Transient
	// ClientError 请求侧 4xx：语义重建终止，不重试（带 usage 仍计费）。
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

// Classify 按判定总表分类一次上游 attempt（见包注释）。
// 仅状态码触发，不做错误体关键词匹配。
func Classify(statusCode int, headers http.Header, errBody []byte, netErr error) Outcome {
	if netErr != nil {
		return Outcome{Verdict: Transient, Reason: "网络错误: " + netErr.Error()}
	}
	if statusCode >= 200 && statusCode < 300 {
		return Outcome{Verdict: Success}
	}

	snippet := BodySnippet(errBody)
	reason := "HTTP " + strconv.Itoa(statusCode) + ": " + snippet

	switch statusCode {
	case http.StatusTooManyRequests: // 429
		return Outcome{
			Verdict:    RateLimited,
			RetryAfter: clampRetryAfter(parseRetryAfter(headers)),
			Reason:     reason,
		}
	case http.StatusUnauthorized, // 401
		http.StatusPaymentRequired, // 402 配额/订阅不可用：直接禁用账号并切号
		http.StatusForbidden:       // 403
		return Outcome{Verdict: AuthFailed, Reason: reason}
	case http.StatusRequestTimeout: // 408：超时偏瞬时，软换号
		return Outcome{Verdict: Transient, Reason: reason}
	}

	if statusCode >= 500 {
		return Outcome{Verdict: Transient, Reason: reason}
	}

	// 其余 4xx/3xx：请求侧错误，语义重建终止（reason 带原始体片段供排障）。
	return Outcome{Verdict: ClientError, Reason: reason}
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
