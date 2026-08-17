package account

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

func TestAntigravityOAuthUsesBuiltInClientID(t *testing.T) {
	entry := &oauthSessionEntry{}
	if err := (&Service{}).startAntigravityOAuth(entry); err != nil {
		t.Fatalf("使用内置 Antigravity OAuth 凭据生成授权链接失败: %v", err)
	}
	parsed, err := url.Parse(entry.public.AuthorizeURL)
	if err != nil {
		t.Fatalf("解析授权地址失败: %v", err)
	}
	if got := parsed.Query().Get("client_id"); got != antigravityOAuthClientID() {
		t.Fatalf("client_id = %q，期望内置客户端 ID", got)
	}
	if antigravityOAuthClientSecret() == "" {
		t.Fatal("内置 Antigravity OAuth 客户端密钥不能为空")
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

func TestPollXAIToken写入OAuth转发元数据(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("解析表单失败: %v", err)
		}
		if got := r.Form.Get("grant_type"); got != "urn:ietf:params:oauth:grant-type:device_code" {
			t.Fatalf("grant_type = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-token",
			"refresh_token": "refresh-token",
			"token_type":    "Bearer",
			"expires_in":    21600,
		})
	}))
	defer server.Close()

	creds, pending, err := pollXAIToken(context.Background(), "device-code", server.URL, "")
	if err != nil {
		t.Fatalf("pollXAIToken() 错误: %v", err)
	}
	if pending {
		t.Fatal("成功换票后不应继续等待")
	}
	if creds["auth_kind"] != TypeOAuth || creds["credential_origin"] != TypeOAuth {
		t.Fatalf("OAuth 元数据不完整: %#v", creds)
	}
	if creds["type"] != "xai" || creds["token_endpoint"] != server.URL {
		t.Fatalf("xAI 元数据不完整: %#v", creds)
	}
	if creds["token_type"] != "Bearer" || creds["expires_in"] != "21600" || creds["expired"] == "" {
		t.Fatalf("Token 有效期元数据不完整: %#v", creds)
	}
}
