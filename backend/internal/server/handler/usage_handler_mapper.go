package handler

import (
	appusage "github.com/DouDOU-start/airgate-core/internal/app/usage"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
)

// toUsageLogResp 转换为 reseller / admin 视角的完整响应（包含 actual_cost、billed_cost 等所有字段）。
func toUsageLogResp(record appusage.LogRecord) dto.UsageLogResp {
	return dto.UsageLogResp{
		ID:                    record.ID,
		UserID:                record.UserID,
		UserEmail:             record.UserEmail,
		UserDeleted:           record.UserDeleted,
		APIKeyID:              record.APIKeyID,
		APIKeyName:            record.APIKeyName,
		APIKeyHint:            record.APIKeyHint,
		APIKeyDeleted:         record.APIKeyDeleted,
		ChannelID:             record.ChannelID,
		ChannelName:           record.ChannelName,
		GroupID:               record.GroupID,
		Model:                 record.Model,
		InputTokens:           record.InputTokens,
		OutputTokens:          record.OutputTokens,
		CachedInputTokens:     record.CachedInputTokens,
		CacheCreationTokens:   record.CacheCreationTokens,
		CacheCreation5mTokens: record.CacheCreation5mTokens,
		CacheCreation1hTokens: record.CacheCreation1hTokens,
		Calls:                 record.Calls,
		InputPrice:            record.InputPrice,
		OutputPrice:           record.OutputPrice,
		CachedInputPrice:      record.CachedInputPrice,
		CacheCreationPrice:    record.CacheCreationPrice,
		CacheCreation1hPrice:  record.CacheCreation1hPrice,
		InputCost:             record.InputCost,
		OutputCost:            record.OutputCost,
		CachedInputCost:       record.CachedInputCost,
		CacheCreationCost:     record.CacheCreationCost,
		TotalCost:             record.TotalCost,
		ActualCost:            record.ActualCost,
		BilledCost:            record.BilledCost,
		RateMultiplier:        record.RateMultiplier,
		SellRate:              record.SellRate,
		AccountRateMultiplier: record.AccountRateMultiplier,
		ServiceTier:           record.ServiceTier,
		ReasoningEffort:       record.ReasoningEffort,
		Stream:                record.Stream,
		DurationMs:            record.DurationMs,
		FirstTokenMs:          record.FirstTokenMs,
		UserAgent:             record.UserAgent,
		IPAddress:             record.IPAddress,
		Endpoint:              record.Endpoint,
		Source:                record.Source,
		RequestID:             record.RequestID,
		CreatedAt:             record.CreatedAt,
	}
}

// toUserUsageLogResp 转换为普通用户视角的响应：
// 在完整映射基础上剥离渠道成本倍率快照与渠道字段——
// 用户只能看到分组，渠道拓扑（ID/名称）仅管理端可见。
func toUserUsageLogResp(record appusage.LogRecord) dto.UsageLogResp {
	resp := toUsageLogResp(record)
	resp.AccountRateMultiplier = 0
	resp.ChannelID = 0
	resp.ChannelName = ""
	return resp
}

// toCustomerUsageLogResp 转换为 end customer 视角的精简响应（仅 billed_cost，剥离所有平台真实成本字段）。
//
// 当请求来自 API Key 登录拿到的 scoped JWT 时使用，避免泄漏 reseller 与平台之间的差价。
func toCustomerUsageLogResp(record appusage.LogRecord) dto.CustomerUsageLogResp {
	return dto.CustomerUsageLogResp{
		ID:                    record.ID,
		APIKeyID:              record.APIKeyID,
		Model:                 record.Model,
		InputTokens:           record.InputTokens,
		OutputTokens:          record.OutputTokens,
		CachedInputTokens:     record.CachedInputTokens,
		CacheCreationTokens:   record.CacheCreationTokens,
		CacheCreation5mTokens: record.CacheCreation5mTokens,
		CacheCreation1hTokens: record.CacheCreation1hTokens,
		Calls:                 record.Calls,
		BilledCost:            record.BilledCost,
		ServiceTier:           record.ServiceTier,
		ReasoningEffort:       record.ReasoningEffort,
		Stream:                record.Stream,
		DurationMs:            record.DurationMs,
		FirstTokenMs:          record.FirstTokenMs,
		Endpoint:              record.Endpoint,
		RequestID:             record.RequestID,
		CreatedAt:             record.CreatedAt,
	}
}

func toUsageStatsResp(result appusage.StatsResult) dto.UsageStatsResp {
	resp := dto.UsageStatsResp{
		TotalRequests:   result.TotalRequests,
		TotalTokens:     result.TotalTokens,
		TotalCost:       result.TotalCost,
		TotalActualCost: result.TotalActualCost,
		TotalBilledCost: result.TotalBilledCost,
	}
	for _, item := range result.ByModel {
		resp.ByModel = append(resp.ByModel, dto.ModelStats{
			Model:      item.Model,
			Requests:   item.Requests,
			Tokens:     item.Tokens,
			TotalCost:  item.TotalCost,
			ActualCost: item.ActualCost,
			BilledCost: item.BilledCost,
		})
	}
	for _, item := range result.ByUser {
		resp.ByUser = append(resp.ByUser, dto.UserStats{
			UserID:     item.UserID,
			Email:      item.Email,
			Requests:   item.Requests,
			Tokens:     item.Tokens,
			TotalCost:  item.TotalCost,
			ActualCost: item.ActualCost,
			BilledCost: item.BilledCost,
		})
	}
	for _, item := range result.ByChannel {
		resp.ByChannel = append(resp.ByChannel, dto.ChannelStats{
			ChannelID:  item.ChannelID,
			Name:       item.Name,
			Requests:   item.Requests,
			Tokens:     item.Tokens,
			TotalCost:  item.TotalCost,
			ActualCost: item.ActualCost,
			BilledCost: item.BilledCost,
		})
	}
	for _, item := range result.ByChannelKey {
		resp.ByChannelKey = append(resp.ByChannelKey, dto.ChannelKeyStats{
			ChannelKeyID: item.ChannelKeyID,
			Name:         item.Name,
			ChannelName:  item.ChannelName,
			Requests:     item.Requests,
			Tokens:       item.Tokens,
			TotalCost:    item.TotalCost,
			ActualCost:   item.ActualCost,
			BilledCost:   item.BilledCost,
		})
	}
	for _, item := range result.ByGroup {
		resp.ByGroup = append(resp.ByGroup, dto.GroupStats{
			GroupID:    item.GroupID,
			Name:       item.Name,
			Requests:   item.Requests,
			Tokens:     item.Tokens,
			TotalCost:  item.TotalCost,
			ActualCost: item.ActualCost,
			BilledCost: item.BilledCost,
		})
	}
	return resp
}

func toUsageTrendBuckets(items []appusage.TrendBucket) []dto.UsageTrendBucket {
	result := make([]dto.UsageTrendBucket, 0, len(items))
	for _, item := range items {
		result = append(result, dto.UsageTrendBucket{
			Time:          item.Time,
			InputTokens:   item.InputTokens,
			OutputTokens:  item.OutputTokens,
			CacheCreation: item.CacheCreation,
			CacheRead:     item.CacheRead,
			ActualCost:    item.ActualCost,
			StandardCost:  item.StandardCost,
		})
	}
	return result
}
