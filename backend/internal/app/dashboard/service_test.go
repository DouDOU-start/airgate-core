package dashboard

import (
	"context"
	"testing"
	"time"
)

func TestStatsComputesDerivedMetrics(t *testing.T) {
	service := NewService(dashboardStubRepository{
		loadStatsSnapshot: func(_ context.Context, _, _ time.Time) (StatsSnapshot, error) {
			return StatsSnapshot{
				TodayRequests:   4,
				TodayDurationMs: 1000,
				RecentRequests:  10,
				RecentTokens:    500,
			}, nil
		},
	})

	result, err := service.Stats(t.Context(), 0)
	if err != nil {
		t.Fatalf("Stats() returned error: %v", err)
	}
	if result.AvgDurationMs != 250 {
		t.Fatalf("AvgDurationMs = %v, want 250", result.AvgDurationMs)
	}
	if result.RPM != 2 {
		t.Fatalf("RPM = %v, want 2", result.RPM)
	}
	if result.TPM != 100 {
		t.Fatalf("TPM = %v, want 100", result.TPM)
	}
}

func TestResolveTrendTimeRangeCustomIncludesEndDate(t *testing.T) {
	now := time.Date(2026, 4, 2, 12, 0, 0, 0, time.UTC)

	start, end := resolveTrendTimeRange(TrendQuery{
		Range:     "custom",
		StartDate: "2026-03-01",
		EndDate:   "2026-03-15",
	}, now)

	if !start.Equal(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("start = %v, want 2026-03-01", start)
	}
	if !end.Equal(time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("end = %v, want 2026-03-16", end)
	}
}

func TestTrendAggregatesTopUsersAndBuckets(t *testing.T) {
	now := time.Date(2026, 4, 2, 12, 0, 0, 0, time.UTC)
	service := NewService(dashboardStubRepository{
		listTrendLogs: func(_ context.Context, _, _ time.Time) ([]TrendLog, error) {
			return []TrendLog{
				{
					UserID:            1,
					UserEmail:         "a@test.com",
					Model:             "gpt-4.1",
					InputTokens:       10,
					OutputTokens:      20,
					CachedInputTokens: 5,
					ActualCost:        1.2,
					StandardCost:      1.5,
					CreatedAt:         time.Date(2026, 4, 1, 10, 15, 0, 0, time.UTC),
				},
				{
					UserID:            1,
					UserEmail:         "a@test.com",
					Model:             "gpt-4.1",
					InputTokens:       2,
					OutputTokens:      3,
					CachedInputTokens: 0,
					ActualCost:        0.2,
					StandardCost:      0.3,
					CreatedAt:         time.Date(2026, 4, 1, 10, 45, 0, 0, time.UTC),
				},
				{
					UserID:            2,
					UserEmail:         "b@test.com",
					Model:             "gpt-4o",
					InputTokens:       5,
					OutputTokens:      5,
					CachedInputTokens: 0,
					ActualCost:        0.5,
					StandardCost:      0.8,
					CreatedAt:         time.Date(2026, 4, 1, 11, 0, 0, 0, time.UTC),
				},
			}, nil
		},
	})
	service.now = func() time.Time { return now }

	result, err := service.Trend(t.Context(), TrendQuery{Range: "today", Granularity: "hour"})
	if err != nil {
		t.Fatalf("Trend() returned error: %v", err)
	}
	if len(result.ModelDistribution) != 2 {
		t.Fatalf("len(ModelDistribution) = %d, want 2", len(result.ModelDistribution))
	}
	if result.ModelDistribution[0].Model != "gpt-4.1" || result.ModelDistribution[0].Requests != 2 {
		t.Fatalf("unexpected first model stat: %+v", result.ModelDistribution[0])
	}
	if len(result.TokenTrend) != 2 {
		t.Fatalf("len(TokenTrend) = %d, want 2", len(result.TokenTrend))
	}
	if len(result.TopUsers) == 0 || result.TopUsers[0].UserID != 1 {
		t.Fatalf("unexpected top users: %+v", result.TopUsers)
	}
}

type dashboardStubRepository struct {
	loadStatsSnapshot func(context.Context, time.Time, time.Time) (StatsSnapshot, error)
	listTrendLogs     func(context.Context, time.Time, time.Time) ([]TrendLog, error)
}

func (s dashboardStubRepository) LoadStatsSnapshot(ctx context.Context, todayStart, fiveMinAgo time.Time, _ int) (StatsSnapshot, error) {
	if s.loadStatsSnapshot == nil {
		return StatsSnapshot{}, nil
	}
	return s.loadStatsSnapshot(ctx, todayStart, fiveMinAgo)
}

func (s dashboardStubRepository) ListTrendLogs(ctx context.Context, startTime, endTime time.Time, _ int) ([]TrendLog, error) {
	if s.listTrendLogs == nil {
		return nil, nil
	}
	return s.listTrendLogs(ctx, startTime, endTime)
}
