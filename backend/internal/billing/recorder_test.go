package billing

import (
	"context"
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
		SetType("openai_compatible").
		SetBaseURL("https://api.openai.com").
		Save(ctx)
	if err != nil {
		t.Fatalf("创建渠道失败: %v", err)
	}

	recorder := NewRecorder(db, 0)
	recorder.Start()
	recorder.Record(UsageRecord{
		UserID:    user.ID,
		UserEmail: user.Email,
		ChannelID: channel.ID,
		GroupID:   group.ID,
		Model:     "gpt-5",
	})
	recorder.Stop() // 排空缓冲，保证记录已落库

	log, err := db.UsageLog.Query().Only(ctx)
	if err != nil {
		t.Fatalf("查询 usage log 失败: %v", err)
	}
	if log.UserIDSnapshot != user.ID || log.UserEmailSnapshot != user.Email {
		t.Fatalf("用户快照 = (%d, %q), 期望 (%d, %q)", log.UserIDSnapshot, log.UserEmailSnapshot, user.ID, user.Email)
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
