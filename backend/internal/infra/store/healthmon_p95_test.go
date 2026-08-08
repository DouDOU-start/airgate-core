package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
)

func TestHealthmonNearestRankP95(t *testing.T) {
	tests := []struct {
		name   string
		values []int64
		want   int64
	}{
		{name: "empty", want: 0},
		{name: "single", values: []int64{420}, want: 420},
		{name: "nineteen uses max", values: sequenceInt64(1, 19), want: 19},
		{name: "twenty drops top five percent", values: sequenceInt64(1, 20), want: 19},
		{name: "unsorted", values: []int64{82_000, 1_000, 1_000, 1_000, 1_000, 1_000, 1_000, 1_000, 1_000, 1_000, 1_000, 1_000, 1_000, 1_000, 1_000, 1_000, 1_000, 1_000, 1_000, 1_000}, want: 1_000},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := healthmonNearestRankP95(test.values); got != test.want {
				t.Fatalf("healthmonNearestRankP95(%v) = %d, want %d", test.values, got, test.want)
			}
		})
	}
}

func TestHealthmonPostgresP95ExpressionsUsePercentileDisc(t *testing.T) {
	store := &HealthmonStore{sqlDialect: dialect.Postgres}
	duration, ttft := store.healthmonP95Expressions(`"duration_ms"`, `"first_token_ms"`)
	if !strings.Contains(duration, "PERCENTILE_DISC(0.95)") {
		t.Fatalf("duration expression = %q, want percentile_disc", duration)
	}
	if !strings.Contains(ttft, "PERCENTILE_DISC(0.95)") || !strings.Contains(ttft, `FILTER (WHERE "first_token_ms" > 0)`) {
		t.Fatalf("ttft expression = %q, want percentile_disc filtered to positive samples", ttft)
	}
}

func TestHealthmonStoreComputesExactP95PerDimensionAndSummary(t *testing.T) {
	db := enttestOpen(t)
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}
	}()
	ctx := context.Background()

	fastGroup, err := db.Group.Create().SetName("p95-fast").Save(ctx)
	if err != nil {
		t.Fatalf("create fast group: %v", err)
	}
	slowGroup, err := db.Group.Create().SetName("p95-slow").Save(ctx)
	if err != nil {
		t.Fatalf("create slow group: %v", err)
	}
	channel := createTestChannel(t, db, "p95-channel")
	fastKey := createTestKey(t, db, channel.ID)
	slowKey := createTestKey(t, db, channel.ID)

	at := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	since := at.Add(-time.Hour)
	createUsage := func(groupID, keyID int, source string, createdAt time.Time, durationMs, ttftMs int64) {
		t.Helper()
		if _, err := db.UsageLog.Create().
			SetModel("gpt-p95").
			SetGroupID(groupID).
			SetChannelKeyID(keyID).
			SetSource(source).
			SetDurationMs(durationMs).
			SetFirstTokenMs(ttftMs).
			SetCreatedAt(createdAt).
			Save(ctx); err != nil {
			t.Fatalf("create usage log: %v", err)
		}
	}

	// 19 个正常 TTFT + 1 个缺失 TTFT；缺失值不参与 TTFT P95。
	for range 19 {
		createUsage(fastGroup.ID, fastKey, "relay", at, 1_500, 1_000)
	}
	createUsage(fastGroup.ID, fastKey, "relay", at, 1_500, 0)
	// 单独的慢维度用于证明整体 P95 不能取 max(各维度 P95)。
	createUsage(slowGroup.ID, slowKey, "relay", at, 90_000, 82_000)

	// 非 relay 与窗口外极值不得污染结果。
	createUsage(fastGroup.ID, fastKey, "channel_test", at, 999_000, 999_000)
	createUsage(fastGroup.ID, fastKey, "relay", since.Add(-time.Second), 888_000, 888_000)

	store := NewHealthmonStore(db)
	groups, err := store.AggregateSuccessByGroup(ctx, since)
	if err != nil {
		t.Fatalf("AggregateSuccessByGroup: %v", err)
	}
	groupByID := make(map[int]int64, len(groups))
	groupMaxByID := make(map[int]int64, len(groups))
	for _, row := range groups {
		groupByID[row.DimID] = row.P95TTFT
		groupMaxByID[row.DimID] = int64(row.MaxTTFT)
	}
	if groupByID[fastGroup.ID] != 1_000 || groupByID[slowGroup.ID] != 82_000 {
		t.Fatalf("group P95 = %v, want fast=1000 slow=82000", groupByID)
	}
	if groupMaxByID[fastGroup.ID] != 1_000 || groupMaxByID[slowGroup.ID] != 82_000 {
		t.Fatalf("group max = %v, unexpected filtered max", groupMaxByID)
	}

	keys, err := store.AggregateSuccess(ctx, since)
	if err != nil {
		t.Fatalf("AggregateSuccess: %v", err)
	}
	keyByID := make(map[int]int64, len(keys))
	for _, row := range keys {
		keyByID[row.DimID] = row.P95TTFT
	}
	if keyByID[fastKey] != 1_000 || keyByID[slowKey] != 82_000 {
		t.Fatalf("key P95 = %v, want fast=1000 slow=82000", keyByID)
	}

	summary, err := store.AggregateSuccessSummary(ctx, since)
	if err != nil {
		t.Fatalf("AggregateSuccessSummary: %v", err)
	}
	if summary.Count != 21 || summary.TTFTCount != 20 {
		t.Fatalf("summary counts = total %d ttft %d, want 21/20", summary.Count, summary.TTFTCount)
	}
	if summary.P95TTFT != 1_000 || summary.MaxTTFT != 82_000 {
		t.Fatalf("summary TTFT = p95 %d max %.0f, want 1000/82000", summary.P95TTFT, summary.MaxTTFT)
	}
	if summary.P95Duration != 1_500 || summary.MaxDuration != 90_000 {
		t.Fatalf("summary duration = p95 %d max %.0f, want 1500/90000", summary.P95Duration, summary.MaxDuration)
	}
	if summary.AvgTTFT != 5_050 {
		t.Fatalf("summary avg TTFT = %.0f, want 5050 (zero TTFT excluded)", summary.AvgTTFT)
	}

	visibleSummary, err := store.AggregateSuccessSummaryByGroupIDs(ctx, since, []int{fastGroup.ID, slowGroup.ID})
	if err != nil {
		t.Fatalf("AggregateSuccessSummaryByGroupIDs: %v", err)
	}
	if visibleSummary.P95TTFT != 1_000 || visibleSummary.Count != 21 {
		t.Fatalf("visible summary = %+v, want overall P95 1000 and count 21", visibleSummary)
	}
	slowOnly, err := store.AggregateSuccessSummaryByGroupIDs(ctx, since, []int{slowGroup.ID})
	if err != nil {
		t.Fatalf("slow-only summary: %v", err)
	}
	if slowOnly.P95TTFT != 82_000 || slowOnly.Count != 1 {
		t.Fatalf("slow-only summary = %+v, want P95 82000 count 1", slowOnly)
	}
	empty, err := store.AggregateSuccessSummaryByGroupIDs(ctx, since, nil)
	if err != nil {
		t.Fatalf("empty summary: %v", err)
	}
	if empty.Count != 0 || empty.P95TTFT != 0 {
		t.Fatalf("empty allow-list summary = %+v, want zero value", empty)
	}
}

func sequenceInt64(from, to int64) []int64 {
	out := make([]int64, 0, to-from+1)
	for value := from; value <= to; value++ {
		out = append(out, value)
	}
	return out
}
