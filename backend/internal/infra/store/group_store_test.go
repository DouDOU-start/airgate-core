package store

import (
	"context"
	"testing"

	appgroup "github.com/DouDOU-start/airgate-core/internal/app/group"
)

// TestGroupStoreListAvailableExclusiveAfterBinding 覆盖核心场景：用户绑定分组时
// 该分组还不是专属，管理员事后才把它设为专属且未把该用户加入白名单。
// 专属分组通常对应私下谈价的定价，管理员收紧白名单的意图就是不想再让这个用户
// 看到分组名/倍率——所以分组必须从该用户的可见列表里彻底消失，而不是留一条
// "受限"的行继续暴露名称/倍率。密钥管理页对这种已经查不到分组的密钥，只按
// "分组已失效，请重新绑定"这种不透传具体信息的通用提示处理（前端逻辑）。
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

	t.Run("group not exclusive yet: listed", func(t *testing.T) {
		list, total, err := store.ListAvailable(ctx, appgroup.AvailableFilter{UserID: user.ID, Page: 1, PageSize: 20})
		if err != nil {
			t.Fatalf("ListAvailable returned error: %v", err)
		}
		if total != 1 || len(list) != 1 {
			t.Fatalf("total/len = %d/%d, want 1/1", total, len(list))
		}
	})

	if _, err := group.Update().SetIsExclusive(true).Save(ctx); err != nil {
		t.Fatalf("set group exclusive: %v", err)
	}

	t.Run("group turned exclusive, user not whitelisted: disappears despite existing key binding", func(t *testing.T) {
		list, total, err := store.ListAvailable(ctx, appgroup.AvailableFilter{UserID: user.ID, Page: 1, PageSize: 20})
		if err != nil {
			t.Fatalf("ListAvailable returned error: %v", err)
		}
		if total != 0 || len(list) != 0 {
			t.Fatalf("total/len = %d/%d, want 0/0 (name/rate must not leak once whitelist is revoked)", total, len(list))
		}
	})

	t.Run("other user with no bound key never sees the exclusive group either", func(t *testing.T) {
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

	t.Run("user whitelisted afterwards: listed again", func(t *testing.T) {
		list, total, err := store.ListAvailable(ctx, appgroup.AvailableFilter{UserID: user.ID, Page: 1, PageSize: 20})
		if err != nil {
			t.Fatalf("ListAvailable returned error: %v", err)
		}
		if total != 1 || len(list) != 1 {
			t.Fatalf("total/len = %d/%d, want 1/1", total, len(list))
		}
	})
}
