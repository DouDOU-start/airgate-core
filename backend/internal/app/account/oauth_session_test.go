package account

import (
	"net/url"
	"sync"
	"testing"
	"time"
)

func TestAntigravityOAuthRequiresEnvironmentCredentials(t *testing.T) {
	t.Setenv(antigravityOAuthClientIDEnv, "")
	t.Setenv(antigravityOAuthClientSecretEnv, "")

	entry := &oauthSessionEntry{}
	err := (&Service{}).startAntigravityOAuth(entry)
	if err == nil {
		t.Fatal("未配置 Antigravity OAuth 环境变量时应返回错误")
	}
}

func TestAntigravityOAuthUsesEnvironmentClientID(t *testing.T) {
	t.Setenv(antigravityOAuthClientIDEnv, "test-client-id")
	t.Setenv(antigravityOAuthClientSecretEnv, "test-client-secret")

	entry := &oauthSessionEntry{}
	if err := (&Service{}).startAntigravityOAuth(entry); err != nil {
		t.Fatalf("startAntigravityOAuth() 错误: %v", err)
	}
	parsed, err := url.Parse(entry.public.AuthorizeURL)
	if err != nil {
		t.Fatalf("解析授权地址失败: %v", err)
	}
	if got := parsed.Query().Get("client_id"); got != "test-client-id" {
		t.Fatalf("client_id = %q，期望 test-client-id", got)
	}
}

func TestOAuthSessionTerminalStateCannotBeOverwritten(t *testing.T) {
	entry := &oauthSessionEntry{
		public: OAuthSession{ID: "session", Status: OAuthStatusPending, CreatedAt: time.Now()},
		done:   make(chan struct{}),
	}
	store := &oauthSessionStore{sessions: map[string]*oauthSessionEntry{"session": entry}}

	store.complete("session", 42, "测试账号", false)
	store.fail("session", "迟到的失败结果")

	got := entry.snapshot()
	if got.Status != OAuthStatusCompleted || got.AccountID != 42 {
		t.Fatalf("完成状态被覆盖：%+v", got)
	}
}

func TestOAuthSessionConcurrentTerminalTransitionsAreSafe(t *testing.T) {
	entry := &oauthSessionEntry{
		public: OAuthSession{ID: "session", Status: OAuthStatusPending, CreatedAt: time.Now()},
		done:   make(chan struct{}),
	}
	store := &oauthSessionStore{sessions: map[string]*oauthSessionEntry{"session": entry}}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			store.complete("session", 1, "账号", false)
		}()
		go func() {
			defer wg.Done()
			store.fail("session", "失败")
		}()
	}
	wg.Wait()

	got := entry.snapshot()
	if got.Status != OAuthStatusCompleted && got.Status != OAuthStatusFailed {
		t.Fatalf("会话未进入终态：%+v", got)
	}
	select {
	case <-entry.done:
	default:
		t.Fatal("终态会话必须关闭完成信号")
	}
}
