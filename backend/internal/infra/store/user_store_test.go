package store

import (
	"context"
	"testing"

	"github.com/DouDOU-start/airgate-core/ent"
	entbalancelog "github.com/DouDOU-start/airgate-core/ent/balancelog"
	appusage "github.com/DouDOU-start/airgate-core/internal/app/usage"
)

func TestUserStoreDeleteKeepsUsageAndBillingHistory(t *testing.T) {
	db := enttestOpen(t)
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}
	}()

	ctx := context.Background()
	user := createTestUser(t, db, "deleted-user@example.com")
	key, err := db.APIKey.Create().
		SetName("test-key").
		SetKeyHash("hash").
		SetUserID(user.ID).
		Save(ctx)
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}
	if _, err := db.UsageLog.Create().
		SetModel("gpt-test").
		SetUserID(user.ID).
		SetUserIDSnapshot(user.ID).
		SetUserEmailSnapshot(user.Email).
		SetAPIKeyID(key.ID).
		SetTotalCost(1.25).
		SetActualCost(1.25).
		SetBilledCost(2.50).
		Save(ctx); err != nil {
		t.Fatalf("create usage log: %v", err)
	}
	if _, err := db.BalanceLog.Create().
		SetAction(entbalancelog.ActionSubtract).
		SetAmount(2.50).
		SetBeforeBalance(10).
		SetAfterBalance(7.50).
		SetRemark("usage charge").
		SetUserID(user.ID).
		SetUserIDSnapshot(user.ID).
		SetUserEmailSnapshot(user.Email).
		Save(ctx); err != nil {
		t.Fatalf("create balance log: %v", err)
	}

	if err := NewUserStore(db).Delete(ctx, user.ID); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}

	if count, err := db.User.Query().Count(ctx); err != nil || count != 0 {
		t.Fatalf("user count = %d, err = %v; want 0", count, err)
	}
	if count, err := db.APIKey.Query().Count(ctx); err != nil || count != 0 {
		t.Fatalf("api key count = %d, err = %v; want 0", count, err)
	}

	usage, err := db.UsageLog.Query().Only(ctx)
	if err != nil {
		t.Fatalf("query usage log: %v", err)
	}
	if usage.UserIDSnapshot != user.ID || usage.UserEmailSnapshot != user.Email {
		t.Fatalf("usage snapshot = (%d, %q), want (%d, %q)", usage.UserIDSnapshot, usage.UserEmailSnapshot, user.ID, user.Email)
	}
	if usage.ActualCost != 1.25 || usage.BilledCost != 2.50 {
		t.Fatalf("usage costs = actual %v billed %v, want 1.25/2.50", usage.ActualCost, usage.BilledCost)
	}
	if hasUser, err := usage.QueryUser().Exist(ctx); err != nil || hasUser {
		t.Fatalf("usage has user = %v, err = %v; want false", hasUser, err)
	}
	if hasKey, err := usage.QueryAPIKey().Exist(ctx); err != nil || hasKey {
		t.Fatalf("usage has api key = %v, err = %v; want false", hasKey, err)
	}

	balanceLog, err := db.BalanceLog.Query().Only(ctx)
	if err != nil {
		t.Fatalf("query balance log: %v", err)
	}
	if balanceLog.UserIDSnapshot != user.ID || balanceLog.UserEmailSnapshot != user.Email {
		t.Fatalf("balance snapshot = (%d, %q), want (%d, %q)", balanceLog.UserIDSnapshot, balanceLog.UserEmailSnapshot, user.ID, user.Email)
	}
	if hasUser, err := balanceLog.QueryUser().Exist(ctx); err != nil || hasUser {
		t.Fatalf("balance log has user = %v, err = %v; want false", hasUser, err)
	}
	balanceLogs, total, err := NewUserStore(db).ListBalanceLogs(ctx, user.ID, 1, 20)
	if err != nil {
		t.Fatalf("ListBalanceLogs returned error: %v", err)
	}
	if total != 1 || len(balanceLogs) != 1 || balanceLogs[0].Amount != 2.50 {
		t.Fatalf("balance logs = total %d list %+v, want deleted user billing history", total, balanceLogs)
	}

	stats, err := NewUsageStore(db).StatsByUser(ctx, appUsageStatsFilter())
	if err != nil {
		t.Fatalf("StatsByUser returned error: %v", err)
	}
	if len(stats) != 1 || stats[0].UserID != int64(user.ID) || stats[0].Email != user.Email || stats[0].BilledCost != 2.50 {
		t.Fatalf("stats = %+v, want deleted user billing preserved", stats)
	}
}

func createTestUser(t *testing.T, db *ent.Client, email string) *ent.User {
	t.Helper()
	user, err := db.User.Create().
		SetEmail(email).
		SetPasswordHash("hash").
		Save(context.Background())
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user
}

func appUsageStatsFilter() appusage.StatsFilter {
	return appusage.StatsFilter{}
}
