package usage

import (
	"context"
	"testing"
)

func TestUserStatsWithModelsCombinesSummaryAndModelStats(t *testing.T) {
	repo := &stubUsageRepository{
		summaryUserFn: func(_ context.Context, userID int64, filter StatsFilter) (Summary, error) {
			if userID != 42 {
				t.Fatalf("SummaryUser userID = %d, want 42", userID)
			}
			if filter.Platform != "openai" || filter.Model != "gpt-5.5" {
				t.Fatalf("SummaryUser filter = %+v, want openai/gpt-5.5", filter)
			}
			return Summary{TotalRequests: 7, TotalTokens: 99, TotalBilledCost: 3.14}, nil
		},
		statsByModelFn: func(_ context.Context, filter StatsFilter) ([]ModelStats, error) {
			if filter.UserID == nil || *filter.UserID != 42 {
				t.Fatalf("StatsByModel userID = %v, want 42", filter.UserID)
			}
			return []ModelStats{{Model: "gpt-5.5", Requests: 2, Tokens: 11, BilledCost: 1.23}}, nil
		},
	}

	svc := NewService(repo)
	result, err := svc.UserStatsWithModels(context.Background(), 42, StatsFilter{
		Platform: "openai",
		Model:    "gpt-5.5",
	})
	if err != nil {
		t.Fatalf("UserStatsWithModels returned error: %v", err)
	}
	if got, want := result.Summary.TotalRequests, int64(7); got != want {
		t.Fatalf("Summary.TotalRequests = %d, want %d", got, want)
	}
	if got, want := len(result.ByModel), 1; got != want {
		t.Fatalf("len(ByModel) = %d, want %d", got, want)
	}
	if got, want := result.ByModel[0].Model, "gpt-5.5"; got != want {
		t.Fatalf("ByModel[0].Model = %q, want %q", got, want)
	}
}

func TestNormalizeStatsGroupByDropsUnknownAndDuplicates(t *testing.T) {
	got := normalizeStatsGroupBy("user, model,foo,group,user,account,model")
	want := "account,group,model,user"
	if got != want {
		t.Fatalf("normalizeStatsGroupBy() = %q, want %q", got, want)
	}
}

func TestNormalizeTrendFilterDefaultsRecentHours(t *testing.T) {
	got := normalizeTrendFilter(TrendFilter{})
	if got.DefaultRecentHours != 24 {
		t.Fatalf("DefaultRecentHours = %d, want 24", got.DefaultRecentHours)
	}
}

type stubUsageRepository struct {
	summaryUserFn    func(context.Context, int64, StatsFilter) (Summary, error)
	summaryAdminFn   func(context.Context, StatsFilter) (Summary, error)
	statsByModelFn   func(context.Context, StatsFilter) ([]ModelStats, error)
	statsByUserFn    func(context.Context, StatsFilter) ([]UserStats, error)
	statsByAccountFn func(context.Context, StatsFilter) ([]AccountStats, error)
	statsByGroupFn   func(context.Context, StatsFilter) ([]GroupStats, error)
	trendEntriesFn   func(context.Context, TrendFilter) ([]TrendEntry, error)
}

func (s *stubUsageRepository) ListUser(context.Context, int64, ListFilter) ([]LogRecord, int64, error) {
	return nil, 0, nil
}

func (s *stubUsageRepository) ListAdmin(context.Context, ListFilter) ([]LogRecord, int64, error) {
	return nil, 0, nil
}

func (s *stubUsageRepository) SummaryUser(ctx context.Context, userID int64, filter StatsFilter) (Summary, error) {
	if s.summaryUserFn != nil {
		return s.summaryUserFn(ctx, userID, filter)
	}
	return Summary{}, nil
}

func (s *stubUsageRepository) SummaryAdmin(ctx context.Context, filter StatsFilter) (Summary, error) {
	if s.summaryAdminFn != nil {
		return s.summaryAdminFn(ctx, filter)
	}
	return Summary{}, nil
}

func (s *stubUsageRepository) StatsByModel(ctx context.Context, filter StatsFilter) ([]ModelStats, error) {
	if s.statsByModelFn != nil {
		return s.statsByModelFn(ctx, filter)
	}
	return nil, nil
}

func (s *stubUsageRepository) StatsByUser(ctx context.Context, filter StatsFilter) ([]UserStats, error) {
	if s.statsByUserFn != nil {
		return s.statsByUserFn(ctx, filter)
	}
	return nil, nil
}

func (s *stubUsageRepository) StatsByAccount(ctx context.Context, filter StatsFilter) ([]AccountStats, error) {
	if s.statsByAccountFn != nil {
		return s.statsByAccountFn(ctx, filter)
	}
	return nil, nil
}

func (s *stubUsageRepository) StatsByGroup(ctx context.Context, filter StatsFilter) ([]GroupStats, error) {
	if s.statsByGroupFn != nil {
		return s.statsByGroupFn(ctx, filter)
	}
	return nil, nil
}

func (s *stubUsageRepository) TrendEntries(ctx context.Context, filter TrendFilter) ([]TrendEntry, error) {
	if s.trendEntriesFn != nil {
		return s.trendEntriesFn(ctx, filter)
	}
	return nil, nil
}
