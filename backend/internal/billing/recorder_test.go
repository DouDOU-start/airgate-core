package billing

import (
	"context"
	"sync"
	"testing"

	"entgo.io/ent/dialect/sql/schema"
	_ "github.com/mattn/go-sqlite3"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/enttest"
)

// TestRecordPersistsUserEmailSnapshot 经异步 Record + Stop 排空的完整链路，
// 验证 user_id/user_email 快照列随记录落库。
func TestRecordPersistsUserEmailSnapshot(t *testing.T) {
	db := enttest.Open(t, "sqlite3", "file:billing_recorder?mode=memory&cache=shared&_fk=1", enttest.WithMigrateOptions(schema.WithGlobalUniqueID(false)))
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("关闭数据库失败: %v", err)
		}
	}()

	ctx := context.Background()
	user := createBillingTestUser(t, ctx, db, "billing-snapshot@example.com")
	group, err := db.Group.Create().
		SetName("OpenAI").
		SetPlatform("openai").
		Save(ctx)
	if err != nil {
		t.Fatalf("创建分组失败: %v", err)
	}
	channel, err := db.Channel.Create().
		SetName("chan").
		SetBaseURL("https://api.openai.com").
		Save(ctx)
	if err != nil {
		t.Fatalf("创建渠道失败: %v", err)
	}

	recorder := NewRecorder(db, 0)
	recorder.Start()
	recorder.Record(UsageRecord{
		UserID:      user.ID,
		UserEmail:   user.Email,
		ChannelID:   channel.ID,
		GroupID:     group.ID,
		Model:       "gpt-5",
		UsageStatus: UsageStatusStreamAbortedUsageMissing,
	})
	recorder.Stop() // 排空缓冲，保证记录已落库

	log, err := db.UsageLog.Query().Only(ctx)
	if err != nil {
		t.Fatalf("查询 usage log 失败: %v", err)
	}
	if log.UserIDSnapshot != user.ID || log.UserEmailSnapshot != user.Email {
		t.Fatalf("用户快照 = (%d, %q), 期望 (%d, %q)", log.UserIDSnapshot, log.UserEmailSnapshot, user.ID, user.Email)
	}
	if log.UsageStatus != UsageStatusStreamAbortedUsageMissing {
		t.Fatalf("usage_status = %q，期望 %q", log.UsageStatus, UsageStatusStreamAbortedUsageMissing)
	}
}

func TestNormalizedUsageStatus(t *testing.T) {
	if got := normalizedUsageStatus(""); got != UsageStatusCompleted {
		t.Fatalf("空状态应归一为 completed，实际 %q", got)
	}
	if got := normalizedUsageStatus(UsageStatusMissing); got != UsageStatusMissing {
		t.Fatalf("已知异常状态不应被改写，实际 %q", got)
	}
	if got := normalizedUsageStatus("未知状态"); got != UsageStatusCompleted {
		t.Fatalf("未知状态应归一为 completed，实际 %q", got)
	}
}

// TestRecorderFiresBalanceChargedHook 验证扣费事务提交后触发 onBalanceCharged，
// 且只回传实际扣减了余额的用户 ID（供余额预警检查）。
func TestRecorderFiresBalanceChargedHook(t *testing.T) {
	db := enttest.Open(t, "sqlite3", "file:billing_hook?mode=memory&cache=shared&_fk=1", enttest.WithMigrateOptions(schema.WithGlobalUniqueID(false)))
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("关闭数据库失败: %v", err)
		}
	}()

	ctx := context.Background()
	user := createBillingTestUser(t, ctx, db, "billing-hook@example.com")
	if err := db.User.UpdateOneID(user.ID).SetBalance(10).Exec(ctx); err != nil {
		t.Fatalf("设置余额失败: %v", err)
	}

	var mu sync.Mutex
	var got []int
	recorder := NewRecorder(db, 0)
	recorder.SetBalanceChargedHook(func(ids []int) {
		mu.Lock()
		got = append(got, ids...)
		mu.Unlock()
	})
	recorder.Start()
	recorder.Record(UsageRecord{
		UserID:     user.ID,
		UserEmail:  user.Email,
		Model:      "gpt-5",
		ActualCost: 3,
	})
	recorder.Stop() // 排空缓冲，保证扣费事务已提交且 hook 已触发

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != user.ID {
		t.Fatalf("hook 收到 userIDs = %v，期望 [%d]", got, user.ID)
	}
}

func createBillingTestUser(t *testing.T, ctx context.Context, db *ent.Client, email string) *ent.User {
	t.Helper()
	user, err := db.User.Create().
		SetEmail(email).
		SetPasswordHash("secret").
		Save(ctx)
	if err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}
	return user
}
