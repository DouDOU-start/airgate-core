package plugin

import (
	"context"
	"testing"
	"time"

	"entgo.io/ent/dialect/sql/schema"
	_ "github.com/mattn/go-sqlite3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/DouDOU-start/airgate-core/ent/enttest"
	pb "github.com/DouDOU-start/airgate-sdk/proto"
)

func TestHostForwardTimeout(t *testing.T) {
	cases := []struct {
		name string
		req  *pb.HostForwardRequest
		want time.Duration
	}{
		{name: "nil request", req: nil, want: defaultHostForwardTimeout},
		{name: "chat request", req: &pb.HostForwardRequest{Path: "/v1/chat/completions", Model: "gpt-4o"}, want: defaultHostForwardTimeout},
		{name: "images API request", req: &pb.HostForwardRequest{Path: "/v1/images/generations", Model: "gpt-4o"}, want: imageHostForwardTimeout},
		{name: "image model request", req: &pb.HostForwardRequest{Path: "/v1/responses", Model: "gpt-image-2"}, want: imageHostForwardTimeout},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hostForwardTimeout(tc.req); got != tc.want {
				t.Fatalf("hostForwardTimeout() = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestHostForwardReasoningEffort(t *testing.T) {
	t.Parallel()

	req := &pb.HostForwardRequest{
		Body: []byte(`{"model":"gpt-5","reasoning":{"effort":"x-high"}}`),
		Headers: map[string]*pb.HeaderValues{
			"Content-Type": {Values: []string{"application/json"}},
		},
	}

	if got := hostForwardReasoningEffort(req); got != "xhigh" {
		t.Fatalf("hostForwardReasoningEffort() = %q, want %q", got, "xhigh")
	}
}

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
