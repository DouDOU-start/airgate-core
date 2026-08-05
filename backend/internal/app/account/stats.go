package account

import (
	"context"
	"fmt"
	"time"
)

// AccountDayHistory 账号按日用量。
type AccountDayHistory struct {
	Date       string  `json:"date"`
	Label      string  `json:"label"`
	Requests   int64   `json:"requests"`
	Tokens     int64   `json:"tokens"`
	Cost       float64 `json:"cost"`        // total_cost 标准成本
	ActualCost float64 `json:"actual_cost"` // total × account_rate_multiplier 估算的账号成本
	UserCost   float64 `json:"user_cost"`   // actual_cost 用户扣费
}

// AccountModelStat 账号按模型统计。
type AccountModelStat struct {
	Model      string  `json:"model"`
	Requests   int64   `json:"requests"`
	Tokens     int64   `json:"tokens"`
	TotalCost  float64 `json:"total_cost"`
	ActualCost float64 `json:"actual_cost"`
}

// AccountDayHighlight 峰值日摘要。
type AccountDayHighlight struct {
	Date     string  `json:"date"`
	Label    string  `json:"label"`
	Cost     float64 `json:"cost"`
	UserCost float64 `json:"user_cost"`
	Requests int64   `json:"requests"`
	Tokens   int64   `json:"tokens,omitempty"`
}

// AccountUsageSummary 账号统计汇总。
type AccountUsageSummary struct {
	Days              int                  `json:"days"`
	ActualDaysUsed    int                  `json:"actual_days_used"`
	TotalCost         float64              `json:"total_cost"`          // 账号侧成本（actual_cost 累加口径对齐 sub2api）
	TotalUserCost     float64              `json:"total_user_cost"`     // 用户扣费
	TotalStandardCost float64              `json:"total_standard_cost"` // 标准价 total_cost
	TotalRequests     int64                `json:"total_requests"`
	TotalTokens       int64                `json:"total_tokens"`
	AvgDailyCost      float64              `json:"avg_daily_cost"`
	AvgDailyUserCost  float64              `json:"avg_daily_user_cost"`
	AvgDailyRequests  float64              `json:"avg_daily_requests"`
	AvgDailyTokens    float64              `json:"avg_daily_tokens"`
	AvgDurationMs     float64              `json:"avg_duration_ms"`
	Today             *AccountDayHighlight `json:"today,omitempty"`
	HighestCostDay    *AccountDayHighlight `json:"highest_cost_day,omitempty"`
	HighestRequestDay *AccountDayHighlight `json:"highest_request_day,omitempty"`
}

// AccountUsageStats 账号使用统计（对齐 sub2api AccountUsageStatsResponse）。
type AccountUsageStats struct {
	History []AccountDayHistory `json:"history"`
	Summary AccountUsageSummary `json:"summary"`
	Models  []AccountModelStat  `json:"models"`
}

// MoneyStats 账号金额统计（列表用，口径对齐渠道 key）：
// Cost/Revenue 累计，TodayCost/TodayRevenue 为 todayStart 起的今日口径。
// 成本 = Σ(total_cost × account_rate_multiplier)，收益 = Σ(actual_cost)。
type MoneyStats struct {
	Cost         float64
	Revenue      float64
	TodayCost    float64
	TodayRevenue float64
}

// UsageStatsRepository 账号用量聚合仓储（由 UsageStore 实现）。
type UsageStatsRepository interface {
	GetAccountUsageStats(ctx context.Context, accountID int, start, end time.Time) (AccountUsageStats, error)
	// GetAccountMoneyStats 批量按 account_id 聚合金额；todayStart 为调用方时区当日零点。
	GetAccountMoneyStats(ctx context.Context, accountIDs []int, todayStart time.Time) (map[int]MoneyStats, error)
}

// SetUsageStatsRepo 注入用量统计仓储。
func (s *Service) SetUsageStatsRepo(repo UsageStatsRepository) {
	if s == nil {
		return
	}
	s.statsRepo = repo
}

// GetUsageStats 查询账号近 N 天使用统计（默认 30，最大 90）。
func (s *Service) GetUsageStats(ctx context.Context, id int, days int) (AccountUsageStats, error) {
	if days <= 0 {
		days = 30
	}
	if days > 90 {
		days = 90
	}
	if _, err := s.FindByID(ctx, id, LoadOptions{}); err != nil {
		return AccountUsageStats{}, err
	}
	if s.statsRepo == nil {
		return AccountUsageStats{}, fmt.Errorf("用量统计仓储未配置")
	}

	now := time.Now()
	// 统计窗口：[今天 00:00 - (days-1) 天, 明天 00:00)
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, 1)
	start := end.AddDate(0, 0, -days)

	stats, err := s.statsRepo.GetAccountUsageStats(ctx, id, start, end)
	if err != nil {
		return AccountUsageStats{}, err
	}
	stats.Summary.Days = days
	return stats, nil
}
