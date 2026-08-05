package cpa

import "strings"

// DefaultModels 返回 platform 的默认可服务模型 ID 列表。
// 数据来自 CPA 嵌入目录（embed/models.json，对齐 CLIProxyAPI models.json）。
// 未知平台返回空切片。
//
// 无 plan 信息时 Codex 使用 pro 档（与 CPA OAuth 默认一致）。
// 需要分档时请用 DefaultModelsWithPlan / DefaultModelInfos。
func DefaultModels(platform string) []string {
	return DefaultModelsWithPlan(platform, "")
}

// DefaultModelsWithPlan 同 DefaultModels，Codex 按 planType 选 free/plus/team/pro。
func DefaultModelsWithPlan(platform, planType string) []string {
	return ModelIDs(DefaultModelInfos(platform, planType))
}

// DefaultModelSet 返回 platform 默认模型集合（O(1) 命中）。
func DefaultModelSet(platform string) map[string]struct{} {
	return DefaultModelSetWithPlan(platform, "")
}

// DefaultModelSetWithPlan 同 DefaultModelSet，支持 Codex plan 分档。
func DefaultModelSetWithPlan(platform, planType string) map[string]struct{} {
	list := DefaultModelsWithPlan(platform, planType)
	if len(list) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(list))
	for _, m := range list {
		set[m] = struct{}{}
	}
	return set
}

// LookupModelDisplayName 在 CPA 静态目录中查展示名；找不到返回 id 本身。
func LookupModelDisplayName(platform, planType, modelID string) string {
	id := strings.TrimSpace(modelID)
	if id == "" {
		return ""
	}
	for _, m := range DefaultModelInfos(platform, planType) {
		if m.ID == id {
			if m.DisplayName != "" {
				return m.DisplayName
			}
			return id
		}
	}
	return id
}

// SupportedPlatforms 返回桥接层内建支持的平台标识列表。
func SupportedPlatforms() []string {
	return SupportedProviders()
}
