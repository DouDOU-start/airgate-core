package store

import (
	"context"
	"testing"
	"time"

	appriskcontrol "github.com/DouDOU-start/airgate-core/internal/app/riskcontrol"
	"github.com/DouDOU-start/airgate-core/internal/moderation"
)

func newModerationEntry(userID int, action string, flagged bool) moderation.LogEntry {
	return moderation.LogEntry{
		RequestID:       "req-" + action,
		UserID:          userID,
		UserEmail:       "u@example.com",
		GroupID:         1,
		Endpoint:        "/v1/chat/completions",
		Protocol:        moderation.ProtocolOpenAIChat,
		Model:           "gpt-4o",
		Mode:            moderation.ModePreBlock,
		Action:          action,
		Flagged:         flagged,
		HighestCategory: "hate",
		HighestScore:    0.9,
		CategoryScores:  map[string]float64{"hate": 0.9},
		InputExcerpt:    "excerpt",
		InputHash:       "hash-" + action,
	}
}

func moderationTestStore(t *testing.T) *ModerationLogStore {
	t.Helper()
	db := enttestOpen(t)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ModerationLog.Delete().Exec(context.Background()); err != nil {
		t.Fatal(err)
	}
	return NewModerationLogStore(db)
}

func TestModerationLogCreateAndList(t *testing.T) {
	s := moderationTestStore(t)
	ctx := context.Background()

	entries := []moderation.LogEntry{
		newModerationEntry(101, moderation.ActionBlock, true),
		newModerationEntry(101, moderation.ActionKeywordBlock, true),
		newModerationEntry(102, moderation.ActionAllow, false),
	}
	errEntry := newModerationEntry(102, moderation.ActionError, false)
	errEntry.Error = "boom"
	entries = append(entries, errEntry)
	for _, e := range entries {
		if err := s.Create(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name   string
		filter appriskcontrol.ListFilter
		want   int
	}{
		{"全部", appriskcontrol.ListFilter{}, 4},
		{"hit", appriskcontrol.ListFilter{Result: appriskcontrol.ResultHit}, 2},
		{"blocked", appriskcontrol.ListFilter{Result: appriskcontrol.ResultBlocked}, 2},
		{"pass", appriskcontrol.ListFilter{Result: appriskcontrol.ResultPass}, 1},
		{"error", appriskcontrol.ListFilter{Result: appriskcontrol.ResultError}, 1},
		{"分组过滤", appriskcontrol.ListFilter{GroupID: intPtr(1)}, 4},
		{"分组不命中", appriskcontrol.ListFilter{GroupID: intPtr(9)}, 0},
		{"搜索邮箱", appriskcontrol.ListFilter{Search: "u@example"}, 4},
		{"搜索不命中", appriskcontrol.ListFilter{Search: "nobody"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.filter.Page, tt.filter.PageSize = 1, 20
			items, total, err := s.List(ctx, tt.filter)
			if err != nil {
				t.Fatal(err)
			}
			if int(total) != tt.want || len(items) != tt.want {
				t.Fatalf("total=%d items=%d, want %d", total, len(items), tt.want)
			}
		})
	}
}

func intPtr(v int) *int { return &v }

func TestModerationLogCountFlaggedSince(t *testing.T) {
	s := moderationTestStore(t)
	ctx := context.Background()
	const userID = 201
	since := time.Now().Add(-time.Hour)

	// 2 条计入 + 1 条 hash_block 排除 + 1 条未命中排除 + 别人的 1 条排除。
	for _, e := range []moderation.LogEntry{
		newModerationEntry(userID, moderation.ActionBlock, true),
		newModerationEntry(userID, moderation.ActionKeywordBlock, true),
		newModerationEntry(userID, moderation.ActionHashBlock, true),
		newModerationEntry(userID, moderation.ActionAllow, false),
		newModerationEntry(999, moderation.ActionBlock, true),
	} {
		if err := s.Create(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.CountFlaggedSince(ctx, userID, since)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}

	// 落一条 auto_banned 行后，此前的违规不再计入（封禁后重算）。
	banEntry := newModerationEntry(userID, moderation.ActionBlock, true)
	banEntry.AutoBanned = true
	if err := s.Create(ctx, banEntry); err != nil {
		t.Fatal(err)
	}
	n, err = s.CountFlaggedSince(ctx, userID, since)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("封禁后计数 = %d, want 0", n)
	}

	// 封禁行之后的新违规重新累计。
	time.Sleep(10 * time.Millisecond)
	late := newModerationEntry(userID, moderation.ActionBlock, true)
	if err := s.Create(ctx, late); err != nil {
		t.Fatal(err)
	}
	n, err = s.CountFlaggedSince(ctx, userID, since)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("封禁后新违规计数 = %d, want 1", n)
	}
}

func TestModerationLogCleanup(t *testing.T) {
	db := enttestOpen(t)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ModerationLog.Delete().Exec(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := NewModerationLogStore(db)
	ctx := context.Background()
	now := time.Now()

	// 直接用 ent 客户端造不同时刻的行（Create 接口不暴露 created_at）。
	mk := func(flagged bool, age time.Duration) {
		if err := db.ModerationLog.Create().
			SetRequestID("r").
			SetFlagged(flagged).
			SetCreatedAt(now.Add(-age)).
			Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	mk(true, 200*24*time.Hour) // 过期命中 → 删
	mk(true, time.Hour)        // 新命中 → 留
	mk(false, 5*24*time.Hour)  // 过期未命中 → 删
	mk(false, time.Hour)       // 新未命中 → 留

	result, err := s.Cleanup(ctx, now.AddDate(0, 0, -180), now.AddDate(0, 0, -3))
	if err != nil {
		t.Fatal(err)
	}
	if result.DeletedHit != 1 || result.DeletedNonHit != 1 {
		t.Fatalf("result = %+v", result)
	}
	rest, err := db.ModerationLog.Query().Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rest != 2 {
		t.Fatalf("剩余 = %d, want 2", rest)
	}
}
