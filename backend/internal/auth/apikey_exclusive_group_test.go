package auth

import (
	"context"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/DouDOU-start/airgate-core/ent/enttest"
	"github.com/DouDOU-start/airgate-core/ent/migrate"
)

// TestValidateAPIKeyExclusiveGroup 覆盖请求时校验：密钥绑定分组时该分组还不是
// 专属，管理员事后设为专属且未把密钥归属用户加入白名单——已签发的密钥必须
// 立即被拒绝，不能"创建时校验过就一直放行"。
func TestValidateAPIKeyExclusiveGroup(t *testing.T) {
	resetAPIKeyLocalCache()
	t.Cleanup(resetAPIKeyLocalCache)

	db := enttest.Open(t, "sqlite3", "file:apikey_exclusive_test?mode=memory&cache=shared&_fk=1",
		enttest.WithMigrateOptions(migrate.WithGlobalUniqueID(false)))
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}
	}()
	ctx := context.Background()

	user, err := db.User.Create().SetEmail("bound@example.com").SetPasswordHash("hash").Save(ctx)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	group, err := db.Group.Create().SetName("g1").SetIsExclusive(false).Save(ctx)
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	key, hash, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("generate api key: %v", err)
	}
	if _, err := db.APIKey.Create().
		SetName("k1").
		SetKeyHint("sk-k1").
		SetKeyHash(hash).
		SetUserID(user.ID).
		SetGroupID(group.ID).
		Save(ctx); err != nil {
		t.Fatalf("create api key: %v", err)
	}

	if info, err := ValidateAPIKey(ctx, db, key); err != nil {
		t.Fatalf("validate before exclusive: err=%v", err)
	} else if info.GroupID != group.ID {
		t.Fatalf("group id = %d, want %d", info.GroupID, group.ID)
	}

	resetAPIKeyLocalCache()
	if _, err := group.Update().SetIsExclusive(true).Save(ctx); err != nil {
		t.Fatalf("set group exclusive: %v", err)
	}

	if _, err := ValidateAPIKey(ctx, db, key); err != ErrAPIKeyGroupExclusive {
		t.Fatalf("validate after exclusive = %v, want ErrAPIKeyGroupExclusive", err)
	}

	resetAPIKeyLocalCache()
	if err := db.Group.UpdateOne(group).AddAllowedUserIDs(user.ID).Exec(ctx); err != nil {
		t.Fatalf("whitelist user: %v", err)
	}
	if info, err := ValidateAPIKey(ctx, db, key); err != nil {
		t.Fatalf("validate after whitelisting: err=%v", err)
	} else if info.GroupID != group.ID {
		t.Fatalf("group id = %d, want %d", info.GroupID, group.ID)
	}
}
