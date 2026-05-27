package store

import (
	"context"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func TestDashboardStoreLoadStatsSnapshotAggregatesUsageLogsInSQL(t *testing.T) {
	db := enttestOpen(t)
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}
	}()

	ctx := context.Background()
	todayStart := time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC)
	fiveMinAgo := time.Date(2026, 5, 27, 11, 55, 0, 0, time.UTC)

	u, err := db.User.Create().
		SetEmail("active@example.com").
		SetPasswordHash("secret").
		SetCreatedAt(todayStart.Add(30 * time.Minute)).
		Save(ctx)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	if _, err := db.UsageLog.Create().
		SetPlatform("openai").
		SetModel("gpt-4.1").
		SetInputTokens(10).
		SetOutputTokens(20).
		SetCachedInputTokens(3).
		SetCacheCreationTokens(4).
		SetActualCost(1.5).
		SetTotalCost(2.0).
		SetDurationMs(1200).
		SetFirstTokenMs(300).
		SetUser(u).
		SetCreatedAt(todayStart.Add(2 * time.Hour)).
		Save(ctx); err != nil {
		t.Fatalf("create relation usage log: %v", err)
	}

	if _, err := db.UsageLog.Create().
		SetPlatform("openai").
		SetModel("gpt-image-1").
		SetInputTokens(1).
		SetOutputTokens(2).
		SetActualCost(3.5).
		SetTotalCost(5.0).
		SetDurationMs(2400).
		SetUserIDSnapshot(u.ID).
		SetUserEmailSnapshot(u.Email).
		SetCreatedAt(fiveMinAgo.Add(1 * time.Minute)).
		Save(ctx); err != nil {
		t.Fatalf("create snapshot usage log: %v", err)
	}

	store := NewDashboardStore(db)
	snapshot, err := store.LoadStatsSnapshot(ctx, todayStart, fiveMinAgo, u.ID)
	if err != nil {
		t.Fatalf("LoadStatsSnapshot returned error: %v", err)
	}

	if snapshot.TotalUsers != 1 || snapshot.NewUsersToday != 1 {
		t.Fatalf("user counts = (%d, %d), want (1, 1)", snapshot.TotalUsers, snapshot.NewUsersToday)
	}
	if snapshot.TodayRequests != 2 || snapshot.TodayImageRequests != 1 || snapshot.TodayNonImageRequests != 1 {
		t.Fatalf("today request counts = (%d, %d, %d), want (2, 1, 1)", snapshot.TodayRequests, snapshot.TodayImageRequests, snapshot.TodayNonImageRequests)
	}
	if snapshot.TodayTokens != 40 || snapshot.AllTimeTokens != 40 || snapshot.RecentTokens != 3 {
		t.Fatalf("token counts = (%d, %d, %d), want (40, 40, 3)", snapshot.TodayTokens, snapshot.AllTimeTokens, snapshot.RecentTokens)
	}
	if snapshot.TodayCost != 5.0 || snapshot.TodayStandardCost != 7.0 {
		t.Fatalf("today costs = (%v, %v), want (5.0, 7.0)", snapshot.TodayCost, snapshot.TodayStandardCost)
	}
	if snapshot.AllTimeCost != 5.0 || snapshot.AllTimeStandardCost != 7.0 {
		t.Fatalf("all-time costs = (%v, %v), want (5.0, 7.0)", snapshot.AllTimeCost, snapshot.AllTimeStandardCost)
	}
	if snapshot.TodayNonImageDurationMs != 1200 || snapshot.TodayFirstTokenRequests != 1 || snapshot.TodayFirstTokenMs != 300 || snapshot.TodayImageDurationMs != 2400 {
		t.Fatalf("duration stats = (%d, %d, %d, %d), want (1200, 1, 300, 2400)", snapshot.TodayNonImageDurationMs, snapshot.TodayFirstTokenRequests, snapshot.TodayFirstTokenMs, snapshot.TodayImageDurationMs)
	}
	if snapshot.ActiveUsers != 1 {
		t.Fatalf("ActiveUsers = %d, want 1", snapshot.ActiveUsers)
	}
	if snapshot.AllTimeRequests != 2 || snapshot.RecentRequests != 1 {
		t.Fatalf("request totals = (%d, %d), want (2, 1)", snapshot.AllTimeRequests, snapshot.RecentRequests)
	}
}

func TestDashboardStoreListTrendLogsIncludesSnapshotOnlyRows(t *testing.T) {
	db := enttestOpen(t)
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}
	}()

	ctx := context.Background()
	todayStart := time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC)
	endTime := todayStart.Add(24 * time.Hour)

	u, err := db.User.Create().
		SetEmail("trend@example.com").
		SetPasswordHash("secret").
		SetCreatedAt(todayStart.Add(30 * time.Minute)).
		Save(ctx)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	if _, err := db.UsageLog.Create().
		SetPlatform("openai").
		SetModel("gpt-4.1").
		SetInputTokens(10).
		SetOutputTokens(20).
		SetActualCost(1.5).
		SetTotalCost(2.0).
		SetUser(u).
		SetCreatedAt(todayStart.Add(2 * time.Hour)).
		Save(ctx); err != nil {
		t.Fatalf("create relation usage log: %v", err)
	}

	if _, err := db.UsageLog.Create().
		SetPlatform("openai").
		SetModel("gpt-image-1").
		SetInputTokens(1).
		SetOutputTokens(2).
		SetActualCost(3.5).
		SetTotalCost(5.0).
		SetUserIDSnapshot(u.ID).
		SetCreatedAt(todayStart.Add(3 * time.Hour)).
		Save(ctx); err != nil {
		t.Fatalf("create snapshot usage log: %v", err)
	}

	store := NewDashboardStore(db)
	logs, err := store.ListTrendLogs(ctx, todayStart, endTime, u.ID)
	if err != nil {
		t.Fatalf("ListTrendLogs returned error: %v", err)
	}
	if len(logs) != 2 {
		t.Fatalf("len(logs) = %d, want 2", len(logs))
	}

	var snapshotOnlyFound bool
	for _, log := range logs {
		if log.Model == "gpt-image-1" {
			snapshotOnlyFound = true
			if log.UserID != u.ID || log.UserEmail != u.Email {
				t.Fatalf("snapshot-only log user fallback = (%d, %q), want (%d, %q)", log.UserID, log.UserEmail, u.ID, u.Email)
			}
		}
	}
	if !snapshotOnlyFound {
		t.Fatal("snapshot-only usage log was not returned")
	}
}
