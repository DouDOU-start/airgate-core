package handler

import (
	appchannel "github.com/DouDOU-start/airgate-core/internal/app/channel"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
)

// toChannelRespFromDomain 领域对象 → 响应 DTO（含其下各 key）。
// 明文密钥不映射出去，仅回各 key 的尾 4 位提示。
func toChannelRespFromDomain(item appchannel.Channel) dto.ChannelResp {
	keys := make([]dto.ChannelKeyResp, 0, len(item.Keys))
	for _, k := range item.Keys {
		keys = append(keys, toChannelKeyResp(k))
	}
	return dto.ChannelResp{
		ID:               int64(item.ID),
		Name:             item.Name,
		BaseURL:          item.BaseURL,
		Balance:          item.Balance,
		BalanceUpdatedAt: item.BalanceUpdatedAt,
		Keys:             keys,
		TotalCost:        item.TotalCost,
		TotalRevenue:     item.TotalRevenue,
		TodayCost:        item.TodayCost,
		TodayRevenue:     item.TodayRevenue,
		TimeMixin: dto.TimeMixin{
			CreatedAt: item.CreatedAt,
			UpdatedAt: item.UpdatedAt,
		},
	}
}

// toChannelKeyResp 密钥端点领域对象 → 响应 DTO。
func toChannelKeyResp(k appchannel.ChannelKey) dto.ChannelKeyResp {
	return dto.ChannelKeyResp{
		ID:               int64(k.ID),
		ChannelID:        int64(k.ChannelID),
		Name:             k.Name,
		Type:             k.Type,
		APIKeyHint:       k.APIKeyHint,
		Models:           emptyIfNilStrings(k.Models),
		ModelMapping:     k.ModelMapping,
		ParamOverride:    k.ParamOverride,
		HeaderOverride:   k.HeaderOverride,
		Status:           k.Status,
		ErrorMsg:         k.ErrorMsg,
		Priority:         k.Priority,
		Weight:           k.Weight,
		MaxConcurrency:   k.MaxConcurrency,
		MaxRPM:           k.MaxRPM,
		CostRatio:        k.CostRatio,
		Tags:             emptyIfNilStrings(k.Tags),
		TestModel:        k.TestModel,
		ResponseTimeMs:   k.ResponseTimeMs,
		TestedAt:         k.TestedAt,
		LastUsedAt:       k.LastUsedAt,
		GroupIDs:         emptyIfNilInts(k.GroupIDs),
		Balance:          k.Balance,
		BalanceUpdatedAt: k.BalanceUpdatedAt,

		CurrentConcurrency: k.CurrentConcurrency,
		CurrentRPM:         k.CurrentRPM,
		TotalCost:          k.TotalCost,
		TotalRevenue:       k.TotalRevenue,
		TodayCost:          k.TodayCost,
		TodayRevenue:       k.TodayRevenue,
		TimeMixin: dto.TimeMixin{
			CreatedAt: k.CreatedAt,
			UpdatedAt: k.UpdatedAt,
		},
	}
}

// toKeyInput 请求 DTO → 领域 KeyInput（新增/更新 key 共用）。
func toKeyInput(req dto.ChannelKeyReq) appchannel.KeyInput {
	name := ""
	if req.Name != nil {
		name = *req.Name
	}
	return appchannel.KeyInput{
		Name:           name,
		Type:           req.Type,
		APIKey:         req.APIKey,
		Models:         req.Models,
		ModelMapping:   req.ModelMapping,
		ParamOverride:  req.ParamOverride,
		HeaderOverride: req.HeaderOverride,
		Status:         req.Status,
		Priority:       req.Priority,
		Weight:         req.Weight,
		MaxConcurrency: req.MaxConcurrency,
		MaxRPM:         req.MaxRPM,
		CostRatio:      req.CostRatio,
		Tags:           req.Tags,
		TestModel:      req.TestModel,
		GroupIDs:       req.GroupIDs,
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
