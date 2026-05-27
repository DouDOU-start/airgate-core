package scheduler

import (
	"context"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/account"
	"github.com/DouDOU-start/airgate-core/ent/enttest"
	"github.com/DouDOU-start/airgate-core/ent/migrate"
	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

func TestStateMachineAccountUnavailableEscalatesAfterThreshold(t *testing.T) {
	ctx := context.Background()
	db := openStateMachineTestDB(t, "scheduler_account_unavailable_threshold")
	sm := NewStateMachine(db, nil, nil)
	criticalTransitions := 0
	sm.onCriticalTransition = func() { criticalTransitions++ }

	acc := db.Account.Create().
		SetName("temporary 403").
		SetPlatform("openai").
		SetType("apikey").
		SetCredentials(map[string]string{}).
		SaveX(ctx)

	sm.Apply(ctx, acc.ID, Judgment{Kind: sdk.OutcomeAccountUnavailable, Reason: "HTTP 403"})
	fresh := db.Account.GetX(ctx, acc.ID)
	if fresh.State != account.StateDegraded {
		t.Fatalf("state after first unavailable = %s, want degraded", fresh.State)
	}
	if fresh.StateUntil == nil {
		t.Fatalf("state_until should be set after first unavailable")
	}
	if got := extraInt(fresh.Extra, accountUnavailableCountExtraKey); got != 1 {
		t.Fatalf("unavailable count after first unavailable = %d, want 1", got)
	}

	sm.Apply(ctx, acc.ID, Judgment{Kind: sdk.OutcomeAccountUnavailable, Reason: "HTTP 403"})
	fresh = db.Account.GetX(ctx, acc.ID)
	if got := extraInt(fresh.Extra, accountUnavailableCountExtraKey); got != 1 {
		t.Fatalf("unavailable count during degraded window = %d, want 1", got)
	}

	expireAccountDegradedWindow(ctx, db, acc.ID)

	sm.Apply(ctx, acc.ID, Judgment{Kind: sdk.OutcomeAccountUnavailable, Reason: "HTTP 403"})
	fresh = db.Account.GetX(ctx, acc.ID)
	if fresh.State != account.StateDegraded {
		t.Fatalf("state after second unavailable = %s, want degraded", fresh.State)
	}
	if got := extraInt(fresh.Extra, accountUnavailableCountExtraKey); got != 2 {
		t.Fatalf("unavailable count after second unavailable = %d, want 2", got)
	}

	expireAccountDegradedWindow(ctx, db, acc.ID)

	sm.Apply(ctx, acc.ID, Judgment{Kind: sdk.OutcomeAccountUnavailable, Reason: "HTTP 403"})
	fresh = db.Account.GetX(ctx, acc.ID)
	if fresh.State != account.StateDisabled {
		t.Fatalf("state after third unavailable = %s, want disabled", fresh.State)
	}
	if fresh.StateUntil != nil {
		t.Fatalf("state_until should be cleared after escalation")
	}
	if got := extraInt(fresh.Extra, accountUnavailableCountExtraKey); got != 0 {
		t.Fatalf("unavailable count after escalation = %d, want cleared", got)
	}
	if criticalTransitions != 1 {
		t.Fatalf("critical transitions = %d, want 1", criticalTransitions)
	}
}

func TestStateMachineSuccessClearsAccountUnavailableCount(t *testing.T) {
	ctx := context.Background()
	db := openStateMachineTestDB(t, "scheduler_account_unavailable_success")
	sm := NewStateMachine(db, nil, nil)

	acc := db.Account.Create().
		SetName("temporary 403").
		SetPlatform("openai").
		SetType("apikey").
		SetCredentials(map[string]string{}).
		SetExtra(map[string]interface{}{"keep": "value"}).
		SaveX(ctx)

	sm.Apply(ctx, acc.ID, Judgment{Kind: sdk.OutcomeAccountUnavailable, Reason: "HTTP 403"})
	sm.Apply(ctx, acc.ID, Judgment{Kind: sdk.OutcomeSuccess})

	fresh := db.Account.GetX(ctx, acc.ID)
	if fresh.State != account.StateActive {
		t.Fatalf("state after success = %s, want active", fresh.State)
	}
	if fresh.StateUntil != nil {
		t.Fatalf("state_until should be cleared after success")
	}
	if fresh.ErrorMsg != "" {
		t.Fatalf("error_msg after success = %q, want empty", fresh.ErrorMsg)
	}
	if got := extraInt(fresh.Extra, accountUnavailableCountExtraKey); got != 0 {
		t.Fatalf("unavailable count after success = %d, want cleared", got)
	}
	if fresh.Extra["keep"] != "value" {
		t.Fatalf("unrelated extra value was not preserved: %+v", fresh.Extra)
	}
}

func openStateMachineTestDB(t *testing.T, name string) *ent.Client {
	t.Helper()
	db := enttest.Open(t, "sqlite3", "file:"+name+"?mode=memory&cache=shared&_fk=1", enttest.WithMigrateOptions(migrate.WithGlobalUniqueID(false)))
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}
	})
	return db
}

func expireAccountDegradedWindow(ctx context.Context, db *ent.Client, accountID int) {
	db.Account.UpdateOneID(accountID).
		SetStateUntil(time.Now().Add(-time.Second)).
		ExecX(ctx)
}
