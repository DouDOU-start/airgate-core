package pipeline

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	providertransport "github.com/DouDOU-start/airgate-core/internal/relay/transport"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
)

func TestRegisterRemoteControlEnrollmentStoresIssuedToken(t *testing.T) {
	store := middleware.NewRemoteControlTokenStore()
	p := &Pipeline{remoteControlTokens: store}
	keyInfo := &auth.APIKeyInfo{KeyID: 7, UserID: 11, GroupID: 13}
	account := &accountreg.Snapshot{ID: 42, Name: "codex", Platform: "codex", Type: "oauth"}
	expires := time.Now().UTC().Add(10 * time.Minute).Truncate(time.Second)
	result := &attemptResult{
		statusCode: 200,
		body:       []byte(`{"server_id":"server-1","environment_id":"env-1","remote_control_token":"issued-token","expires_at":"3026-05-22T12:34:56Z"}`),
	}
	if err := p.registerRemoteControlEnrollment(nil, keyInfo, account, codexRemoteControlMetadata{
		name: "workstation", installationID: "install-1",
	}, result); err != nil {
		t.Fatalf("register enrollment: %v", err)
	}
	record, ok := store.Lookup("issued-token")
	if !ok {
		t.Fatal("issued token was not registered")
	}
	if record.AccountID != account.ID || record.KeyID != keyInfo.KeyID || record.GroupID != keyInfo.GroupID ||
		record.ServerID != "server-1" || record.EnvironmentID != "env-1" || record.InstallationID != "install-1" {
		t.Fatalf("unexpected registration record: %+v", record)
	}
	if record.ExpiresAt.Before(expires) {
		t.Fatalf("expiry = %v, expected a future upstream expiry", record.ExpiresAt)
	}
}

func TestRegisterRemoteControlEnrollmentAcceptsUnixExpiry(t *testing.T) {
	store := middleware.NewRemoteControlTokenStore()
	p := &Pipeline{remoteControlTokens: store}
	expires := time.Now().Add(10 * time.Minute).Unix()
	result := &attemptResult{statusCode: 200, body: []byte(`{"server_id":"s","environment_id":"e","remote_control_token":"t","expires_at":` +
		fmt.Sprint(expires) + `}`)}
	if err := p.registerRemoteControlEnrollment(nil, &auth.APIKeyInfo{KeyID: 1, UserID: 2, GroupID: 3},
		&accountreg.Snapshot{ID: 4}, codexRemoteControlMetadata{}, result); err != nil {
		t.Fatalf("register Unix expiry: %v", err)
	}
	if _, ok := store.Lookup("t"); !ok {
		t.Fatal("Unix-expiry token was not registered")
	}
}

