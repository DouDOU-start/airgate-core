package cpa

import (
	"sync"
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestMapAuthXAIOAuth补齐认证类型(t *testing.T) {
	auth, err := MapAuth(AccountAuthInput{
		AccountID: 1,
		Name:      "xAI 订阅账号",
		Platform:  "xai",
		Type:      "oauth",
		Credentials: map[string]string{
			"access_token":      "test-token",
			"credential_origin": "oauth",
		},
	})
	if err != nil {
		t.Fatalf("MapAuth() 错误: %v", err)
	}
	if got := metadataToString(auth.Metadata["auth_kind"]); got != "oauth" {
		t.Fatalf("auth_kind = %q，期望 oauth", got)
	}
}

func TestMapAuthXAIAPIKey不伪装成OAuth(t *testing.T) {
	auth, err := MapAuth(AccountAuthInput{
		AccountID: 2,
		Name:      "xAI API Key",
		Platform:  "xai",
		Type:      "api_key",
		Credentials: map[string]string{
			"api_key": "xai-test-key",
		},
	})
	if err != nil {
		t.Fatalf("MapAuth() 错误: %v", err)
	}
	if got := metadataToString(auth.Metadata["auth_kind"]); got != "" {
		t.Fatalf("API Key 账号不应补 auth_kind，实际为 %q", got)
	}
}

func TestMapAuthCached复用未变化账号(t *testing.T) {
	bridge := &Bridge{}
	input := AccountAuthInput{
		AccountID: 3,
		Name:      "Codex 账号",
		Platform:  "codex",
		Type:      "oauth",
		Credentials: map[string]string{
			"access_token":  "访问令牌",
			"refresh_token": "刷新令牌",
			"account_id":    "上游账号",
		},
	}
	first, err := bridge.mapAuthCached(input)
	if err != nil {
		t.Fatalf("首次映射失败: %v", err)
	}
	second, err := bridge.mapAuthCached(input)
	if err != nil {
		t.Fatalf("缓存映射失败: %v", err)
	}
	if first != second {
		t.Fatal("未变化账号应复用同一个只读 Auth")
	}
}

func TestMapAuthCached凭证变化自动失效(t *testing.T) {
	bridge := &Bridge{}
	firstInput := AccountAuthInput{
		AccountID: 4,
		Platform:  "codex",
		Type:      "oauth",
		Credentials: map[string]string{
			"access_token": "旧令牌",
		},
	}
	first, err := bridge.mapAuthCached(firstInput)
	if err != nil {
		t.Fatalf("首次映射失败: %v", err)
	}
	secondInput := firstInput
	secondInput.Credentials = map[string]string{"access_token": "新令牌"}
	second, err := bridge.mapAuthCached(secondInput)
	if err != nil {
		t.Fatalf("更新凭证后映射失败: %v", err)
	}
	if first == second {
		t.Fatal("凭证变化后不应继续复用旧 Auth")
	}
	if got := metadataToString(second.Metadata["access_token"]); got != "新令牌" {
		t.Fatalf("更新后的 access_token = %q，期望新令牌", got)
	}
}

func TestMapAuthCached不复用可能改写Auth的平台(t *testing.T) {
	bridge := &Bridge{}
	input := AccountAuthInput{
		AccountID:   7,
		Platform:    "kimi",
		Type:        "oauth",
		Credentials: map[string]string{"access_token": "访问令牌"},
	}
	first, err := bridge.mapAuthCached(input)
	if err != nil {
		t.Fatalf("首次映射失败: %v", err)
	}
	second, err := bridge.mapAuthCached(input)
	if err != nil {
		t.Fatalf("再次映射失败: %v", err)
	}
	if first == second {
		t.Fatal("可能就地改写 Auth 的平台不应跨请求复用对象")
	}
}

func TestMappedAuth缓存主动刷新截止时间(t *testing.T) {
	now := time.Date(2026, 8, 15, 8, 0, 0, 0, time.UTC)
	entry := newMappedAuthEntry(AccountAuthInput{}, &coreauth.Auth{
		Provider: "xai",
		Metadata: map[string]any{
			"access_token":  "访问令牌",
			"refresh_token": "刷新令牌",
			"expired":       now.Add(4 * time.Minute).Format(time.RFC3339),
		},
	})
	if !entry.needsProactiveRefresh(now) {
		t.Fatal("距过期不足提前量时应触发主动刷新")
	}
	if entry.needsProactiveRefresh(now.Add(-2 * time.Minute)) {
		t.Fatal("尚未到达主动刷新截止时间时不应刷新")
	}
}

func TestMapAuthCached并发读取(t *testing.T) {
	bridge := &Bridge{}
	input := AccountAuthInput{
		AccountID: 5,
		Platform:  "codex",
		Type:      "oauth",
		Credentials: map[string]string{
			"access_token":  "访问令牌",
			"refresh_token": "刷新令牌",
		},
	}
	const workers = 32
	type result struct {
		auth *coreauth.Auth
		err  error
	}
	results := make(chan result, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			auth, err := bridge.mapAuthCached(input)
			results <- result{auth: auth, err: err}
		}()
	}
	wait.Wait()
	close(results)
	for result := range results {
		if result.err != nil {
			t.Fatalf("并发映射失败: %v", result.err)
		}
		if result.auth == nil || metadataToString(result.auth.Metadata["access_token"]) != "访问令牌" {
			t.Fatalf("并发映射结果错误: %+v", result.auth)
		}
	}
}

func TestResolveProviderDoesNotAliasOpenAIToCodex(t *testing.T) {
	for _, test := range []struct {
		platform string
		want     string
	}{
		{platform: "openai", want: "openai"},
		{platform: "OPENAI", want: "openai"},
		{platform: "openai_codex", want: "openai-codex"},
		{platform: "openai-codex", want: "openai-codex"},
	} {
		if got := ResolveProvider(test.platform); got != test.want {
			t.Fatalf("ResolveProvider(%q) = %q, want %q", test.platform, got, test.want)
		}
	}
	auth, err := MapAuth(AccountAuthInput{
		AccountID:   99,
		Platform:    "openai",
		Type:        "oauth",
		Credentials: map[string]string{"access_token": "token"},
	})
	if err != nil {
		t.Fatalf("MapAuth(openai) error: %v", err)
	}
	if auth.Provider != "openai" {
		t.Fatalf("MapAuth(openai) provider = %q, want openai", auth.Provider)
	}
}

func BenchmarkMapAuth(b *testing.B) {
	input := AccountAuthInput{
		AccountID: 6,
		Name:      "Codex 生产账号",
		Platform:  "codex",
		Type:      "oauth",
		ProxyURL:  "http://127.0.0.1:8080",
		Credentials: map[string]string{
			"access_token":      "访问令牌",
			"refresh_token":     "刷新令牌",
			"account_id":        "上游账号",
			"email":             "user@example.com",
			"expired":           "2026-08-15T12:00:00Z",
			"credential_origin": "oauth",
		},
	}
	b.Run("每次重建", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := MapAuth(input); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("缓存命中", func(b *testing.B) {
		bridge := &Bridge{}
		if _, err := bridge.mapAuthCached(input); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			if _, err := bridge.mapAuthCached(input); err != nil {
				b.Fatal(err)
			}
		}
	})
}
