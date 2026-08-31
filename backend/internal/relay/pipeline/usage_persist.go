package pipeline

import "github.com/DouDOU-start/airgate-core/internal/billing"

// persistableUsageStatus 决定是否写入 usage_log。
// 计量缺失 / 中断且无计量 没有可对账用量，不应污染使用记录与请求计数；
// 失败语义由调用方打 WARN 并写入失败留痕。
func persistableUsageStatus(status string) bool {
	switch status {
	case billing.UsageStatusMissing, billing.UsageStatusStreamAbortedUsageMissing:
		return false
	default:
		return true
	}
}
