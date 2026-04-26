package routing

import (
	"context"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/DouDOU-start/airgate-core/ent/enttest"
	"github.com/DouDOU-start/airgate-core/ent/migrate"
)

func TestListEligibleGroups(t *testing.T) {
	ctx := context.Background()
	db := enttest.Open(t, "sqlite3", "file:route_selector?mode=memory&cache=shared&_fk=1", enttest.WithMigrateOptions(migrate.WithGlobalUniqueID(false)))
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}
	})

	u := db.User.Create().
		SetEmail("user@example.com").
		SetPasswordHash("hash").
		SetGroupRates(map[int64]float64{}).
		SaveX(ctx)

	publicSlow := db.Group.Create().
		SetName("public slow").
		SetPlatform("openai").
		SetRateMultiplier(0.8).
		SetSortWeight(10).
		SaveX(ctx)
	allowedFast := db.Group.Create().
		SetName("allowed fast").
		SetPlatform("openai").
		SetRateMultiplier(0.4).
		SetIsExclusive(true).
		SetSortWeight(1).
		AddAllowedUsers(u).
		SaveX(ctx)
	db.Group.Create().
		SetName("denied fast").
		SetPlatform("openai").
		SetRateMultiplier(0.1).
		SetIsExclusive(true).
		SaveX(ctx)
	tieHighWeight := db.Group.Create().
		SetName("tie high weight").
		SetPlatform("openai").
		SetRateMultiplier(0.8).
		SetSortWeight(20).
		SaveX(ctx)
	db.Group.Create().
		SetName("other platform").
		SetPlatform("anthropic").
		SetRateMultiplier(0.01).
		SaveX(ctx)

	routes, err := ListEligibleGroups(ctx, db, u.ID, "openai", map[int64]float64{int64(publicSlow.ID): 0.3}, Requirements{})
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 3 {
		t.Fatalf("len(routes) = %d, want 3", len(routes))
	}

	wantIDs := []int{publicSlow.ID, allowedFast.ID, tieHighWeight.ID}
	for i, want := range wantIDs {
		if routes[i].GroupID != want {
			t.Fatalf("routes[%d].GroupID = %d, want %d", i, routes[i].GroupID, want)
		}
	}
	if routes[0].EffectiveRate != 0.3 {
		t.Fatalf("routes[0].EffectiveRate = %v, want 0.3", routes[0].EffectiveRate)
	}
}

func TestListEligibleGroupsFiltersImageDisabledOpenAIGroups(t *testing.T) {
	ctx := context.Background()
	db := enttest.Open(t, "sqlite3", "file:route_selector_image?mode=memory&cache=shared&_fk=1", enttest.WithMigrateOptions(migrate.WithGlobalUniqueID(false)))
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}
	})

	u := db.User.Create().
		SetEmail("image-user@example.com").
		SetPasswordHash("hash").
		SetGroupRates(map[int64]float64{}).
		SaveX(ctx)

	db.Group.Create().
		SetName("image disabled").
		SetPlatform("openai").
		SetRateMultiplier(0.1).
		SetPluginSettings(map[string]map[string]string{"openai": {"image_enabled": "false"}}).
		SaveX(ctx)
	imageEnabled := db.Group.Create().
		SetName("image enabled").
		SetPlatform("openai").
		SetRateMultiplier(0.2).
		SetPluginSettings(map[string]map[string]string{"openai": {"image_enabled": "true"}}).
		SaveX(ctx)
	db.Group.Create().
		SetName("chat only implicit").
		SetPlatform("openai").
		SetRateMultiplier(0.3).
		SaveX(ctx)

	routes, err := ListEligibleGroups(ctx, db, u.ID, "openai", nil, Requirements{NeedsImage: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 {
		t.Fatalf("len(routes) = %d, want 1", len(routes))
	}
	if routes[0].GroupID != imageEnabled.ID {
		t.Fatalf("routes[0].GroupID = %d, want %d", routes[0].GroupID, imageEnabled.ID)
	}
}
