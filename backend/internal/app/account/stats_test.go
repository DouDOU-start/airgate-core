package account

import (
	"testing"
	"time"
)

func TestResolveStatsRange_DefaultWindow(t *testing.T) {
	now := time.Date(2026, 4, 2, 10, 30, 0, 0, time.FixedZone("CST", 8*3600))

	startDate, endDate, err := ResolveStatsRange(now, StatsQuery{})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if got, want := startDate.Format("2006-01-02"), "2026-03-04"; got != want {
		t.Fatalf("expected start date %s, got %s", want, got)
	}
	if got, want := endDate.Format("2006-01-02"), "2026-04-02"; got != want {
		t.Fatalf("expected end date %s, got %s", want, got)
	}
}

func TestBuildStatsResult_FillsDailyTrendAndSortsModels(t *testing.T) {
	location := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 4, 2, 18, 0, 0, 0, location)
	startDate := time.Date(2026, 3, 31, 0, 0, 0, 0, location)
	endDate := time.Date(2026, 4, 2, 23, 59, 59, 0, location)

	result := BuildStatsResult(Account{
		ID:       7,
		Name:     "主账号",
		Platform: "openai",
		Status:   "active",
	}, []UsageLog{
		{
			Model:        "gpt-4.1",
			InputTokens:  100,
			OutputTokens: 60,
			TotalCost:    1.2,
			ActualCost:   0.9,
			DurationMs:   500,
			CreatedAt:    time.Date(2026, 4, 2, 9, 0, 0, 0, location),
		},
		{
			Model:        "gpt-4.1",
			InputTokens:  50,
			OutputTokens: 30,
			TotalCost:    0.6,
			ActualCost:   0.4,
			DurationMs:   300,
			CreatedAt:    time.Date(2026, 4, 1, 15, 0, 0, 0, location),
		},
		{
			Model:        "gpt-4o-mini",
			InputTokens:  20,
			OutputTokens: 10,
			TotalCost:    0.2,
			ActualCost:   0.1,
			DurationMs:   100,
			CreatedAt:    time.Date(2026, 4, 1, 20, 0, 0, 0, location),
		},
	}, now, startDate, endDate)

	if got, want := len(result.DailyTrend), 3; got != want {
		t.Fatalf("expected %d daily records, got %d", want, got)
	}
	if got, want := result.DailyTrend[0].Date, "2026-03-31"; got != want {
		t.Fatalf("expected first daily date %s, got %s", want, got)
	}
	if result.DailyTrend[0].Count != 0 {
		t.Fatalf("expected empty day to be filled with zero count, got %d", result.DailyTrend[0].Count)
	}
	if got, want := result.Today.Count, 1; got != want {
		t.Fatalf("expected today count %d, got %d", want, got)
	}
	if got, want := result.ActiveDays, 2; got != want {
		t.Fatalf("expected active days %d, got %d", want, got)
	}
	if got, want := result.Models[0].Model, "gpt-4.1"; got != want {
		t.Fatalf("expected top model %s, got %s", want, got)
	}
	if got, want := result.AvgDurationMs, int64(300); got != want {
		t.Fatalf("expected avg duration %d, got %d", want, got)
	}
	if got, want := result.PeakRequestDay.Date, "2026-04-01"; got != want {
		t.Fatalf("expected peak request day %s, got %s", want, got)
	}
}