func TestRemoteControlAccountMetadataUsesEnrollmentIdentity(t *testing.T) {
	store := middleware.NewRemoteControlTokenStore()
	keyInfo := &auth.APIKeyInfo{KeyID: 7, UserID: 11, GroupID: 13}
	if err := store.Register(middleware.RemoteControlTokenRegistration{
		Token: "old-token", KeyInfo: keyInfo, AccountID: 42,
		ServerID: "server-1", EnvironmentID: "env-1", Name: "workstation",
		InstallationID: "install-1", ExpiresAt: time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatalf("seed enrollment: %v", err)
	}
	p := &Pipeline{remoteControlTokens: store}
	metadata := remoteControlAccountMetadata(p, codexRemoteControlMetadata{serverID: "server-1"}, keyInfo)
	if metadata.accountID != 42 || metadata.environmentID != "env-1" || metadata.name != "workstation" || metadata.installationID != "install-1" {
		t.Fatalf("resolved metadata = %+v", metadata)
	}
}

func TestParseCodexRemoteControlExpiryRejectsExpired(t *testing.T) {
	if _, err := parseCodexRemoteControlExpiry([]byte(`"2000-01-01T00:00:00Z"`)); err == nil {
		t.Fatal("expired expiry unexpectedly accepted")
	}
}

type remoteControlLifecycleTransport struct {
	mu      sync.Mutex
	request providertransport.Request
	result  providertransport.Result
}

func (t *remoteControlLifecycleTransport) Execute(_ context.Context, request providertransport.Request) providertransport.Result {
	t.mu.Lock()
	t.request = request
	result := t.result
	t.mu.Unlock()
	return result
}

func (t *remoteControlLifecycleTransport) SupportsAccount(account providertransport.Account) bool {
	return providertransport.IsCodexPlatform(account.Platform)
}

func (t *remoteControlLifecycleTransport) snapshot() providertransport.Request {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.request
}

type remoteControlLifecycleLoader struct{ accounts []accountreg.Snapshot }

func (l remoteControlLifecycleLoader) LoadAllForAccountRegistry(context.Context) ([]accountreg.Snapshot, error) {
	return l.accounts, nil
}

func TestHandleCodexRemoteControlEnrollRegistersAndPinsAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	accounts := accountreg.New(remoteControlLifecycleLoader{accounts: []accountreg.Snapshot{
		{ID: 42, Name: "oauth", Platform: "codex", Type: "oauth", State: accountreg.StateActive,
			Credentials: map[string]string{"access_token": "oauth-token"}, GroupIDs: map[int]struct{}{13: {}}},
		{ID: 43, Name: "other", Platform: "codex", Type: "oauth", State: accountreg.StateActive,
			Credentials: map[string]string{"access_token": "other-token"}, GroupIDs: map[int]struct{}{13: {}}},
	}}, nil)
	if err := accounts.Reload(context.Background()); err != nil {
		t.Fatalf("reload accounts: %v", err)
	}
	transport := &remoteControlLifecycleTransport{result: providertransport.Result{
		StatusCode: http.StatusOK,
		Headers:    http.Header{"Content-Type": {"application/json"}},
		Body:       []byte(`{"server_id":"server-1","environment_id":"env-1","remote_control_token":"issued-token","expires_at":"3026-05-22T12:34:56Z"}`),
	}}
	store := middleware.NewRemoteControlTokenStore()
	pipe := New(Options{Accounts: accounts, ProviderTransport: transport, RemoteControlTokens: store,
		Concurrency: scheduler.NewConcurrencyManager(nil), RPM: scheduler.NewRPMCounter(nil)})
	keyInfo := &auth.APIKeyInfo{KeyID: 7, UserID: 11, GroupID: 13, UserBalance: 100}
	engine := gin.New()
	engine.POST("/backend-api/wham/remote/control/server/enroll", func(c *gin.Context) {
		c.Set(middleware.CtxKeyKeyInfo, keyInfo)
	}, pipe.HandleCodexRemoteControl)
	body := []byte(`{"name":"workstation","installation_id":"install-1"}`)
	request := httptest.NewRequest(http.MethodPost, "/backend-api/wham/remote/control/server/enroll", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != string(transport.result.Body) {
		t.Fatalf("enroll response = %d/%q", response.Code, response.Body.String())
	}
	record, ok := store.Lookup("issued-token")
	if !ok || record.AccountID != 42 {
		t.Fatalf("stored record = %+v, found=%v", record, ok)
	}
	if got := transport.snapshot(); got.Account.ID != 42 || got.RemoteControlToken != "" {
		t.Fatalf("native enroll request = account:%d remote-token:%q", got.Account.ID, got.RemoteControlToken)
	}

	// A later refresh carrying the OAuth bearer plus server_id must recover the
	// same account even though another eligible OAuth account is in the group.
	transport.mu.Lock()
	transport.result = providertransport.Result{StatusCode: http.StatusOK,
		Headers: http.Header{"Content-Type": {"application/json"}},
		Body:    []byte(`{"server_id":"server-1","environment_id":"env-1","remote_control_token":"refreshed-token","expires_at":"3026-05-22T12:34:56Z"}`)}
	transport.mu.Unlock()
	refresh := httptest.NewRequest(http.MethodPost, "/backend-api/wham/remote/control/server/refresh", bytes.NewReader([]byte(`{"server_id":"server-1","installation_id":"install-1"}`)))
	refresh.Header.Set("Content-Type", "application/json")
	refresh.Header.Set("Authorization", "Bearer oauth-caller-token")
	refreshResponse := httptest.NewRecorder()
	engine = gin.New()
	engine.POST("/backend-api/wham/remote/control/server/refresh", func(c *gin.Context) {
		c.Set(middleware.CtxKeyKeyInfo, keyInfo)
	}, pipe.HandleCodexRemoteControl)
	engine.ServeHTTP(refreshResponse, refresh)
	if refreshResponse.Code != http.StatusOK {
		t.Fatalf("refresh response = %d/%s", refreshResponse.Code, refreshResponse.Body.String())
	}
	if got := transport.snapshot(); got.Account.ID != 42 {
		t.Fatalf("refresh selected account %d, want 42", got.Account.ID)
	}
	if _, ok := store.Lookup("refreshed-token"); !ok {
		t.Fatal("refreshed token was not registered")
	}
}

func TestCodexRemoteControlDynamicPathEscapesOpaqueIDsExactlyOnce(t *testing.T) {
	tests := []struct {
		name            string
		method          string
		requestPath     string
		wantProvider    string
		wantEnvironment string
		wantClient      string
	}{
		{
			name:            "official encoded slash question mark",
			method:          http.MethodGet,
			requestPath:     "/backend-api/wham/remote/control/environments/env%20%2F%3F/clients",
			wantProvider:    "/remote/control/environments/env%20%2F%3F/clients",
			wantEnvironment: "env /?",
		},
		{
			name:            "client encoded slash question mark",
			method:          http.MethodDelete,
			requestPath:     "/backend-api/wham/remote/control/environments/env%20%2F%3F/clients/client%20%2F%3F",
			wantProvider:    "/remote/control/environments/env%20%2F%3F/clients/client%20%2F%3F",
			wantEnvironment: "env /?",
			wantClient:      "client /?",
		},
		{
			name:            "percent remains one pass",
			method:          http.MethodDelete,
			requestPath:     "/remote/control/environments/env%252F/clients/client%2525",
			wantProvider:    "/remote/control/environments/env%252F/clients/client%2525",
			wantEnvironment: "env%2F",
			wantClient:      "client%25",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(tt.method, tt.requestPath, nil)
			gin.SetMode(gin.TestMode)
			engine := gin.New()
			engine.UseRawPath = true
			engine.UnescapePathValues = true
			var gotRoute codexRemoteControlRoute
			var gotPath string
			var gotOK bool
			handle := func(c *gin.Context) {
				gotRoute, gotPath, gotOK = codexRemoteControlRouteForRequest(c)
				c.Status(http.StatusNoContent)
			}
			engine.GET("/backend-api/wham/remote/control/environments/:environment_id/clients", handle)
			engine.DELETE("/backend-api/wham/remote/control/environments/:environment_id/clients/:client_id", handle)
			engine.GET("/remote/control/environments/:environment_id/clients", handle)
			engine.DELETE("/remote/control/environments/:environment_id/clients/:client_id", handle)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusNoContent || !gotOK {
				t.Fatalf("response=%d route=%+v provider=%q ok=%v", response.Code, gotRoute, gotPath, gotOK)
			}
			if gotPath != tt.wantProvider {
				t.Fatalf("provider path=%q want %q", gotPath, tt.wantProvider)
			}
			if tt.wantClient != "" && gotRoute.Endpoint != adaptor.EndpointCodexRemoteControlClientRevoke {
				t.Fatalf("route endpoint=%q want revoke", gotRoute.Endpoint)
			}
			if tt.wantClient == "" && gotRoute.Endpoint != adaptor.EndpointCodexRemoteControlClientsList {
				t.Fatalf("route endpoint=%q want list", gotRoute.Endpoint)
			}
			parts := strings.Split(strings.Trim(gotPath, "/"), "/")
			if len(parts) == 5 {
				decoded, _ := url.PathUnescape(parts[3])
				if decoded != tt.wantEnvironment {
					t.Fatalf("environment encoding=%q decoded=%q want %q", parts[3], decoded, tt.wantEnvironment)
				}
			}
		})
	}
}

