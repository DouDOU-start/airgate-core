package usage

import "testing"

func TestBuildTrendBuckets_AggregatesAndSorts(t *testing.T) {
	buckets := BuildTrendBuckets([]TrendEntry{
		{
			CreatedAt:         "2026-04-02T10:20:00Z",
			InputTokens:       10,
			OutputTokens:      5,
			CachedInputTokens: 2,
			ActualCost:        0.3,
			StandardCost:      0.5,
		},
		{
			CreatedAt:         "2026-04-02T10:50:00Z",
			InputTokens:       20,
			OutputTokens:      10,
			CachedInputTokens: 1,
			ActualCost:        0.4,
			StandardCost:      0.6,
		},
		{
			CreatedAt:         "2026-04-02T09:10:00Z",
			InputTokens:       7,
			OutputTokens:      3,
			CachedInputTokens: 0,
			ActualCost:        0.2,
			StandardCost:      0.4,
		},
	}, "hour")

	if got, want := len(buckets), 2; got != want {
		t.Fatalf("expected %d buckets, got %d", want, got)
	}
	if got, want := buckets[0].Time, "2026-04-02 09:00"; got != want {
		t.Fatalf("expected first bucket %s, got %s", want, got)
	}
	if got, want := buckets[1].InputTokens, int64(30); got != want {
		t.Fatalf("expected merged input tokens %d, got %d", want, got)
	}
	if got, want := buckets[1].CacheRead, int64(3); got != want {
		t.Fatalf("expected merged cache read %d, got %d", want, got)
	}
}
