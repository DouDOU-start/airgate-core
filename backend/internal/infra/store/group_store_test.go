package store

import (
	"context"
	"testing"

	appgroup "github.com/DouDOU-start/airgate-core/internal/app/group"
)

// TestGroupStoreListAvailableExclusiveAfterBinding 覆盖核心场景：用户绑定分组时
// 该分组还不是专属，管理员事后才把它设为专属且未把该用户加入白名单。
// 分组应仍出现在列表里（供密钥管理页展示名称/受限提示），但 Accessible 必须为
// false，前端据此把它从"可选新建分组"里排除。
func TestGroupStoreListAvailableExclusiveAfterBinding(t *testing.T) {
	db := enttestOpen(t)
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}
	}()
	ctx := context.Background()

	user := createTestUser(t, db, "bound-user@example.com")
	other := createTestUser(t, db, "other-user@example.com")

	group, err := db.Group.Create().SetName("g1").SetIsExclusive(false).Save(ctx)
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	if _, err := db.APIKey.Create().
		SetName("k1").
		SetKeyHint("sk-k1").
		SetKeyHash("hash-k1").
		SetUserID(user.ID).
		SetGroupID(group.ID).
		Save(ctx); err != nil {
		t.Fatalf("create api key: %v", err)
	}

	store := NewGroupStore(db)

	t.Run("group not exclusive yet: accessible", func(t *testing.T) {
		list, total, err := store.ListAvailable(ctx, appgroup.AvailableFilter{UserID: user.ID, Page: 1, PageSize: 20})
		if err != nil {
			t.Fatalf("ListAvailable returned error: %v", err)
		}
		if total != 1 || len(list) != 1 {
			t.Fatalf("total/len = %d/%d, want 1/1", total, len(list))
		}
		if !list[0].Accessible {
			t.Fatalf("Accessible = false, want true (group is not exclusive)")
		}
	})

	if _, err := group.Update().SetIsExclusive(true).Save(ctx); err != nil {
		t.Fatalf("set group exclusive: %v", err)
	}

	t.Run("group turned exclusive, user not whitelisted: still listed but restricted", func(t *testing.T) {
		list, total, err := store.ListAvailable(ctx, appgroup.AvailableFilter{UserID: user.ID, Page: 1, PageSize: 20})
		if err != nil {
			t.Fatalf("ListAvailable returned error: %v", err)
		}
		if total != 1 || len(list) != 1 {
			t.Fatalf("total/len = %d/%d, want 1/1 (group must remain visible for key-management display)", total, len(list))
		}
		if list[0].Accessible {
			t.Fatalf("Accessible = true, want false (user was never added to the allowed-users whitelist)")
		}
	})

	t.Run("other user with no bound key never sees the exclusive group", func(t *testing.T) {
		list, total, err := store.ListAvailable(ctx, appgroup.AvailableFilter{UserID: other.ID, Page: 1, PageSize: 20})
		if err != nil {
			t.Fatalf("ListAvailable returned error: %v", err)
		}
		if total != 0 || len(list) != 0 {
			t.Fatalf("total/len = %d/%d, want 0/0", total, len(list))
		}
	})

	if err := db.Group.UpdateOne(group).AddAllowedUserIDs(user.ID).Exec(ctx); err != nil {
		t.Fatalf("whitelist user: %v", err)
	}

	t.Run("user whitelisted afterwards: accessible again", func(t *testing.T) {
		list, total, err := store.ListAvailable(ctx, appgroup.AvailableFilter{UserID: user.ID, Page: 1, PageSize: 20})
		if err != nil {
			t.Fatalf("ListAvailable returned error: %v", err)
		}
		if total != 1 || len(list) != 1 {
			t.Fatalf("total/len = %d/%d, want 1/1", total, len(list))
		}
		if !list[0].Accessible {
			t.Fatalf("Accessible = false, want true (user is now whitelisted)")
		}
	})
}
