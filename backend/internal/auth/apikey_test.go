package auth

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// resetAPIKeyLocalCache 清空本地 API Key 缓存与负缓存计数（测试隔离用）。
func resetAPIKeyLocalCache() {
	apiKeyCache.Range(func(k, _ any) bool {
		apiKeyCache.Delete(k)
		return true
	})
	negativeCacheCount.Store(0)
}

// TestNegativeLocalCacheBounded 本地负缓存有界行为：达到上限后新负条目不再写入、
// 正条目不受影响；负条目被覆盖/驱逐时释放名额，名额可被复用。
func TestNegativeLocalCacheBounded(t *testing.T) {
	resetAPIKeyLocalCache()
	t.Cleanup(resetAPIKeyLocalCache)

	// 填满上限
	for i := 0; i < maxNegativeCacheEntries; i++ {
		storeAPIKeyLocalCache(fmt.Sprintf("neg-%d", i), nil, ErrInvalidAPIKey)
	}
	if n := negativeCacheCount.Load(); n != maxNegativeCacheEntries {
		t.Fatalf("负缓存计数 = %d, want %d", n, maxNegativeCacheEntries)
	}

	// 超限后新负条目不缓存
	storeAPIKeyLocalCache("neg-overflow", nil, ErrInvalidAPIKey)
	if _, ok := apiKeyCache.Load("neg-overflow"); ok {
		t.Fatal("达到上限后新负条目不应写入本地缓存")
	}
	if n := negativeCacheCount.Load(); n != maxNegativeCacheEntries {
		t.Fatalf("超限写入不应改变计数: %d", n)
	}

	// 正条目不受上限影响
	storeAPIKeyLocalCache("pos-1", &APIKeyInfo{KeyID: 1}, nil)
	if _, ok := apiKeyCache.Load("pos-1"); !ok {
		t.Fatal("正条目不应受负缓存上限影响")
	}

	// 负条目被正条目覆盖时释放名额
	storeAPIKeyLocalCache("neg-0", &APIKeyInfo{KeyID: 2}, nil)
	if n := negativeCacheCount.Load(); n != maxNegativeCacheEntries-1 {
		t.Fatalf("负条目被正条目覆盖后计数 = %d, want %d", n, maxNegativeCacheEntries-1)
	}

	// 释放出的名额可再次写入负条目
	storeAPIKeyLocalCache("neg-new", nil, ErrAPIKeyExpired)
	cached, ok := apiKeyCache.Load("neg-new")
	if !ok {
		t.Fatal("释放名额后新负条目应可写入")
	}
	if e := cached.(apiKeyCacheEntry); !errors.Is(e.err, ErrAPIKeyExpired) {
		t.Fatalf("负条目 err = %v, want ErrAPIKeyExpired", e.err)
	}

	// 同一 hash 的负条目覆盖写入不重复占名额
	storeAPIKeyLocalCache("neg-new", nil, ErrInvalidAPIKey)
	if n := negativeCacheCount.Load(); n != maxNegativeCacheEntries {
		t.Fatalf("负→负覆盖后计数 = %d, want %d", n, maxNegativeCacheEntries)
	}

	// 过期驱逐路径递减计数
	evictAPIKeyLocalCache("neg-new")
	if _, ok := apiKeyCache.Load("neg-new"); ok {
		t.Fatal("驱逐后条目应被删除")
	}
	if n := negativeCacheCount.Load(); n != maxNegativeCacheEntries-1 {
		t.Fatalf("驱逐负条目后计数 = %d, want %d", n, maxNegativeCacheEntries-1)
	}

	// 驱逐正条目不影响负缓存计数
	evictAPIKeyLocalCache("pos-1")
	if n := negativeCacheCount.Load(); n != maxNegativeCacheEntries-1 {
		t.Fatalf("驱逐正条目不应改变负缓存计数: %d", n)
	}
}

func TestGenerateAPIKeyPrefixesAndHashes(t *testing.T) {
	t.Parallel()

	key, hash, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey 失败: %v", err)
	}
	if !strings.HasPrefix(key, apiKeyPrefix) {
		t.Fatalf("API key 前缀 = %q, 期望 %q", key[:len(apiKeyPrefix)], apiKeyPrefix)
	}
	if hash != HashAPIKey(key) {
		t.Fatalf("哈希 = %q, 期望与 HashAPIKey(key) 一致", hash)
	}

	adminKey, adminHash, err := GenerateAdminAPIKey()
	if err != nil {
		t.Fatalf("GenerateAdminAPIKey 失败: %v", err)
	}
	if !strings.HasPrefix(adminKey, adminKeyPrefix) {
		t.Fatalf("管理员 key 前缀 = %q, 期望 %q", adminKey[:len(adminKeyPrefix)], adminKeyPrefix)
	}
	if adminHash != HashAPIKey(adminKey) {
		t.Fatalf("管理员哈希 = %q, 期望与 HashAPIKey(adminKey) 一致", adminHash)
	}
}
