package healthmon

import "strings"

// ClassifyFailure 将 upstream 失败行映射为错误分类。
//
// 规则优先级：
//  1. phase 平台侧 precheck / 本地闸门 / 取消 / 坏请求
//  2. error_code 兜底（phase 丢失时仍能识别余额不足、缺价等）
//  3. HTTP 状态码（401/403 auth；429 rate_limit；5xx/408 upstream）
//  4. 402：仅上游路径（upstream_*）才算 auth；
//     网关余额不足走 precheck_balance，不计入 SLA
//  5. phase 兜底（upstream_client_error → client；stream_aborted/exhausted → upstream_5xx）
//  6. other
//
// 计入 SLA error_rate 的类别：auth / rate_limit / upstream_5xx。
// precheck（余额不足、缺价、倍率、审核、本地限流等）与 client 4xx 不计入。
func ClassifyFailure(statusCode int, phase, errorCode string) ErrorClass {
	phase = strings.TrimSpace(phase)
	errorCode = strings.TrimSpace(errorCode)

	if class, ok := classifyByPhase(phase); ok {
		return class
	}
	if class, ok := classifyByErrorCode(errorCode); ok {
		return class
	}

	switch {
	case statusCode == 401 || statusCode == 403:
		return ClassAuth
	case statusCode == 402:
		// 上游鉴权/欠费切号：phase 通常为 upstream_exhausted 等；
		// 网关余额预检必须带 precheck_balance 或 insufficient_balance。
		// 裸 402 保守归 client，避免把用户余额不足算进运维失败率。
		if strings.HasPrefix(phase, "upstream_") {
			return ClassAuth
		}
		return ClassClient
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

func classifyByPhase(phase string) (ErrorClass, bool) {
	switch phase {
	case "precheck_balance", "precheck_price", "precheck_rate",
		"precheck_client_restrict", "precheck_moderation",
		"local_limit", "queue_timeout":
		return ClassPrecheck, true
	case "canceled":
		return ClassCanceled, true
	case "bad_request":
		return ClassClient, true
	default:
		// 兼容未来 precheck_* 扩展，统一不进 SLA。
		if strings.HasPrefix(phase, "precheck_") {
			return ClassPrecheck, true
		}
		return "", false
	}
}

func classifyByErrorCode(code string) (ErrorClass, bool) {
	switch code {
	case "insufficient_balance", "model_price_not_configured",
		"billing_rate_exceeded", "client_restricted":
		return ClassPrecheck, true
	default:
		return "", false
	}
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
