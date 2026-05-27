package store

import (
	"context"
	"testing"
	"time"

	appusage "github.com/DouDOU-start/airgate-core/internal/app/usage"
)

func TestUsageStoreListPaginationUsesStableIDOrder(t *testing.T) {
	db := enttestOpen(t)
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}
	}()

	ctx := context.Background()
	user := createTestUser(t, db, "usage-pagination@example.com")
	sameCreatedAt := time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC)

	for range 3 {
		if _, err := db.UsageLog.Create().
			SetPlatform("openai").
			SetModel("gpt-5").
			SetUserID(user.ID).
			SetUserIDSnapshot(user.ID).
			SetUserEmailSnapshot(user.Email).
			SetCreatedAt(sameCreatedAt).
			Save(ctx); err != nil {
			t.Fatalf("create usage log: %v", err)
		}
	}

	store := NewUsageStore(db)

	t.Run("admin list", func(t *testing.T) {
		page1, total, err := store.ListAdmin(ctx, appusage.ListFilter{Page: 1, PageSize: 2})
		if err != nil {
			t.Fatalf("ListAdmin page 1 returned error: %v", err)
		}
		if total != 3 {
			t.Fatalf("ListAdmin page 1 total = %d, want 3", total)
		}
		assertLogIDs(t, page1, 3, 2)

		page2, total, err := store.ListAdmin(ctx, appusage.ListFilter{Page: 2, PageSize: 2})
		if err != nil {
			t.Fatalf("ListAdmin page 2 returned error: %v", err)
		}
		if total != 3 {
			t.Fatalf("ListAdmin page 2 total = %d, want 3", total)
		}
		assertLogIDs(t, page2, 1)
	})

	t.Run("user list", func(t *testing.T) {
		page1, total, err := store.ListUser(ctx, int64(user.ID), appusage.ListFilter{Page: 1, PageSize: 2})
		if err != nil {
			t.Fatalf("ListUser page 1 returned error: %v", err)
		}
		if total != 3 {
			t.Fatalf("ListUser page 1 total = %d, want 3", total)
		}
		assertLogIDs(t, page1, 3, 2)

		page2, total, err := store.ListUser(ctx, int64(user.ID), appusage.ListFilter{Page: 2, PageSize: 2})
		if err != nil {
			t.Fatalf("ListUser page 2 returned error: %v", err)
		}
		if total != 3 {
			t.Fatalf("ListUser page 2 total = %d, want 3", total)
		}
		assertLogIDs(t, page2, 1)
	})
}

func assertLogIDs(t *testing.T, got []appusage.LogRecord, want ...int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d; got %+v", len(got), len(want), got)
	}
	for i, item := range got {
		if item.ID != want[i] {
			t.Fatalf("got[%d].ID = %d, want %d; got %+v", i, item.ID, want[i], got)
		}
	}
}
