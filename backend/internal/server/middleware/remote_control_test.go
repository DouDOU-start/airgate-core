package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/auth"
)

func TestCodexRemoteControlAuthAcceptsEnrolledBearer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := NewRemoteControlTokenStore()
	keyInfo := &auth.APIKeyInfo{KeyID: 7, UserID: 11, GroupID: 13}
	if err := store.Register(RemoteControlTokenRegistration{
		Token:          "rc-test-token",
		KeyInfo:        keyInfo,
		AccountID:      42,
		ServerID:       "server-1",
		EnvironmentID:  "env-1",
		Name:           "test",
		InstallationID: "install-1",
		ExpiresAt:      time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	engine := gin.New()
	engine.POST("/remote/control/server/pair", CodexRemoteControlAuth(nil, store), func(c *gin.Context) {
		if got := c.GetString(CtxKeyCodexRemoteControlToken); got != "rc-test-token" {
			t.Errorf("remote token context = %q, want %q", got, "rc-test-token")
		}
		if got := c.GetInt(CtxKeyCodexRemoteControlAccountID); got != 42 {
			t.Errorf("account id context = %d, want 42", got)
		}
		if got := c.GetString(CtxKeyCodexRemoteControlServerID); got != "server-1" {
			t.Errorf("server id context = %q, want %q", got, "server-1")
		}
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/remote/control/server/pair", nil)
	req.Header.Set("Authorization", "Bearer rc-test-token")
	resp := httptest.NewRecorder()
	engine.ServeHTTP(resp, req)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d (body=%s)", resp.Code, http.StatusNoContent, resp.Body.String())
	}
}

func TestCodexRemoteControlAuthRejectsUnknownBearer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	called := false
	engine.POST("/remote/control/server/enroll", CodexRemoteControlAuth(nil, NewRemoteControlTokenStore()), func(c *gin.Context) {
		called = true
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/remote/control/server/enroll", nil)
	req.Header.Set("Authorization", "Bearer unknown-remote-token")
	resp := httptest.NewRecorder()
	engine.ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized || called {
		t.Fatalf("status = %d, called=%v, want 401 and no handler", resp.Code, called)
	}
}

func TestCodexRemoteControlAuthUnknownOAuthBearerWithAirGateKeyFailsClosedWithoutDB(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	called := false
	engine.POST("/remote/control/server/refresh", CodexRemoteControlAuth(nil, NewRemoteControlTokenStore()), func(c *gin.Context) {
		called = true
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/remote/control/server/refresh", nil)
	req.Header.Set("Authorization", "Bearer chatgpt-oauth-token")
	req.Header.Set("X-API-Key", "sk-gateway-key")
	resp := httptest.NewRecorder()
	engine.ServeHTTP(resp, req)
	if resp.Code != http.StatusServiceUnavailable || called {
		t.Fatalf("status = %d, called=%v, want 503 and no handler", resp.Code, called)
	}
}

func TestCodexRemoteControlAuthRejectsUnknownExplicitTokenWithoutAPIKeyFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	called := false
	engine.POST("/remote/control/server/enroll", CodexRemoteControlAuth(nil, NewRemoteControlTokenStore()), func(c *gin.Context) {
		called = true
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/remote/control/server/enroll", nil)
	req.Header.Set("X-Codex-Remote-Control-Token", "not-enrolled")
	req.Header.Set("X-API-Key", "sk-gateway-key")
	resp := httptest.NewRecorder()
	engine.ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized || called {
		t.Fatalf("status = %d, called=%v, want 401 and no API-key fallback (body=%s)", resp.Code, called, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "invalid_remote_control_token") {
		t.Fatalf("body = %s, want invalid_remote_control_token", resp.Body.String())
	}
}

func TestCodexRemoteControlAuthRejectsEmptyExplicitTokenWithoutAPIKeyFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	called := false
	engine.POST("/remote/control/server/refresh", CodexRemoteControlAuth(nil, NewRemoteControlTokenStore()), func(c *gin.Context) {
		called = true
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/remote/control/server/refresh", nil)
	// Header.Set intentionally preserves an empty value in Request.Header;
	// presence must not be confused with absence by the middleware.
	req.Header.Set("X-Remote-Control-Token", "")
	req.Header.Set("X-API-Key", "sk-gateway-key")
	resp := httptest.NewRecorder()
	engine.ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized || called {
		t.Fatalf("status = %d, called=%v, want 401 and no API-key fallback (body=%s)", resp.Code, called, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "invalid_remote_control_token") {
		t.Fatalf("body = %s, want invalid_remote_control_token", resp.Body.String())
	}
}

func TestCodexRemoteControlAuthTreatsOAuthBearerAsAPIKeyOnEnroll(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := NewRemoteControlTokenStore()
	keyInfo := &auth.APIKeyInfo{KeyID: 7, UserID: 11, GroupID: 13}
	if err := store.Register(RemoteControlTokenRegistration{
		Token:         "oauth-shaped-token",
		KeyInfo:       keyInfo,
		AccountID:     42,
		ServerID:      "server-1",
		EnvironmentID: "env-1",
		ExpiresAt:     time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	engine := gin.New()
	called := false
	engine.POST("/remote/control/server/enroll", CodexRemoteControlAuth(nil, store), func(c *gin.Context) {
		called = true
		c.Status(http.StatusNoContent)
	})

	// Enroll's Authorization bearer is the upstream OAuth lease. Even if its
	// bytes happen to equal an enrolled token, the separately supplied AirGate
	// key remains the authentication path for this endpoint.
	req := httptest.NewRequest(http.MethodPost, "/remote/control/server/enroll", nil)
	req.Header.Set("Authorization", "Bearer oauth-shaped-token")
	req.Header.Set("X-API-Key", "sk-gateway-key")
	resp := httptest.NewRecorder()
	engine.ServeHTTP(resp, req)
	if resp.Code != http.StatusServiceUnavailable || called {
		t.Fatalf("status = %d, called=%v, want 503 from API-key path (body=%s)", resp.Code, called, resp.Body.String())
	}
	if strings.Contains(resp.Body.String(), "remote_control_binding") || strings.Contains(resp.Body.String(), "invalid_remote_control_token") {
		t.Fatalf("body = %s, OAuth bearer was misclassified as Remote Control", resp.Body.String())
	}
}

func TestCodexRemoteControlAuthAcceptsAuthorizationBearerOnWebSocketPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := NewRemoteControlTokenStore()
	if err := store.Register(RemoteControlTokenRegistration{
		Token:         "ws-remote-token",
		KeyInfo:       &auth.APIKeyInfo{KeyID: 7, UserID: 11, GroupID: 13},
		AccountID:     42,
		ServerID:      "server-1",
		EnvironmentID: "env-1",
		ExpiresAt:     time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	engine := gin.New()
	engine.GET("/remote/control/server", CodexRemoteControlAuth(nil, store), func(c *gin.Context) {
		if got := c.GetString(CtxKeyCodexRemoteControlToken); got != "ws-remote-token" {
			t.Errorf("token context = %q, want ws-remote-token", got)
		}
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/remote/control/server", nil)
	req.Header.Set("Authorization", "Bearer ws-remote-token")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	resp := httptest.NewRecorder()
	engine.ServeHTTP(resp, req)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body=%s)", resp.Code, resp.Body.String())
	}
}

func TestRemoteControlTokenStorePreservesOpaqueIdentityBytes(t *testing.T) {
	store := NewRemoteControlTokenStore()
	keyInfo := &auth.APIKeyInfo{KeyID: 17, UserID: 23, GroupID: 29}
	serverID := " server /?#% 节点 "
	environmentID := "环境 /?#% workspace "
	name := "显示 /?#% 名称"
	installationID := "安装 /?#% 标识"
	if err := store.Register(RemoteControlTokenRegistration{
		Token:          "opaque-id-token",
		KeyInfo:        keyInfo,
		AccountID:      41,
		ServerID:       serverID,
		EnvironmentID:  environmentID,
		Name:           name,
		InstallationID: installationID,
		ExpiresAt:      time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	record, ok := store.Lookup("opaque-id-token")
	if !ok {
		t.Fatal("Lookup() did not find opaque token")
	}
	if record.ServerID != serverID || record.EnvironmentID != environmentID ||
		record.Name != name || record.InstallationID != installationID {
		t.Fatalf("record identity changed: %+v", record)
	}
	found, ok := store.Find(serverID, environmentID, keyInfo, 41)
	if !ok {
		t.Fatal("Find() did not resolve opaque identity")
	}
	if found.ServerID != serverID || found.EnvironmentID != environmentID || found.AccountID != 41 {
		t.Fatalf("Find() record = %+v", found)
	}
}

func TestRemoteControlTokenStoreRejectsUnsafeOpaqueMetadata(t *testing.T) {
	base := RemoteControlTokenRegistration{
		Token:         "opaque-id-token",
		KeyInfo:       &auth.APIKeyInfo{KeyID: 17, UserID: 23, GroupID: 29},
		AccountID:     41,
		ServerID:      "server-id",
		EnvironmentID: "environment-id",
		ExpiresAt:     time.Now().Add(time.Minute),
	}
	tests := []struct {
		name string
		set  func(*RemoteControlTokenRegistration)
	}{
		{"empty server id", func(reg *RemoteControlTokenRegistration) { reg.ServerID = "" }},
		{"blank server id", func(reg *RemoteControlTokenRegistration) { reg.ServerID = "   " }},
		{"dot server id", func(reg *RemoteControlTokenRegistration) { reg.ServerID = "." }},
		{"dot dot server id", func(reg *RemoteControlTokenRegistration) { reg.ServerID = ".." }},
		{"control server id", func(reg *RemoteControlTokenRegistration) { reg.ServerID = "server\x00id" }},
		{"del server id", func(reg *RemoteControlTokenRegistration) { reg.ServerID = "server\x7fid" }},
		{"backslash server id", func(reg *RemoteControlTokenRegistration) { reg.ServerID = `server\\id` }},
		{"invalid utf8 server id", func(reg *RemoteControlTokenRegistration) { reg.ServerID = string([]byte{0xff}) }},
		{"oversized server id", func(reg *RemoteControlTokenRegistration) { reg.ServerID = strings.Repeat("s", 513) }},
		{"empty environment id", func(reg *RemoteControlTokenRegistration) { reg.EnvironmentID = "" }},
		{"blank environment id", func(reg *RemoteControlTokenRegistration) { reg.EnvironmentID = "\u2003" }},
		{"dot environment id", func(reg *RemoteControlTokenRegistration) { reg.EnvironmentID = "." }},
		{"dot dot environment id", func(reg *RemoteControlTokenRegistration) { reg.EnvironmentID = ".." }},
		{"control environment id", func(reg *RemoteControlTokenRegistration) { reg.EnvironmentID = "env\n id" }},
		{"backslash environment id", func(reg *RemoteControlTokenRegistration) { reg.EnvironmentID = `env\\id` }},
		{"invalid utf8 environment id", func(reg *RemoteControlTokenRegistration) { reg.EnvironmentID = string([]byte{0xfe, 0xff}) }},
		{"oversized environment id", func(reg *RemoteControlTokenRegistration) { reg.EnvironmentID = strings.Repeat("e", 513) }},
		{"control optional name", func(reg *RemoteControlTokenRegistration) { reg.Name = "name\x00" }},
		{"control optional installation id", func(reg *RemoteControlTokenRegistration) { reg.InstallationID = "install\x7f" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := base
			tt.set(&reg)
			if err := NewRemoteControlTokenStore().Register(reg); err == nil {
				t.Fatalf("Register() accepted unsafe metadata: %+v", reg)
			}
		})
	}
}
