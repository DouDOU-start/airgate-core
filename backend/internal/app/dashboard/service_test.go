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
				TodayRequests:           6,
				TodayImageRequests:      2,
				TodayNonImageRequests:   4,
				TodayNonImageDurationMs: 1000,
				TodayFirstTokenRequests: 2,
				TodayFirstTokenMs:       300,
				TodayImageDurationMs:    240000,
				RecentRequests:          10,
				RecentTokens:            500,
			}, nil
		},
	})

	result, err := service.Stats(t.Context(), 0, "")
	if err != nil {
		t.Fatalf("Stats() returned error: %v", err)
	}
	if result.AvgDurationMs != 250 {
		t.Fatalf("AvgDurationMs = %v, want 250", result.AvgDurationMs)
	}
	if result.AvgFirstTokenMs != 150 {
		t.Fatalf("AvgFirstTokenMs = %v, want 150", result.AvgFirstTokenMs)
	}
	if result.AvgImageDurationMs != 120000 {
		t.Fatalf("AvgImageDurationMs = %v, want 120000", result.AvgImageDurationMs)
	}
	if result.TodayImageRequests != 2 {
		t.Fatalf("TodayImageRequests = %v, want 2", result.TodayImageRequests)
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

func TestTrendCacheKeyBucketsMovingEndTime(t *testing.T) {
	query := TrendQuery{Range: "today", Granularity: "hour", UserID: 7}
	start := time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC)
	key1 := trendCacheKey(query, time.UTC, start, time.Date(2026, 5, 27, 12, 0, 1, 0, time.UTC))
	key2 := trendCacheKey(query, time.UTC, start, time.Date(2026, 5, 27, 12, 0, 14, 0, time.UTC))
	key3 := trendCacheKey(query, time.UTC, start, time.Date(2026, 5, 27, 12, 0, 16, 0, time.UTC))

	if key1 != key2 {
		t.Fatalf("same cache bucket keys differ: %q vs %q", key1, key2)
	}
	if key1 == key3 {
		t.Fatalf("different cache bucket keys unexpectedly match: %q", key1)
	}

	otherUser := query
	otherUser.UserID = 8
	if key1 == trendCacheKey(otherUser, time.UTC, start, time.Date(2026, 5, 27, 12, 0, 1, 0, time.UTC)) {
		t.Fatal("cache key must include user scope")
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
					CreatedAt:         time.Date(2026, 4, 2, 10, 15, 0, 0, time.UTC),
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
					CreatedAt:         time.Date(2026, 4, 2, 10, 45, 0, 0, time.UTC),
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
					CreatedAt:         time.Date(2026, 4, 2, 11, 0, 0, 0, time.UTC),
				},
			}, nil
		},
	})
	service.now = func() time.Time { return now }

	result, err := service.Trend(t.Context(), TrendQuery{Range: "today", Granularity: "hour", TZ: "UTC"})
	if err != nil {
		t.Fatalf("Trend() returned error: %v", err)
	}
	if len(result.ModelDistribution) != 2 {
		t.Fatalf("len(ModelDistribution) = %d, want 2", len(result.ModelDistribution))
	}
	if result.ModelDistribution[0].Model != "gpt-4.1" || result.ModelDistribution[0].Requests != 2 {
		t.Fatalf("unexpected first model stat: %+v", result.ModelDistribution[0])
	}
	// 零填充：today 12:00 时应有 00:00~11:00 共 12 个小时桶，其中 10:00/11:00 有数据。
	if len(result.TokenTrend) != 12 {
		t.Fatalf("len(TokenTrend) = %d, want 12", len(result.TokenTrend))
	}
	if result.TokenTrend[0].Time != "2026-04-02 00:00" || result.TokenTrend[0].InputTokens != 0 {
		t.Fatalf("unexpected zero-filled first bucket: %+v", result.TokenTrend[0])
	}
	if result.TokenTrend[10].Time != "2026-04-02 10:00" || result.TokenTrend[10].InputTokens != 12 {
		t.Fatalf("unexpected data bucket: %+v", result.TokenTrend[10])
	}
	if len(result.TopUsers) == 0 || result.TopUsers[0].UserID != 1 {
		t.Fatalf("unexpected top users: %+v", result.TopUsers)
	}
	if len(result.TopUsers[0].Trend) != 12 {
		t.Fatalf("len(TopUsers[0].Trend) = %d, want 12（零填充）", len(result.TopUsers[0].Trend))
	}
}

func TestTrendCoercesHourToDayForMultiDayRange(t *testing.T) {
	now := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
	service := NewService(dashboardStubRepository{
		listTrendLogs: func(_ context.Context, _, _ time.Time) ([]TrendLog, error) {
			return []TrendLog{{
				UserID:       1,
				UserEmail:    "a@test.com",
				Model:        "gpt-4.1",
				InputTokens:  10,
				OutputTokens: 20,
				CreatedAt:    time.Date(2026, 4, 5, 10, 15, 0, 0, time.UTC),
			}}, nil
		},
	})
	service.now = func() time.Time { return now }

	result, err := service.Trend(t.Context(), TrendQuery{Range: "7d", Granularity: "hour", TZ: "UTC"})
	if err != nil {
		t.Fatalf("Trend() returned error: %v", err)
	}
	// 多天范围的小时粒度应收敛为按天：4/1~4/8 共 8 个天桶，key 为日期格式。
	if len(result.TokenTrend) != 8 {
		t.Fatalf("len(TokenTrend) = %d, want 8", len(result.TokenTrend))
	}
	if result.TokenTrend[0].Time != "2026-04-01" {
		t.Fatalf("bucket key 应为天粒度日期: %+v", result.TokenTrend[0])
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
