// Package usagemodel 暴露用量统计共用的模型分类规则。
// 抽出来是为了让 app/account/stats.go、infra/store/dashboard_store.go、
// infra/store/account_store.go 这几处的"生图家族"判定保持同一份口径，
// 避免 gpt-image-* 系列改名时漏改某一处导致统计与列表对不上。
package usagemodel

import "strings"

// ImagePrefix 是生图家族模型 ID 的统一前缀，与 scheduler.ModelFamily 的
// "gpt-image-*" 规则一致。
const ImagePrefix = "gpt-image"

// IsImageGen 判断给定 model ID 是否属于生图家族。
// 不直接 import scheduler 包是为了让 stats / store 层不依赖调度模块。
func IsImageGen(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), ImagePrefix)
}
