package handler

import (
	appdashboard "github.com/DouDOU-start/airgate-core/internal/app/dashboard"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
)

func toDashboardStatsResp(item appdashboard.Stats) dto.DashboardStatsResp {
	return dto.DashboardStatsResp{
		TotalAPIKeys:        item.TotalAPIKeys,
		EnabledAPIKeys:      item.EnabledAPIKeys,
		TotalAccounts:       item.TotalAccounts,
		EnabledAccounts:     item.EnabledAccounts,
		ErrorAccounts:       item.ErrorAccounts,
		TodayRequests:       item.TodayRequests,
		AllTimeRequests:     item.AllTimeRequests,
		TotalUsers:          item.TotalUsers,
		NewUsersToday:       item.NewUsersToday,
		TodayTokens:         item.TodayTokens,
		TodayCost:           item.TodayCost,
		TodayStandardCost:   item.TodayStandardCost,
		AllTimeTokens:       item.AllTimeTokens,
		AllTimeCost:         item.AllTimeCost,
		AllTimeStandardCost: item.AllTimeStandardCost,
		RPM:                 item.RPM,
		TPM:                 item.TPM,
		AvgDurationMs:       item.AvgDurationMs,
		ActiveUsers:         item.ActiveUsers,
	}
}

func toDashboardTrendResp(item appdashboard.Trend) dto.DashboardTrendResp {
	return dto.DashboardTrendResp{
		ModelDistribution: toDashboardModelStats(item.ModelDistribution),
		UserRanking:       toDashboardUserRankings(item.UserRanking),
		TokenTrend:        toDashboardTimeBuckets(item.TokenTrend),
		TopUsers:          toDashboardUserTrends(item.TopUsers),
	}
}

func toDashboardModelStats(items []appdashboard.ModelStats) []dto.DashboardModelStats {
	result := make([]dto.DashboardModelStats, 0, len(items))
	for _, item := range items {
		result = append(result, dto.DashboardModelStats{
			Model:        item.Model,
			Requests:     item.Requests,
			Tokens:       item.Tokens,
			ActualCost:   item.ActualCost,
			StandardCost: item.StandardCost,
		})
	}
	return result
}

func toDashboardUserRankings(items []appdashboard.UserRanking) []dto.DashboardUserRanking {
	result := make([]dto.DashboardUserRanking, 0, len(items))
	for _, item := range items {
		result = append(result, dto.DashboardUserRanking{
			UserID:       item.UserID,
			Email:        item.Email,
			Requests:     item.Requests,
			Tokens:       item.Tokens,
			ActualCost:   item.ActualCost,
			StandardCost: item.StandardCost,
		})
	}
	return result
}

func toDashboardTimeBuckets(items []appdashboard.TimeBucket) []dto.DashboardTimeBucket {
	result := make([]dto.DashboardTimeBucket, 0, len(items))
	for _, item := range items {
		result = append(result, dto.DashboardTimeBucket{
			Time:         item.Time,
			InputTokens:  item.InputTokens,
			OutputTokens: item.OutputTokens,
			CachedInput:  item.CachedInput,
		})
	}
	return result
}

func toDashboardUserTrends(items []appdashboard.UserTrend) []dto.DashboardUserTrend {
	result := make([]dto.DashboardUserTrend, 0, len(items))
	for _, item := range items {
		trend := make([]dto.DashboardUserTrendPoint, 0, len(item.Trend))
		for _, point := range item.Trend {
			trend = append(trend, dto.DashboardUserTrendPoint{
				Time:   point.Time,
				Tokens: point.Tokens,
			})
		}
		result = append(result, dto.DashboardUserTrend{
			UserID: item.UserID,
			Email:  item.Email,
			Trend:  trend,
		})
	}
	return result
}
