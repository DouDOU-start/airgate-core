package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/config"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
)

// A helper-level route test cannot detect collisions created by overlapping
// groups in registerRoutes. Constructing the full server makes Gin validate
// the complete method/path tree and regressions such as two GET /v1/usage
// registrations fail immediately during startup.
func TestNewServerRegistersFullRouteTreeWithoutConflicts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{
		Server: config.ServerConfig{Mode: "test"},
		JWT:    config.JWTConfig{Secret: "router-test-secret"},
	}
	s := NewServer(cfg, nil, nil)
	if s == nil || s.engine == nil {
		t.Fatal("NewServer returned a nil server or router")
	}
	// NewServer wires the production fixed-host proxy into the route tree. Swap
	// only that captured proxy's transport/upstream for a local deterministic
	// endpoint before exercising GET, so this route-collision test never reaches
	// the public ChatGPT service.
	upstreamCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"keys":[{"kty":"OKP","crv":"Ed25519","kid":"test","x":"AQ"}]}`))
	}))
	defer upstream.Close()
	s.agentIdentityJWKSProxy.client = upstream.Client()
	s.agentIdentityJWKSProxy.upstream = upstream.URL + "/agent-identities/jwks"
	s.agentIdentityJWKSProxy.allowHTTP = true
	for _, path := range codexAgentIdentityJWKSPaths {
		for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions, http.MethodConnect, http.MethodTrace} {
			resp := httptest.NewRecorder()
			s.engine.ServeHTTP(resp, httptest.NewRequest(method, path, nil))
			want := http.StatusMethodNotAllowed
			if method == http.MethodGet {
				want = http.StatusOK
			}
			if resp.Code != want {
				t.Errorf("%s %s status = %d, want %d", method, path, resp.Code, want)
			}
			if got := resp.Header().Get("Content-Type"); got == "" || !strings.HasPrefix(got, "application/json") {
				t.Errorf("%s %s content type = %q, want JSON", method, path, got)
			}
		}
	}
	if upstreamCalls != len(codexAgentIdentityJWKSPaths) {
		t.Errorf("JWKS upstream calls = %d, want %d GET-only calls", upstreamCalls, len(codexAgentIdentityJWKSPaths))
	}
	// The official Remote Control URL builder appends wham/remote/control to
	// a custom host-root base. Both the plain and optional /v1 WHAM aliases
	// must therefore hit the dedicated server-token middleware instead of the
	// ordinary API-key group or the SPA NoRoute fallback.
	for _, path := range []string{
		"/wham/remote/control/server/pair",
		"/wham/v1/remote/control/server/pair",
	} {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		request.Header.Set("Authorization", "Bearer unknown-remote-control-token")
		request.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		s.engine.ServeHTTP(resp, request)
		if resp.Code != http.StatusUnauthorized {
			t.Errorf("POST %s status = %d, want %d", path, resp.Code, http.StatusUnauthorized)
		}
		if !strings.Contains(resp.Body.String(), "invalid_remote_control_token") {
			t.Errorf("POST %s body = %q, want dedicated Remote Control auth error", path, resp.Body.String())
		}
	}
}

func TestRegisterCodexControlPlaneRoutesAllowlist(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	relayGroup := engine.Group("/v1")
	noPrefixGroup := engine.Group("")
	codexGroup := engine.Group("/codex")
	handler := func(c *gin.Context) { c.Status(http.StatusNoContent) }
	registerCodexControlPlaneRoutes(relayGroup, noPrefixGroup, codexGroup, handler)

	paths := []string{
		"/v1/alpha/history/v2/list_windows",
		"/alpha/history/v2/list_items",
		"/codex/v1/alpha/history/v2/read_item",
		"/codex/alpha/history/v2/search_contents",
		"/v1/alpha/notes/v2/list_files_by_prefix",
		"/alpha/notes/v2/read_file",
		"/codex/v1/alpha/notes/v2/search_contents",
		"/codex/alpha/notes/v2/append_to_file",
		"/v1/alpha/notes/v2/write_file",
		"/alpha/notes/v2/thread_hint",
		"/codex/analytics-events/events",
	}
	for _, path := range paths {
		resp := httptest.NewRecorder()
		engine.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, path, nil))
		if resp.Code != http.StatusNoContent {
			t.Errorf("POST %s status = %d, want 204", path, resp.Code)
		}
	}
	for _, path := range []string{
		"/alpha/history/v2/not-allowlisted",
		"/codex/analytics-events/events/extra",
	} {
		resp := httptest.NewRecorder()
		engine.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, path, nil))
		if resp.Code == http.StatusNoContent {
			t.Errorf("POST %s unexpectedly matched control-plane wildcard", path)
		}
	}
}

func TestRegisterCodexAgentIdentityJWKSUnsupportedRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	// A production engine serves the SPA from NoRoute. Keep that fallback in
	// this regression test so a missing method registration cannot accidentally
	// look like a successful HTML page to the native Codex client.
	engine.NoRoute(func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte("<html>spa</html>"))
	})
	handler := func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		if c.Request.Method != http.MethodGet {
			c.Header("Allow", http.MethodGet)
			c.JSON(http.StatusMethodNotAllowed, gin.H{"error": gin.H{"code": "method_not_allowed"}})
			return
		}
		c.JSON(http.StatusNotImplemented, gin.H{"error": gin.H{"code": "agent_identity_unsupported"}})
	}
	registerCodexAgentIdentityJWKSUnsupportedRoutes(engine, handler)

	paths := []string{
		"/agent-identities/jwks",
		"/v1/agent-identities/jwks",
		"/codex/agent-identities/jwks",
		"/codex/v1/agent-identities/jwks",
		"/wham/agent-identities/jwks",
		"/wham/v1/agent-identities/jwks",
		"/backend-api/wham/agent-identities/jwks",
		"/backend-api/v1/wham/agent-identities/jwks",
		"/backend-api/codex/wham/agent-identities/jwks",
		"/backend-api/codex/v1/wham/agent-identities/jwks",
		"/api/codex/agent-identities/jwks",
		"/api/codex/v1/agent-identities/jwks",
	}
	for _, path := range paths {
		resp := httptest.NewRecorder()
		engine.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, path, nil))
		if resp.Code != http.StatusNotImplemented {
			t.Errorf("GET %s status = %d, want %d", path, resp.Code, http.StatusNotImplemented)
		}
		if got := resp.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
			t.Errorf("GET %s content type = %q, want JSON", path, got)
		}
	}
	for _, path := range paths {
		resp := httptest.NewRecorder()
		engine.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, path, nil))
		if resp.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s status = %d, want %d", path, resp.Code, http.StatusMethodNotAllowed)
		}
		if got := resp.Header().Get("Allow"); got != http.MethodGet {
			t.Errorf("POST %s Allow = %q, want %q", path, got, http.MethodGet)
		}
		if got := resp.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
			t.Errorf("POST %s content type = %q, want JSON", path, got)
		}
		if strings.Contains(resp.Body.String(), "<html>") {
			t.Errorf("POST %s fell through to SPA HTML: %s", path, resp.Body.String())
		}
	}
	for _, method := range []string{http.MethodConnect, http.MethodTrace, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions} {
		for _, path := range paths {
			resp := httptest.NewRecorder()
			engine.ServeHTTP(resp, httptest.NewRequest(method, path, nil))
			if resp.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s status = %d, want %d", method, path, resp.Code, http.StatusMethodNotAllowed)
			}
			if got := resp.Header().Get("Allow"); got != http.MethodGet {
				t.Errorf("%s %s Allow = %q, want %q", method, path, got, http.MethodGet)
			}
			if got := resp.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
				t.Errorf("%s %s content type = %q, want JSON", method, path, got)
			}
			if strings.Contains(resp.Body.String(), "<html>") {
				t.Errorf("%s %s fell through to SPA HTML: %s", method, path, resp.Body.String())
			}
		}
	}
	if isCodexAgentIdentityJWKSPath("/api/codex/agent-identities/jwks/extra") {
		t.Fatal("path with extra suffix must not match JWKS boundary")
	}
	if isCodexAgentIdentityJWKSPath("/api/codex/agent-identities/jwks-other") {
		t.Fatal("path with a suffix segment must not match JWKS boundary")
	}
	seen := make(map[string]struct{}, len(codexAgentIdentityJWKSPaths))
	for _, path := range codexAgentIdentityJWKSPaths {
		if _, ok := seen[path]; ok {
			t.Fatalf("duplicate Codex Agent Identity JWKS alias: %q", path)
		}
		seen[path] = struct{}{}
	}
}

func TestRegisterCodexAgentIdentityJWKSRoutesRateLimitsOnlyGet(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	middlewareCalls := 0
	handlerCalls := 0
	getOnlyMiddleware := func(c *gin.Context) {
		middlewareCalls++
		c.Next()
	}
	handler := func(c *gin.Context) {
		handlerCalls++
		if c.Request.Method == http.MethodGet {
			c.Status(http.StatusNoContent)
			return
		}
		c.Header("Allow", http.MethodGet)
		c.Status(http.StatusMethodNotAllowed)
	}
	registerCodexAgentIdentityJWKSRoutes(engine, getOnlyMiddleware, handler)

	path := codexAgentIdentityJWKSPaths[0]
	methods := []string{
		http.MethodGet,
		http.MethodHead,
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
		http.MethodOptions,
		http.MethodConnect,
		http.MethodTrace,
	}
	for _, method := range methods {
		resp := httptest.NewRecorder()
		engine.ServeHTTP(resp, httptest.NewRequest(method, path, nil))
		want := http.StatusMethodNotAllowed
		if method == http.MethodGet {
			want = http.StatusNoContent
		}
		if resp.Code != want {
			t.Errorf("%s %s status = %d, want %d", method, path, resp.Code, want)
		}
	}
	if middlewareCalls != 1 {
		t.Fatalf("GET-only middleware calls = %d, want 1", middlewareCalls)
	}
	if handlerCalls != len(methods) {
		t.Fatalf("terminal handler calls = %d, want %d", handlerCalls, len(methods))
	}
}

func TestRegisterCodexBackendAliasRoutesControlPlane(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("/backend-api/codex")
	handler := func(c *gin.Context) { c.Status(http.StatusAccepted) }
	registerCodexBackendAliasRoutes(group, codexBackendAliasHandlers{controlPlane: handler})

	for _, path := range []string{
		"/backend-api/codex/alpha/history/v2/list_windows",
		"/backend-api/codex/v1/alpha/notes/v2/write_file",
		"/backend-api/codex/analytics-events/events",
		"/backend-api/codex/v1/analytics-events/events",
	} {
		resp := httptest.NewRecorder()
		engine.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, path, nil))
		if resp.Code != http.StatusAccepted {
			t.Errorf("POST %s status = %d, want 202", path, resp.Code)
		}
	}
}

func TestRegisterCodexBackendClientRoutesEnvironmentDiscovery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("/wham")
	called := ""
	registerCodexBackendClientRoutes(group, "", func(c *gin.Context) {
		called = c.Request.URL.Path
		c.Status(http.StatusNoContent)
	})
	for _, path := range []string{
		"/wham/environments",
		"/wham/environments/by-repo/github/openai/codex",
		"/wham/environments/by-repo/github/openai/codex/main",
	} {
		called = ""
		resp := httptest.NewRecorder()
		engine.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, path, nil))
		if resp.Code != http.StatusNoContent || called == "" {
			t.Errorf("GET %s status=%d called=%q, want 204", path, resp.Code, called)
		}
	}
	for _, path := range []string{
		"/wham/environments/by-repo/github/openai",
		"/wham/environments/by-repo/github/openai/codex/ref/extra",
	} {
		resp := httptest.NewRecorder()
		engine.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, path, nil))
		if resp.Code == http.StatusNoContent {
			t.Errorf("GET %s unexpectedly matched environment wildcard", path)
		}
	}
}

func TestRegisterCodexBackendClientRoutesAppsAndWorkspaceSharing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("/backend-api/codex")
	called := ""
	registerCodexBackendClientRoutes(group, "", func(c *gin.Context) {
		called = c.Request.Method + " " + c.Request.URL.Path
		c.Status(http.StatusNoContent)
	})
	for _, request := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/backend-api/codex/connectors/directory/list"},
		{http.MethodGet, "/backend-api/codex/connectors/directory/list_workspace"},
		{http.MethodPost, "/backend-api/codex/ps/apps/batch"},
		{http.MethodGet, "/backend-api/codex/plugins/featured"},
		{http.MethodPost, "/backend-api/codex/public/plugins/workspace/upload-url"},
		{http.MethodPost, "/backend-api/codex/public/plugins/workspace"},
		{http.MethodPost, "/backend-api/codex/public/plugins/workspace/plugin_123"},
		{http.MethodDelete, "/backend-api/codex/public/plugins/workspace/plugin_123"},
	} {
		called = ""
		resp := httptest.NewRecorder()
		engine.ServeHTTP(resp, httptest.NewRequest(request.method, request.path, nil))
		if resp.Code != http.StatusNoContent || called == "" {
			t.Errorf("%s %s status=%d called=%q, want 204", request.method, request.path, resp.Code, called)
		}
	}
	for _, path := range []string{
		"/backend-api/codex/connectors/unknown",
		"/backend-api/codex/public/plugins/workspace/plugin_123/extra",
	} {
		resp := httptest.NewRecorder()
		engine.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, path, nil))
		if resp.Code == http.StatusNoContent {
			t.Errorf("GET %s unexpectedly matched backend-client wildcard", path)
		}
	}
	// Curated startup-sync export is deliberately not part of the authenticated
	// backend-client table. It is registered separately with a fixed public
	// handler because the official CLI sends no AirGate/API key on this call.
	rootEngine := gin.New()
	rootGroup := rootEngine.Group("/backend-api")
	rootCalled := false
	registerCodexBackendClientRoutes(rootGroup, "", func(c *gin.Context) {
		rootCalled = true
		c.Status(http.StatusNoContent)
	})
	resp := httptest.NewRecorder()
	rootEngine.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/backend-api/plugins/export/curated", nil))
	if resp.Code == http.StatusNoContent || rootCalled {
		t.Fatalf("GET /backend-api/plugins/export/curated unexpectedly matched authenticated backend handler: status=%d called=%v", resp.Code, rootCalled)
	}
}

func TestIsCodexFilesPathRecognizesOnlySupportedFamilies(t *testing.T) {
	for _, path := range []string{
		"/files", "/files/file-1/uploaded", "/v1/files/unknown",
		"/codex/files", "/codex/v1/files/file-1/uploaded",
		"/backend-api/files", "/backend-api/v1/files/file-1/uploaded",
		"/backend-api/codex/files", "/backend-api/codex/v1/files/file-1/uploaded",
		"/api/codex/files", "/api/codex/v1/files/file-1/uploaded",
	} {
		if !isCodexFilesPath(path) {
			t.Errorf("isCodexFilesPath(%q) = false, want true", path)
		}
	}
	for _, path := range []string{"/filesx", "/v1/file", "/codex/models", "/backend-api/codex/responses"} {
		if isCodexFilesPath(path) {
			t.Errorf("isCodexFilesPath(%q) = true, want false", path)
		}
	}
}

func TestIsCodexBackendClientPathRecognizesEnvironmentAliases(t *testing.T) {
	for _, path := range []string{
		"/wham/environments",
		"/wham/environments/by-repo/github/openai/codex",
		"/api/codex/environments/by-repo/github/openai/codex/main",
		"/backend-api/wham/environments",
		"/backend-api/v1/wham/environments/by-repo/github/openai/codex",
	} {
		if !isCodexBackendClientPath(path) {
			t.Errorf("isCodexBackendClientPath(%q) = false, want true", path)
		}
	}
	for _, path := range []string{
		"/connectors/directory/list",
		"/v1/ps/apps/batch",
		"/codex/plugins/featured",
		"/backend-api/codex/public/plugins/workspace/plugin_123",
	} {
		if !isCodexBackendClientPath(path) {
			t.Errorf("isCodexBackendClientPath(%q) = false, want true", path)
		}
	}
	for _, path := range []string{
		"/wham/environmentsx",
		"/api/codex/environment",
		"/backend-api/wham/unknown",
	} {
		if isCodexBackendClientPath(path) {
			t.Errorf("isCodexBackendClientPath(%q) = true, want false", path)
		}
	}
}

func TestRemoteControlAliasUsesDedicatedAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	store := middleware.NewRemoteControlTokenStore()
	if err := store.Register(middleware.RemoteControlTokenRegistration{
		Token:         "router-remote-token",
		KeyInfo:       &auth.APIKeyInfo{KeyID: 1, UserID: 2, GroupID: 3},
		AccountID:     9,
		ServerID:      "server-1",
		EnvironmentID: "env-1",
		ExpiresAt:     time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	called := false
	group := engine.Group("/backend-api/wham", middleware.CodexRemoteControlAuth(nil, store))
	registerCodexRemoteControlRoutes(group, "", func(c *gin.Context) {
		called = true
		if got := c.GetString(middleware.CtxKeyCodexRemoteControlToken); got != "router-remote-token" {
			t.Errorf("token context = %q", got)
		}
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/backend-api/wham/remote/control/server/pair", nil)
	req.Header.Set("Authorization", "Bearer router-remote-token")
	resp := httptest.NewRecorder()
	engine.ServeHTTP(resp, req)
	if resp.Code != http.StatusNoContent || !called {
		t.Fatalf("status=%d called=%v; dedicated Remote Control auth did not run", resp.Code, called)
	}
}

func TestCodexRemoteControlNoRouteReturnsJSONError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.NoRoute(func(c *gin.Context) {
		if isCodexRemoteControlPath(c.Request.URL.Path) {
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"code": "unsupported_endpoint"}})
			return
		}
		c.Status(http.StatusOK)
	})
	resp := httptest.NewRecorder()
	engine.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, "/backend-api/wham/remote/control/unknown", nil))
	if resp.Code != http.StatusNotFound || resp.Header().Get("Content-Type") == "" {
		t.Fatalf("status=%d content-type=%q body=%s", resp.Code, resp.Header().Get("Content-Type"), resp.Body.String())
	}
}
