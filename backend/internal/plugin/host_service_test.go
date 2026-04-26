package plugin

import (
	"context"
	"testing"

	"entgo.io/ent/dialect/sql/schema"
	_ "github.com/mattn/go-sqlite3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/DouDOU-start/airgate-core/ent/enttest"
)

func TestCheckHostForwardBalance(t *testing.T) {
	ctx := context.Background()
	db := enttest.Open(t, "sqlite3", "file:host_forward_balance?mode=memory&cache=shared&_fk=1", enttest.WithMigrateOptions(schema.WithGlobalUniqueID(false)))
	t.Cleanup(func() { _ = db.Close() })

	zeroBalanceUser := db.User.Create().SetEmail("zero@example.com").SetPasswordHash("hash").SetBalance(0).SaveX(ctx)
	positiveBalanceUser := db.User.Create().SetEmail("positive@example.com").SetPasswordHash("hash").SetBalance(1).SaveX(ctx)

	host := &HostService{db: db}

	if err := host.checkHostForwardBalance(ctx, int64(zeroBalanceUser.ID)); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("expected ResourceExhausted for zero balance, got %v", err)
	}
	if err := host.checkHostForwardBalance(ctx, int64(positiveBalanceUser.ID)); err != nil {
		t.Fatalf("expected positive balance user to pass, got %v", err)
	}
}
