// Package usagemodel 暴露用量统计共用的模型分类规则。
// 抽出来是为了让 infra/store/dashboard_store.go 等处的"生图家族"判定
// 保持同一份口径，避免 gpt-image-* 系列改名时漏改导致统计对不上。
package usagemodel

// ImagePrefix 是生图家族模型 ID 的统一前缀（"gpt-image-*" 规则）。
const ImagePrefix = "gpt-image"
