package errlog

import (
	"context"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/enttest"
	"github.com/DouDOU-start/airgate-core/ent/migrate"
	entupstreamrequestlog "github.com/DouDOU-start/airgate-core/ent/upstreamrequestlog"
)

func enttestOpen(t *testing.T) *ent.Client {
	t.Helper()
	return enttest.Open(t, "sqlite3", "file:errlog_test?mode=memory&cache=shared&_fk=1",
		enttest.WithMigrateOptions(migrate.WithGlobalUniqueID(false)))
}

// failureEntry 构造一条可折叠的失败留痕（签名由参数决定）。
func failureEntry(model string, channelID int) Entry {
	return Entry{
		RequestID:  "req-" + model,
		Source:     SourceRelay,
		Phase:      PhaseUpstreamExhausted,
		StatusCode: 429,
		ErrorType:  "rate_limit_error",
		Message:    "上游限流",
		Model:      model,
		Endpoint:   "/v1/chat/completions",
		ChannelID:  channelID,
		UserID:     1,
		APIKeyID:   2,
	}
}

func TestFlushFoldsDuplicateFailures(t *testing.T) {
	db := enttestOpen(t)
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	r := NewRecorder(db, nil)

	// 表驱动的分步场景：每步 flush 一批，断言行数与折叠计数演进。
	steps := []struct {
		name        string
		batch       []Entry
		wantRows    int            // 累计总行数
		wantRepeats map[string]int // model → 该签名首行的 repeat_count
	}{
		{
			name: "批内合并：3 条同签名 + 1 条异签名 + 1 条渠道测试失败",
			batch: []Entry{
				failureEntry("gpt-4o", 1),
				failureEntry("gpt-4o", 1),
				failureEntry("gpt-4o", 1),
				failureEntry("claude-3", 2),
				{RequestID: "test", Source: SourceChannelTest, ChannelID: 1, Message: "测试失败"},
			},
			wantRows:    3,
			wantRepeats: map[string]int{"gpt-4o": 3, "claude-3": 1},
		},
		{
			name: "跨批窗口内：同签名对首行自增，不插新行",
			batch: []Entry{
				failureEntry("gpt-4o", 1),
				failureEntry("gpt-4o", 1),
			},
			wantRows:    3,
			wantRepeats: map[string]int{"gpt-4o": 5},
		},
		{
			name: "已计费失败不折叠：两条相同 billed 行各占一行",
			batch: func() []Entry {
				e1 := failureEntry("gpt-4o", 1)
				e1.Billed = true
				e2 := failureEntry("gpt-4o", 1)
				e2.Billed = true
				return []Entry{e1, e2}
			}(),
			wantRows:    5,
			wantRepeats: map[string]int{"gpt-4o": 5},
		},
	}

	for _, step := range steps {
		r.flush(ctx, step.batch)

		total := db.UpstreamRequestLog.Query().CountX(ctx)
		if total != step.wantRows {
			t.Fatalf("%s: 总行数 = %d, want %d", step.name, total, step.wantRows)
		}
		for model, want := range step.wantRepeats {
			got := db.UpstreamRequestLog.Query().
				Where(
					entupstreamrequestlog.ModelEQ(model),
					entupstreamrequestlog.BilledEQ(false),
				).
				OnlyX(ctx).RepeatCount
			if got != want {
				t.Fatalf("%s: %s 首行 repeat_count = %d, want %d", step.name, model, got, want)
			}
		}
	}

	// 窗口过期：手动把缓存推成过期，再来同签名 → 插新行而非继续自增。
	for k, ref := range r.folds {
		ref.until = time.Now().Add(-time.Second)
		r.folds[k] = ref
	}
	r.flush(ctx, []Entry{failureEntry("gpt-4o", 1)})

	rows := db.UpstreamRequestLog.Query().
		Where(
			entupstreamrequestlog.ModelEQ("gpt-4o"),
			entupstreamrequestlog.BilledEQ(false),
		).
		AllX(ctx)
	if len(rows) != 2 {
		t.Fatalf("窗口过期后 gpt-4o 未计费行数 = %d, want 2（原首行 + 新窗口首行）", len(rows))
	}
	counts := map[int]bool{}
	for _, row := range rows {
		counts[row.RepeatCount] = true
	}
	if !counts[5] || !counts[1] {
		t.Fatalf("窗口过期后 repeat_count 组合 = %v, want {5,1}", counts)
	}
}

// TestStopDrainsWithoutClosingChannel 关停语义回归：
// Stop 排空并落库全部缓冲，且之后 Record 不 panic（关停窗口内在途请求仍会投递）。
func TestStopDrainsWithoutClosingChannel(t *testing.T) {
	db := enttestOpen(t)
	defer func() { _ = db.Close() }()
	r := NewRecorder(db, nil)
	r.Start()

	const n = 5
	for i := 0; i < n; i++ {
		r.Record(failureEntry("m", i+1)) // 不同 channel → 不同签名，不触发折叠
	}
	r.Stop()

	// 不得 panic；条目静默丢弃（run 已退出）。
	r.Record(failureEntry("late", 99))

	total := db.UpstreamRequestLog.Query().CountX(context.Background())
	if total != n {
		t.Fatalf("Stop 排空后落库行数 = %d, want %d", total, n)
	}
}

func TestFoldKeyExcludesBilled(t *testing.T) {
	cases := []struct {
		name  string
		entry Entry
		want  bool // 是否可折叠（key 非空）
	}{
		{"普通失败可折叠", failureEntry("m", 1), true},
		{"渠道测试失败可折叠", Entry{Source: SourceChannelTest, ChannelID: 1}, true},
		{"已计费失败不折叠", func() Entry { e := failureEntry("m", 1); e.Billed = true; return e }(), false},
	}
	for _, tc := range cases {
		if got := foldKey(tc.entry) != ""; got != tc.want {
			t.Errorf("%s: foldKey 非空 = %v, want %v", tc.name, got, tc.want)
		}
	}

	// 任一签名维度变化都必须产生不同 key（防误折叠）。
	base := failureEntry("m", 1)
	variants := map[string]Entry{}
	v := base
	v.Phase = PhaseQueueTimeout
	variants["phase"] = v
	v = base
	v.StatusCode = 500
	variants["status"] = v
	v = base
	v.Model = "other"
	variants["model"] = v
	v = base
	v.ChannelID = 9
	variants["channel"] = v
	v = base
	v.UserID = 9
	variants["user"] = v
	for dim, entry := range variants {
		if foldKey(entry) == foldKey(base) {
			t.Errorf("维度 %s 变化未改变折叠签名", dim)
		}
	}
}
