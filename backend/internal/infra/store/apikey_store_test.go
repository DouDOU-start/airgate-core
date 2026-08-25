package store

import (
	"context"
	"testing"

	appapikey "github.com/DouDOU-start/airgate-core/internal/app/apikey"
)

// TestAPIKeyStoreListAdminSearchScope 验证 search_scope 控制是否按用户邮箱模糊匹配。
//
// 业务背景：管理员通用搜索想同时支持 name/key_hint/user_email；但
// "Usage 页面通过 API Key 选择器搜索"这一场景里，邮箱模糊匹配会带回大量
// 同邮箱所属的其它 Key，造成噪音。前端在该场景下传 search_scope=api_key
// 让 store 跳过邮箱谓词。
func TestAPIKeyStoreListAdminSearchScope(t *testing.T) {
	db := enttestOpen(t)
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}
	}()
	ctx := context.Background()

	user := createTestUser(t, db, "scope-target@example.com")
	if _, err := db.APIKey.Create().
		SetName("billing-runner").
		SetKeyHint("sk-bill-001").
		SetKeyHash("hash-1").
		SetUserID(user.ID).
		Save(ctx); err != nil {
		t.Fatalf("create api key: %v", err)
	}

	store := NewAPIKeyStore(db)

	t.Run("default scope matches user email", func(t *testing.T) {
		_, total, err := store.ListAdmin(ctx, appapikey.ListFilter{
			Page: 1, PageSize: 20, Keyword: "scope-target",
		})
		if err != nil {
			t.Fatalf("ListAdmin returned error: %v", err)
		}
		if total != 1 {
			t.Fatalf("default scope total = %d, want 1 (email predicate must apply)", total)
		}
	})

	t.Run("api_key scope skips user email predicate", func(t *testing.T) {
		_, total, err := store.ListAdmin(ctx, appapikey.ListFilter{
			Page: 1, PageSize: 20, Keyword: "scope-target",
			SearchScope: appapikey.SearchScopeAPIKey,
		})
		if err != nil {
			t.Fatalf("ListAdmin returned error: %v", err)
		}
		if total != 0 {
			t.Fatalf("api_key scope total = %d, want 0 (email predicate must be skipped)", total)
		}
	})

	t.Run("api_key scope still matches name", func(t *testing.T) {
		_, total, err := store.ListAdmin(ctx, appapikey.ListFilter{
			Page: 1, PageSize: 20, Keyword: "billing",
			SearchScope: appapikey.SearchScopeAPIKey,
		})
		if err != nil {
			t.Fatalf("ListAdmin returned error: %v", err)
		}
		if total != 1 {
			t.Fatalf("api_key scope name match total = %d, want 1", total)
		}
	})

	t.Run("api_key scope still matches key_hint", func(t *testing.T) {
		_, total, err := store.ListAdmin(ctx, appapikey.ListFilter{
			Page: 1, PageSize: 20, Keyword: "sk-bill",
			SearchScope: appapikey.SearchScopeAPIKey,
		})
		if err != nil {
			t.Fatalf("ListAdmin returned error: %v", err)
		}
		if total != 1 {
			t.Fatalf("api_key scope key_hint match total = %d, want 1", total)
		}
	})
}
