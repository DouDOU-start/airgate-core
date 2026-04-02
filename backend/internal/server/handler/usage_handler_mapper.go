package handler

import (
	appusage "github.com/DouDOU-start/airgate-core/internal/app/usage"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
)

func toUsageLogResp(record appusage.LogRecord) dto.UsageLogResp {
	return dto.UsageLogResp{
		ID:                    record.ID,
		UserID:                record.UserID,
		UserEmail:             record.UserEmail,
		APIKeyID:              record.APIKeyID,
		APIKeyName:            record.APIKeyName,
		APIKeyHint:            record.APIKeyHint,
		APIKeyDeleted:         record.APIKeyDeleted,
		AccountID:             record.AccountID,
		AccountName:           record.AccountName,
		GroupID:               record.GroupID,
		Platform:              record.Platform,
		Model:                 record.Model,
		InputTokens:           record.InputTokens,
		OutputTokens:          record.OutputTokens,
		CachedInputTokens:     record.CachedInputTokens,
		ReasoningOutputTokens: record.ReasoningOutputTokens,
		InputPrice:            record.InputPrice,
		OutputPrice:           record.OutputPrice,
		CachedInputPrice:      record.CachedInputPrice,
		InputCost:             record.InputCost,
		OutputCost:            record.OutputCost,
		CachedInputCost:       record.CachedInputCost,
		TotalCost:             record.TotalCost,
		ActualCost:            record.ActualCost,
		RateMultiplier:        record.RateMultiplier,
		AccountRateMultiplier: record.AccountRateMultiplier,
		ServiceTier:           record.ServiceTier,
		Stream:                record.Stream,
		DurationMs:            record.DurationMs,
		FirstTokenMs:          record.FirstTokenMs,
		UserAgent:             record.UserAgent,
		IPAddress:             record.IPAddress,
		CreatedAt:             record.CreatedAt,
	}
}

func toUsageStatsResp(result appusage.StatsResult) dto.UsageStatsResp {
	resp := dto.UsageStatsResp{
		TotalRequests:   result.TotalRequests,
		TotalTokens:     result.TotalTokens,
		TotalCost:       result.TotalCost,
		TotalActualCost: result.TotalActualCost,
	}
	for _, item := range result.ByModel {
		resp.ByModel = append(resp.ByModel, dto.ModelStats{
			Model:      item.Model,
			Requests:   item.Requests,
			Tokens:     item.Tokens,
			TotalCost:  item.TotalCost,
			ActualCost: item.ActualCost,
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
		})
	}
	for _, item := range result.ByAccount {
		resp.ByAccount = append(resp.ByAccount, dto.AccountStats{
			AccountID:  item.AccountID,
			Name:       item.Name,
			Requests:   item.Requests,
			Tokens:     item.Tokens,
			TotalCost:  item.TotalCost,
			ActualCost: item.ActualCost,
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
