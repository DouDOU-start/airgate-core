package store

import (
	"context"
	"time"

	"github.com/DouDOU-start/airgate-core/ent"
	entaccount "github.com/DouDOU-start/airgate-core/ent/account"
	entapikey "github.com/DouDOU-start/airgate-core/ent/apikey"
	entusagelog "github.com/DouDOU-start/airgate-core/ent/usagelog"
	entuser "github.com/DouDOU-start/airgate-core/ent/user"
	appdashboard "github.com/DouDOU-start/airgate-core/internal/app/dashboard"
)

// DashboardStore 使用 Ent 实现仪表盘仓储。
type DashboardStore struct {
	db *ent.Client
}

// NewDashboardStore 创建仪表盘仓储。
func NewDashboardStore(db *ent.Client) *DashboardStore {
	return &DashboardStore{db: db}
}

// LoadStatsSnapshot 读取统计快照。
func (s *DashboardStore) LoadStatsSnapshot(ctx context.Context, todayStart, fiveMinAgo time.Time) (appdashboard.StatsSnapshot, error) {
	totalAPIKeys, err := s.db.APIKey.Query().Count(ctx)
	if err != nil {
		return appdashboard.StatsSnapshot{}, err
	}
	enabledAPIKeys, err := s.db.APIKey.Query().Where(entapikey.StatusEQ(entapikey.StatusActive)).Count(ctx)
	if err != nil {
		return appdashboard.StatsSnapshot{}, err
	}

	totalAccounts, err := s.db.Account.Query().Count(ctx)
	if err != nil {
		return appdashboard.StatsSnapshot{}, err
	}
	enabledAccounts, err := s.db.Account.Query().Where(entaccount.StatusEQ(entaccount.StatusActive)).Count(ctx)
	if err != nil {
		return appdashboard.StatsSnapshot{}, err
	}
	errorAccounts, err := s.db.Account.Query().Where(entaccount.StatusEQ(entaccount.StatusError)).Count(ctx)
	if err != nil {
		return appdashboard.StatsSnapshot{}, err
	}

	totalUsers, err := s.db.User.Query().Count(ctx)
	if err != nil {
		return appdashboard.StatsSnapshot{}, err
	}
	newUsersToday, err := s.db.User.Query().Where(entuser.CreatedAtGTE(todayStart)).Count(ctx)
	if err != nil {
		return appdashboard.StatsSnapshot{}, err
	}

	allTimeRequests, err := s.db.UsageLog.Query().Count(ctx)
	if err != nil {
		return appdashboard.StatsSnapshot{}, err
	}

	todayLogs, err := s.db.UsageLog.Query().
		Where(entusagelog.CreatedAtGTE(todayStart)).
		WithUser().
		All(ctx)
	if err != nil {
		return appdashboard.StatsSnapshot{}, err
	}

	var todayRequests int64
	var todayTokens int64
	var todayCost float64
	var todayDurationMs int64
	activeUserSet := make(map[int]bool)
	for _, item := range todayLogs {
		todayRequests++
		todayTokens += int64(item.InputTokens + item.OutputTokens + item.CachedInputTokens)
		todayCost += item.ActualCost
		todayDurationMs += item.DurationMs
		if edgeUser := item.Edges.User; edgeUser != nil {
			activeUserSet[edgeUser.ID] = true
		}
	}

	allTimeTokens, allTimeCost, err := queryUsageTotals(ctx, s.db.UsageLog.Query())
	if err != nil {
		return appdashboard.StatsSnapshot{}, err
	}
	recentTokens, _, err := queryUsageTotals(ctx, s.db.UsageLog.Query().Where(entusagelog.CreatedAtGTE(fiveMinAgo)))
	if err != nil {
		return appdashboard.StatsSnapshot{}, err
	}
	recentRequests, err := s.db.UsageLog.Query().
		Where(entusagelog.CreatedAtGTE(fiveMinAgo)).
		Count(ctx)
	if err != nil {
		return appdashboard.StatsSnapshot{}, err
	}

	return appdashboard.StatsSnapshot{
		TotalAPIKeys:    int64(totalAPIKeys),
		EnabledAPIKeys:  int64(enabledAPIKeys),
		TotalAccounts:   int64(totalAccounts),
		EnabledAccounts: int64(enabledAccounts),
		ErrorAccounts:   int64(errorAccounts),
		TotalUsers:      int64(totalUsers),
		NewUsersToday:   int64(newUsersToday),
		TodayRequests:   todayRequests,
		AllTimeRequests: int64(allTimeRequests),
		TodayTokens:     todayTokens,
		TodayCost:       todayCost,
		TodayDurationMs: todayDurationMs,
		ActiveUsers:     int64(len(activeUserSet)),
		AllTimeTokens:   allTimeTokens,
		AllTimeCost:     allTimeCost,
		RecentRequests:  int64(recentRequests),
		RecentTokens:    recentTokens,
	}, nil
}

// ListTrendLogs 读取趋势聚合所需日志。
func (s *DashboardStore) ListTrendLogs(ctx context.Context, startTime, endTime time.Time) ([]appdashboard.TrendLog, error) {
	list, err := s.db.UsageLog.Query().
		Where(
			entusagelog.CreatedAtGTE(startTime),
			entusagelog.CreatedAtLT(endTime),
		).
		WithUser().
		All(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]appdashboard.TrendLog, 0, len(list))
	for _, item := range list {
		log := appdashboard.TrendLog{
			Model:             item.Model,
			InputTokens:       int64(item.InputTokens),
			OutputTokens:      int64(item.OutputTokens),
			CachedInputTokens: int64(item.CachedInputTokens),
			ActualCost:        item.ActualCost,
			StandardCost:      item.TotalCost,
			CreatedAt:         item.CreatedAt,
		}
		if edgeUser := item.Edges.User; edgeUser != nil {
			log.UserID = edgeUser.ID
			log.UserEmail = edgeUser.Email
		}
		result = append(result, log)
	}

	return result, nil
}

func queryUsageTotals(ctx context.Context, query *ent.UsageLogQuery) (int64, float64, error) {
	var rows []struct {
		InputSum  int64   `json:"input_sum"`
		OutputSum int64   `json:"output_sum"`
		CacheSum  int64   `json:"cache_sum"`
		CostSum   float64 `json:"cost_sum"`
	}
	if err := query.Aggregate(
		ent.As(ent.Sum(entusagelog.FieldInputTokens), "input_sum"),
		ent.As(ent.Sum(entusagelog.FieldOutputTokens), "output_sum"),
		ent.As(ent.Sum(entusagelog.FieldCachedInputTokens), "cache_sum"),
		ent.As(ent.Sum(entusagelog.FieldActualCost), "cost_sum"),
	).Scan(ctx, &rows); err != nil {
		return 0, 0, err
	}
	if len(rows) == 0 {
		return 0, 0, nil
	}
	return rows[0].InputSum + rows[0].OutputSum + rows[0].CacheSum, rows[0].CostSum, nil
}
