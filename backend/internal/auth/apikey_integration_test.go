package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"entgo.io/ent/dialect/sql/schema"
	_ "github.com/mattn/go-sqlite3"

	"github.com/DouDOU-start/airgate-core/ent/enttest"
)

func TestValidateAPIKeyIncludesUserEmail(t *testing.T) {
	db := enttest.Open(t, "sqlite3", "file:apikey_validate?mode=memory&cache=shared&_fk=1", enttest.WithMigrateOptions(schema.WithGlobalUniqueID(false)))
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("关闭数据库失败: %v", err)
		}
	}()

	ctx := context.Background()
	user, err := db.User.Create().
		SetEmail("apikey-user@example.com").
		SetPasswordHash("secret").
		Save(ctx)
	if err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}
	group, err := db.Group.Create().
		SetName("OpenAI").
		SetPlatform("openai").
		Save(ctx)
	if err != nil {
		t.Fatalf("创建分组失败: %v", err)
	}

	key, hash, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey 失败: %v", err)
	}
	if _, err := db.APIKey.Create().
		SetName("key").
		SetKeyHash(hash).
		SetUser(user).
		SetGroup(group).
		Save(ctx); err != nil {
		t.Fatalf("创建 API Key 失败: %v", err)
	}

	info, err := ValidateAPIKey(ctx, db, key)
	if err != nil {
		t.Fatalf("ValidateAPIKey 返回错误: %v", err)
	}
	if info.UserID != user.ID || info.UserEmail != user.Email {
		t.Fatalf("ValidateAPIKey 用户信息 = (%d, %q), 期望 (%d, %q)", info.UserID, info.UserEmail, user.ID, user.Email)
	}
}

// TestValidateAPIKeyNegativeResultCachedLocally 不存在的 key 的负结果进有界本地缓存：
// Redis 故障窗口内被拒 key 的重试不直穿 DB（上限行为见 TestNegativeLocalCacheBounded）。
func TestValidateAPIKeyNegativeResultCachedLocally(t *testing.T) {
	resetAPIKeyLocalCache()
	t.Cleanup(resetAPIKeyLocalCache)

	db := enttest.Open(t, "sqlite3", "file:apikey_negcache?mode=memory&cache=shared&_fk=1", enttest.WithMigrateOptions(schema.WithGlobalUniqueID(false)))
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("关闭数据库失败: %v", err)
		}
	}()
	ctx := context.Background()

	bogus := "sk-" + strings.Repeat("ab", 32)
	if _, err := ValidateAPIKey(ctx, db, bogus); !errors.Is(err, ErrInvalidAPIKey) {
		t.Fatalf("不存在的 key 期望 ErrInvalidAPIKey，实际 %v", err)
	}
	cached, ok := apiKeyCache.Load(HashAPIKey(bogus))
	if !ok {
		t.Fatal("负结果应写入有界本地缓存（Redis 故障窗口内不直穿 DB）")
	}
	if e := cached.(apiKeyCacheEntry); e.info != nil || !errors.Is(e.err, ErrInvalidAPIKey) {
		t.Fatalf("负条目形态异常: info=%v err=%v", e.info, e.err)
	}
	if n := negativeCacheCount.Load(); n != 1 {
		t.Fatalf("负缓存计数 = %d, want 1", n)
	}

	// 成功结果照常进本地缓存（热路径命中语义不变）。
	user, err := db.User.Create().
		SetEmail("negcache-user@example.com").
		SetPasswordHash("secret").
		Save(ctx)
	if err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}
	group, err := db.Group.Create().
		SetName("OpenAI").
		SetPlatform("openai").
		Save(ctx)
	if err != nil {
		t.Fatalf("创建分组失败: %v", err)
	}
	key, hash, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey 失败: %v", err)
	}
	if _, err := db.APIKey.Create().
		SetName("key").
		SetKeyHash(hash).
		SetUser(user).
		SetGroup(group).
		Save(ctx); err != nil {
		t.Fatalf("创建 API Key 失败: %v", err)
	}
	if _, err := ValidateAPIKey(ctx, db, key); err != nil {
		t.Fatalf("ValidateAPIKey 返回错误: %v", err)
	}
	if _, ok := apiKeyCache.Load(hash); !ok {
		t.Fatal("成功结果应写入本地缓存")
	}
}
