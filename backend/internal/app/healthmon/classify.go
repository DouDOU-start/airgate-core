package healthmon

import "strings"

// ClassifyFailure 将 upstream 失败行映射为错误分类。
//
// 规则优先级：
//  1. phase 平台侧 precheck / 取消
//  2. HTTP 状态码（401/402/403 auth；429 rate_limit；5xx upstream）
//  3. phase 兜底（upstream_client_error → client；stream_aborted/exhausted → upstream_5xx）
//  4. other
//
// 计入 SLA error_rate 的类别：auth / rate_limit / upstream_5xx。
func ClassifyFailure(statusCode int, phase string) ErrorClass {
	phase = strings.TrimSpace(phase)
	switch phase {
	case "precheck_balance", "precheck_price", "precheck_moderation", "local_limit", "queue_timeout":
		return ClassPrecheck
	case "canceled":
		return ClassCanceled
	}

	switch {
	case statusCode == 401 || statusCode == 402 || statusCode == 403:
		return ClassAuth
	case statusCode == 429:
		return ClassRateLimit
	case statusCode >= 500 && statusCode <= 599:
		return ClassUpstream5xx
	case statusCode == 408:
		return ClassUpstream5xx
	case statusCode >= 400 && statusCode <= 499:
		return ClassClient
	}

	switch phase {
	case "upstream_client_error":
		return ClassClient
	case "stream_aborted", "upstream_exhausted":
		return ClassUpstream5xx
	case "task_timeout":
		// task 默认不进 SLA；分类仍标 other，聚合层会排除 task source。
		return ClassOther
	}

	if statusCode == 0 {
		// 网络错误等无状态码：视作上游瞬态。
		return ClassUpstream5xx
	}
	return ClassOther
}

// CountsTowardErrorRate 是否计入 error_rate / 分母 E。
func CountsTowardErrorRate(class ErrorClass) bool {
	switch class {
	case ClassAuth, ClassRateLimit, ClassUpstream5xx:
		return true
	default:
		return false
	}
}
