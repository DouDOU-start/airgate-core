package billing

import (
	"context"
	"testing"

	"entgo.io/ent/dialect/sql/schema"
	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/enttest"
	_ "github.com/mattn/go-sqlite3"
)

func TestRecordSyncPersistsUserEmailSnapshot(t *testing.T) {
	db := enttest.Open(t, "sqlite3", "file:billing_recorder?mode=memory&cache=shared&_fk=1", enttest.WithMigrateOptions(schema.WithGlobalUniqueID(false)))
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}
	}()

	ctx := context.Background()
	user := createBillingTestUser(t, ctx, db, "billing-snapshot@example.com")
	group, err := db.Group.Create().
		SetName("OpenAI").
		SetPlatform("openai").
		Save(ctx)
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	account, err := db.Account.Create().
		SetName("acc").
		SetPlatform("openai").
		Save(ctx)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}

	recorder := NewRecorder(db, 0)
	usageID, err := recorder.RecordSync(ctx, UsageRecord{
		UserID:    user.ID,
		UserEmail: user.Email,
		AccountID: account.ID,
		GroupID:   group.ID,
		Platform:  "openai",
		Model:     "gpt-5",
	})
	if err != nil {
		t.Fatalf("RecordSync returned error: %v", err)
	}

	log, err := db.UsageLog.Get(ctx, usageID)
	if err != nil {
		t.Fatalf("get usage log: %v", err)
	}
	if log.UserIDSnapshot != user.ID || log.UserEmailSnapshot != user.Email {
		t.Fatalf("usage snapshot = (%d, %q), want (%d, %q)", log.UserIDSnapshot, log.UserEmailSnapshot, user.ID, user.Email)
	}
}

func createBillingTestUser(t *testing.T, ctx context.Context, db *ent.Client, email string) *ent.User {
	t.Helper()
	user, err := db.User.Create().
		SetEmail(email).
		SetPasswordHash("secret").
		Save(ctx)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user
}