func TestCodexRemoteControlDynamicPathRejectsUnsafeSegments(t *testing.T) {
	for _, path := range []string{
		"/remote/control/environments//clients",
		"/remote/control/environments/.%2E/clients",
		"/remote/control/environments/%2E%2E/clients",
		"/remote/control/environments/env%5Cname/clients",
		"/remote/control/environments/env%00/clients",
		"/remote/control/environments/env%ZZ/clients",
		"/remote/control/environments/env/clients/extra/more",
	} {
		t.Run(path, func(t *testing.T) {
			var request *http.Request
			if strings.Contains(path, "%ZZ") {
				// Build a malformed RawPath directly; net/http normally rejects this
				// before Gin receives a request, but the helper must still fail closed
				// for programmatic callers.
				request = &http.Request{Method: http.MethodGet, URL: &url.URL{Path: path, RawPath: path}}
			} else {
				request = httptest.NewRequest(http.MethodGet, path, nil)
			}
			_, _, ok := codexRemoteControlRouteForRequest(testContextForRequest(request))
			if ok {
				t.Fatalf("unsafe path accepted: %q", path)
			}
		})
	}
}

func TestCodexRemoteControlMetadataAcceptsOpaqueUnicodeAndURLPunctuation(t *testing.T) {
	for _, value := range []string{
		"server /?#% 节点",
		"环境 /?#% workspace",
		"leading and trailing spaces ",
		"%2F remains one decoded pass",
	} {
		if !validCodexRemoteControlMetadata(value) {
			t.Errorf("validCodexRemoteControlMetadata(%q) = false, want true", value)
		}
	}
	if !validCodexRemoteControlMetadata("") {
		t.Error("empty optional metadata should remain valid")
	}
	for _, value := range []string{
		"   ", ".", "..", "server\\id", "server\x00id", "server\x7fid",
		string([]byte{0xff}), strings.Repeat("x", 513),
	} {
		if validCodexRemoteControlMetadata(value) {
			t.Errorf("validCodexRemoteControlMetadata(%q) = true, want false", value)
		}
	}
}

func testContextForRequest(request *http.Request) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = request
	return c
}
