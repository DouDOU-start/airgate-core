package store

import (
	"context"
	"testing"
	"time"

	appusage "github.com/DouDOU-start/airgate-core/internal/app/usage"
)

func TestUsageStoreListPaginationIsStableForIdenticalCreatedAt(t *testing.T) {
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
		total, err := store.CountAdmin(ctx, appusage.ListFilter{})
		if err != nil {
			t.Fatalf("CountAdmin returned error: %v", err)
		}
		if total != 3 {
			t.Fatalf("CountAdmin = %d, want 3", total)
		}

		page1, err := store.ListAdmin(ctx, appusage.ListFilter{Page: 1, PageSize: 2})
		if err != nil {
			t.Fatalf("ListAdmin page 1 returned error: %v", err)
		}
		assertLogIDs(t, page1, 3, 2)

		page2, err := store.ListAdmin(ctx, appusage.ListFilter{Page: 2, PageSize: 2})
		if err != nil {
			t.Fatalf("ListAdmin page 2 returned error: %v", err)
		}
		assertLogIDs(t, page2, 1)
	})

	t.Run("user list", func(t *testing.T) {
		total, err := store.CountUser(ctx, int64(user.ID), appusage.ListFilter{})
		if err != nil {
			t.Fatalf("CountUser returned error: %v", err)
		}
		if total != 3 {
			t.Fatalf("CountUser = %d, want 3", total)
		}

		page1, err := store.ListUser(ctx, int64(user.ID), appusage.ListFilter{Page: 1, PageSize: 2})
		if err != nil {
			t.Fatalf("ListUser page 1 returned error: %v", err)
		}
		assertLogIDs(t, page1, 3, 2)

		page2, err := store.ListUser(ctx, int64(user.ID), appusage.ListFilter{Page: 2, PageSize: 2})
		if err != nil {
			t.Fatalf("ListUser page 2 returned error: %v", err)
		}
		assertLogIDs(t, page2, 1)
	})
}

func TestUsageStoreListAdminFiltersByChannelAndKey(t *testing.T) {
	db := enttestOpen(t)
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}
	}()

	ctx := context.Background()
	user := createTestUser(t, db, "usage-channel-filter@example.com")
	ch := createTestChannel(t, db, "channel-a")
	keyA := createTestKey(t, db, ch.ID)
	keyB := createTestKey(t, db, ch.ID)
	if _, err := db.ChannelKey.UpdateOneID(keyA).SetName("主密钥").Save(ctx); err != nil {
		t.Fatalf("设置渠道密钥名称失败：%v", err)
	}
	otherCh := createTestChannel(t, db, "channel-b")
	otherKey := createTestKey(t, db, otherCh.ID)

	createdAt := time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC)
	// 同渠道两把 key 各一条 + 另一渠道一条，用于验证渠道与 key 两级过滤。
	seed := func(channelID, channelKeyID int) {
		if _, err := db.UsageLog.Create().
			SetModel("gpt-5").
			SetUserID(user.ID).
			SetUserIDSnapshot(user.ID).
			SetUserEmailSnapshot(user.Email).
			SetChannelID(channelID).
			SetChannelKeyID(channelKeyID).
			SetCreatedAt(createdAt).
			Save(ctx); err != nil {
			t.Fatalf("create usage log: %v", err)
		}
	}
	seed(ch.ID, keyA)
	seed(ch.ID, keyB)
	seed(otherCh.ID, otherKey)

	store := NewUsageStore(db)
	channelID := int64(ch.ID)
	channelKeyID := int64(keyA)

	t.Run("filter by channel", func(t *testing.T) {
		total, err := store.CountAdmin(ctx, appusage.ListFilter{ChannelID: &channelID})
		if err != nil {
			t.Fatalf("CountAdmin returned error: %v", err)
		}
		if total != 2 {
			t.Fatalf("CountAdmin by channel = %d, want 2", total)
		}
	})

	t.Run("filter by channel key", func(t *testing.T) {
		total, err := store.CountAdmin(ctx, appusage.ListFilter{ChannelKeyID: &channelKeyID})
		if err != nil {
			t.Fatalf("CountAdmin returned error: %v", err)
		}
		if total != 1 {
			t.Fatalf("CountAdmin by channel key = %d, want 1", total)
		}

		items, err := store.ListAdmin(ctx, appusage.ListFilter{Page: 1, PageSize: 20, ChannelKeyID: &channelKeyID})
		if err != nil {
			t.Fatalf("ListAdmin by channel key returned error: %v", err)
		}
		if len(items) != 1 {
			t.Fatalf("ListAdmin by channel key length = %d, want 1", len(items))
		}
		if items[0].ChannelName != ch.Name || items[0].ChannelKeyID != int64(keyA) || items[0].ChannelKeyName != "主密钥" {
			t.Fatalf("渠道与密钥名称映射异常：%+v", items[0])
		}
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
