package server

import (
	"context"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/DouDOU-start/airgate-core/ent/enttest"
	entmigrate "github.com/DouDOU-start/airgate-core/ent/migrate"
	"github.com/DouDOU-start/airgate-core/internal/billing"
)

func TestAccountFirstTokenStore只加载近期成功流(t *testing.T) {
	db := enttest.Open(t, "sqlite3", "file:account_latency_adapter?mode=memory&cache=shared&_fk=1",
		enttest.WithMigrateOptions(entmigrate.WithGlobalUniqueID(false)))
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("关闭测试数据库失败：%v", err)
		}
	})

	ctx := context.Background()
	now := time.Now()
	since := now.Add(-15 * time.Minute)
	account, err := db.Account.Create().SetName("测试账号").SetPlatform("codex").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	create := func(model string, firstTokenMs int64, createdAt time.Time, stream bool, status string, withAccount bool) {
		t.Helper()
		builder := db.UsageLog.Create().
			SetModel(model).
			SetFirstTokenMs(firstTokenMs).
			SetCreatedAt(createdAt).
			SetStream(stream).
			SetUsageStatus(status)
		if withAccount {
			builder.SetAccount(account)
		}
		if _, createErr := builder.Save(ctx); createErr != nil {
			t.Fatal(createErr)
		}
	}

	create("gpt-5", 900, now.Add(-10*time.Minute), true, billing.UsageStatusCompleted, true)
	create("gpt-5", 300, now.Add(-time.Minute), true, billing.UsageStatusCompleted, true)
	create("gpt-5", 100, now.Add(-30*time.Second), false, billing.UsageStatusCompleted, true)
	create("gpt-5", 100, now.Add(-20*time.Second), true, billing.UsageStatusMissing, true)
	create("gpt-5", 100, now.Add(-10*time.Second), true, billing.UsageStatusCompleted, false)
	create("gpt-5", 0, now.Add(-5*time.Second), true, billing.UsageStatusCompleted, true)
	create("gpt-5", 1_500, since.Add(-time.Second), true, billing.UsageStatusCompleted, true)

	store := accountFirstTokenStore{db: db}
	samples, err := store.LoadRecentAccountFirstTokenSamples(ctx, since, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 2 {
		t.Fatalf("有效预热样本数 = %d，期望 2", len(samples))
	}
	if samples[0].AccountID != account.ID || samples[0].Model != "gpt-5" || samples[0].FirstTokenMs != 300 {
		t.Fatalf("最新样本异常：%+v", samples[0])
	}
	if samples[1].FirstTokenMs != 900 {
		t.Fatalf("较早样本异常：%+v", samples[1])
	}

	limited, err := store.LoadRecentAccountFirstTokenSamples(ctx, since, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 || limited[0].FirstTokenMs != 300 {
		t.Fatalf("有限行查询未保留最新样本：%+v", limited)
	}
}

func TestAccountFirstTokenStore排除多Attempt请求(t *testing.T) {
	db := enttest.Open(t, "sqlite3", "file:account_latency_multi_attempt?mode=memory&cache=shared&_fk=1",
		enttest.WithMigrateOptions(entmigrate.WithGlobalUniqueID(false)))
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("关闭测试数据库失败：%v", err)
		}
	})

	ctx := context.Background()
	now := time.Now()
	account, err := db.Account.Create().SetName("测试账号").SetPlatform("codex").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	createUsage := func(requestID string, firstTokenMs int64, createdAt time.Time) {
		t.Helper()
		if _, createErr := db.UsageLog.Create().
			SetModel("gpt-5").
			SetRequestID(requestID).
			SetFirstTokenMs(firstTokenMs).
			SetCreatedAt(createdAt).
			SetStream(true).
			SetUsageStatus(billing.UsageStatusCompleted).
			SetAccount(account).
			Save(ctx); createErr != nil {
			t.Fatal(createErr)
		}
	}
	createAudit := func(requestID string, attempts int) {
		t.Helper()
		audit, createErr := db.RequestAuditLog.Create().SetRequestID(requestID).Save(ctx)
		if createErr != nil {
			t.Fatal(createErr)
		}
		for seq := 1; seq <= attempts; seq++ {
			if _, createErr = db.RequestAuditAttempt.Create().
				SetRequestAuditID(audit.ID).
				SetSeq(seq).
				SetRouteKind("account").
				Save(ctx); createErr != nil {
				t.Fatal(createErr)
			}
		}
	}

	createUsage("req-single-attempt", 100, now.Add(-3*time.Second))
	createAudit("req-single-attempt", 1)
	createUsage("req-multi-attempt", 200, now.Add(-2*time.Second))
	createAudit("req-multi-attempt", 2)
	createUsage("req-audit-missing", 300, now.Add(-time.Second))

	store := accountFirstTokenStore{db: db}
	samples, err := store.LoadRecentAccountFirstTokenSamples(ctx, now.Add(-time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 2 {
		t.Fatalf("过滤后的预热样本数 = %d，期望 2，样本：%+v", len(samples), samples)
	}
	if samples[0].FirstTokenMs != 300 || samples[1].FirstTokenMs != 100 {
		t.Fatalf("单 attempt 或无审计样本保留异常：%+v", samples)
	}
}
