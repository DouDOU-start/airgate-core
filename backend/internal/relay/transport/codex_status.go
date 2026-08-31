package transport

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// rewriteCodexRetryableStatus 对齐 CPA Codex executor 的 newCodexStatusErr：
// ChatGPT 常把「模型容量不足 / 用量配额耗尽」以 HTTP 400 返回，Core 的 outcome
// 只看状态码，会当成 ClientError 终止而不换号。原生插件路径不再经过 CPA，
// 因此在 Core 信任边界把这两类错误改写成 429，让账号池 failover 与模型冷却
// 继续生效。已向下游提交的流（Written）不再改写，避免中途改状态。
func rewriteCodexRetryableStatus(result *Result) {
	if result == nil || result.Written || result.StatusCode < 400 {
		return
	}
	if !isCodexModelCapacityError(result.Body) && !isCodexUsageLimitError(result.Body) {
		return
	}
	result.StatusCode = http.StatusTooManyRequests
	if retryAfter := parseCodexRetryAfter(result.Body, time.Now()); retryAfter != nil {
		if result.Headers == nil {
			result.Headers = make(http.Header)
		}
		if strings.TrimSpace(result.Headers.Get("Retry-After")) == "" {
			result.Headers.Set("Retry-After", formatRetryAfterSeconds(*retryAfter))
		}
	}
}

func isCodexModelCapacityError(errorBody []byte) bool {
	if len(errorBody) == 0 {
		return false
	}
	candidates := []string{
		gjson.GetBytes(errorBody, "error.message").String(),
		gjson.GetBytes(errorBody, "message").String(),
		string(errorBody),
	}
	for _, candidate := range candidates {
		lower := strings.ToLower(strings.TrimSpace(candidate))
		if lower == "" {
			continue
		}
		if strings.Contains(lower, "selected model is at capacity") ||
			strings.Contains(lower, "model is at capacity. please try a different model") {
			return true
		}
	}
	return false
}

func isCodexUsageLimitError(errorBody []byte) bool {
	if len(errorBody) == 0 {
		return false
	}
	candidates := []string{
		gjson.GetBytes(errorBody, "error.type").String(),
		gjson.GetBytes(errorBody, "type").String(),
	}
	for _, candidate := range candidates {
		if strings.EqualFold(strings.TrimSpace(candidate), "usage_limit_reached") {
			return true
		}
	}
	return false
}

func parseCodexRetryAfter(errorBody []byte, now time.Time) *time.Duration {
	if len(errorBody) == 0 {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(gjson.GetBytes(errorBody, "error.type").String()), "usage_limit_reached") {
		return nil
	}
	if resetsAt := gjson.GetBytes(errorBody, "error.resets_at").Int(); resetsAt > 0 {
		resetAtTime := time.Unix(resetsAt, 0)
		if resetAtTime.After(now) {
			retryAfter := resetAtTime.Sub(now)
			return &retryAfter
		}
	}
	if resetsInSeconds := gjson.GetBytes(errorBody, "error.resets_in_seconds").Int(); resetsInSeconds > 0 {
		retryAfter := time.Duration(resetsInSeconds) * time.Second
		return &retryAfter
	}
	return nil
}

func formatRetryAfterSeconds(d time.Duration) string {
	sec := int(d.Seconds())
	if sec < 1 {
		sec = 1
	}
	return strconv.Itoa(sec)
}
