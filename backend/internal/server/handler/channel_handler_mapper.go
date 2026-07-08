package handler

import (
	appchannel "github.com/DouDOU-start/airgate-core/internal/app/channel"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
)

// toChannelRespFromDomain 领域对象 → 响应 DTO。
// api_keys 密文不映射出去，仅回数量与尾 4 位提示。
func toChannelRespFromDomain(item appchannel.Channel) dto.ChannelResp {
	return dto.ChannelResp{
		ID:             int64(item.ID),
		Name:           item.Name,
		Type:           item.Type,
		BaseURL:        item.BaseURL,
		APIKeysCount:   len(item.APIKeys),
		APIKeyHints:    emptyIfNilStrings(item.APIKeyHints),
		Models:         emptyIfNilStrings(item.Models),
		ModelMapping:   item.ModelMapping,
		ParamOverride:  item.ParamOverride,
		HeaderOverride: item.HeaderOverride,
		Status:         item.Status,
		StatusUntil:    item.StatusUntil,
		ErrorMsg:       item.ErrorMsg,
		Priority:       item.Priority,
		Weight:         item.Weight,
		MaxConcurrency: item.MaxConcurrency,
		MaxRPM:         item.MaxRPM,
		CostRatio:      item.CostRatio,
		Tags:           emptyIfNilStrings(item.Tags),
		TestModel:      item.TestModel,
		CustomConfig:   item.CustomConfig,
		ResponseTimeMs: item.ResponseTimeMs,
		TestedAt:       item.TestedAt,
		LastUsedAt:     item.LastUsedAt,
		GroupIDs:       emptyIfNilInts(item.GroupIDs),
		TimeMixin: dto.TimeMixin{
			CreatedAt: item.CreatedAt,
			UpdatedAt: item.UpdatedAt,
		},
	}
}

// emptyIfNilStrings 将 nil 切片归一为空切片，保证 JSON 输出 [] 而非 null。
func emptyIfNilStrings(items []string) []string {
	if items == nil {
		return []string{}
	}
	return items
}

// emptyIfNilInts 将 nil 切片归一为空切片，保证 JSON 输出 [] 而非 null。
func emptyIfNilInts(items []int) []int {
	if items == nil {
		return []int{}
	}
	return items
}
