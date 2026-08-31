package pipeline

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	providertransport "github.com/DouDOU-start/airgate-core/internal/relay/transport"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
)

func TestAccountProviderHeadersStripsClientCredentialsAndPreservesCodexRouting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	req := httptest.NewRequest(http.MethodGet, "/v1/responses?model=gpt-5", nil)
	req.Header.Add("Authorization", "Bearer client-api-key")
	req.Header.Add("Cookie", "__Secure-next-auth.session-token=client-session")
	req.Header.Add("Proxy-Authorization", "Basic c2VjcmV0")
	req.Header.Add("X-Api-Key", "client-api-key")
	req.Header.Add("X-Codex-Routing-Hint", "model=gpt-5-codex;tier=fast")
	req.Header.Add("X-Codex-Future-Feature", "enabled")
	req.Header.Add("X-OpenAI-Future-Metadata", "opaque")
	req.Header.Add("X-Session-Id", "realtime-session")
	req.Header.Add("X-OpenAI-Session-Id", "provider-session")
	req.Header.Add("X-OpenAI-Actor-Authorization", "caller-actor-secret")
	req.Header.Add("X-OpenAI-Session-Token", "must-not-cross")
	req.Header.Add("X-Client-Version", "0.1.0")
	req.Header.Add("X-Not-Forwarded", "should-not-cross-account-boundary")
	req.Header.Add("Accept", "text/event-stream")

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req

	got := accountProviderHeaders(c)
	for _, name := range []string{"Authorization", "Cookie", "Proxy-Authorization", "X-Api-Key"} {
		if value := got.Get(name); value != "" {
			t.Errorf("%s leaked into provider headers: %q", name, value)
		}
	}
	if got.Get("X-OpenAI-Session-Token") != "" {
		t.Fatal("credential-shaped X-OpenAI header crossed provider boundary")
	}
	for name, want := range map[string]string{
		"X-Codex-Routing-Hint":     "model=gpt-5-codex;tier=fast",
		"X-Codex-Future-Feature":   "enabled",
		"X-OpenAI-Future-Metadata": "opaque",
		"X-Session-Id":             "realtime-session",
		"X-OpenAI-Session-Id":      "provider-session",
		"X-Client-Version":         "0.1.0",
		"Accept":                   "text/event-stream",
		"Content-Type":             "application/json",
	} {
		if gotValue := got.Get(name); gotValue != want {
			t.Errorf("%s = %q, want %q", name, gotValue, want)
		}
	}
	if got.Get("X-Not-Forwarded") != "" {
		t.Fatal("unrecognized client header crossed provider boundary")
	}
	if got.Get("X-OpenAI-Actor-Authorization") != "" {
		t.Fatal("caller-supplied actor authorization crossed provider boundary")
	}
}

func TestAccountProviderHeadersCopiesValuesWithoutAliasingRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	req := httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	req.Header.Add("X-Codex-Turn-Metadata", "first")
	req.Header.Add("X-Codex-Turn-Metadata", "second")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req

	got := accountProviderHeaders(c)
	values := got.Values("X-Codex-Turn-Metadata")
	if len(values) != 2 || values[0] != "first" || values[1] != "second" {
		t.Fatalf("copied values = %#v, want [first second]", values)
	}
	// Mutating the returned slice must not mutate the inbound request header.
	values[0] = "mutated"
	if req.Header.Values("X-Codex-Turn-Metadata")[0] != "first" {
		t.Fatal("provider header values alias inbound request header storage")
	}
}

func TestAccountProviderHeadersForRequestInfersNativeContentTypes(t *testing.T) {
	tests := []struct {
		name        string
		endpoint    string
		rawBody     []byte
		rawCT       string
		requestCT   string
		wantContent string
	}{
		{
			name: "raw realtime SDP without header", endpoint: "realtime_calls",
			rawBody: []byte("\r\nv=0\r\no=- 1 1 IN IP4 127.0.0.1\r\n"), wantContent: "application/sdp",
		},
		{
			name: "realtime backend JSON without header", endpoint: "realtime_calls",
			rawBody: []byte(`{"sdp":"v=0\\r\\n","session":{"model":"gpt-realtime"}}`), wantContent: "application/json",
		},
		{
			name: "compact JSON without header", endpoint: "compact",
			rawBody: []byte(`{"model":"gpt-5-codex"}`), wantContent: "application/json",
		},
		{
			name: "alpha search JSON without header", endpoint: "alpha_search",
			rawBody: []byte(`{"model":"gpt-5-codex"}`), wantContent: "application/json",
		},
		{
			name: "memories JSON without header", endpoint: "memories_trace_summarize",
			rawBody: []byte(`{"model":"gpt-5-codex"}`), wantContent: "application/json",
		},
		{
			name: "raw content type stays exact", endpoint: "realtime_calls",
			rawBody: []byte("v=0\r\n"), rawCT: "application/sdp; x-wire=official", wantContent: "application/sdp; x-wire=official",
		},
		{
			name: "multipart boundary stays exact", endpoint: "realtime_calls",
			rawBody: []byte("--CodexBoundary\r\n--CodexBoundary--\r\n"),
			rawCT:   `multipart/form-data; boundary="CodexBoundary"`, wantContent: `multipart/form-data; boundary="CodexBoundary"`,
		},
		{
			name: "inbound header wins over raw fallback", endpoint: "realtime_calls",
			rawBody: []byte("v=0\r\n"), rawCT: "application/sdp", requestCT: "application/custom", wantContent: "application/custom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			req := httptest.NewRequest(http.MethodPost, "/v1/test", nil)
			if tt.requestCT != "" {
				req.Header.Set("Content-Type", tt.requestCT)
			}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = req
			got := accountProviderHeadersForRequest(c, tt.endpoint, tt.rawBody, tt.rawCT)
			if value := got.Get("Content-Type"); value != tt.wantContent {
				t.Fatalf("Content-Type = %q, want %q", value, tt.wantContent)
			}
		})
	}
}

// websocketCapabilityProvider is intentionally not a WebSocket executor. The
// preflight tests below should prove that Core rejects an unavailable native
// data plane before it attempts the HTTP 101 upgrade.
type websocketCapabilityProvider struct {
	caps providertransport.Capabilities
	mode string
}

func (p websocketCapabilityProvider) Execute(context.Context, providertransport.Request) providertransport.Result {
	return providertransport.Result{}
}

func (p websocketCapabilityProvider) Capabilities() providertransport.Capabilities {
	return p.caps
}

func (p websocketCapabilityProvider) CodexTransportMode() string { return p.mode }

type websocketAccountLoader struct {
	accounts []accountreg.Snapshot
}

func (l websocketAccountLoader) LoadAllForAccountRegistry(context.Context) ([]accountreg.Snapshot, error) {
	return l.accounts, nil
}

func newWebSocketPreflightPipeline(t *testing.T, caps providertransport.Capabilities, accounts ...accountreg.Snapshot) *Pipeline {
	t.Helper()
	reg := accountreg.New(websocketAccountLoader{accounts: accounts}, nil)
	if err := reg.Reload(context.Background()); err != nil {
		t.Fatalf("load account registry: %v", err)
	}
	return New(Options{
		Accounts:          reg,
		Concurrency:       scheduler.NewConcurrencyManager(nil),
		RPM:               scheduler.NewRPMCounter(nil),
		ProviderTransport: websocketCapabilityProvider{caps: caps},
	})
}

func newWebSocketPreflightPipelineWithMode(t *testing.T, mode string, caps providertransport.Capabilities, accounts ...accountreg.Snapshot) *Pipeline {
	t.Helper()
	reg := accountreg.New(websocketAccountLoader{accounts: accounts}, nil)
	if err := reg.Reload(context.Background()); err != nil {
		t.Fatalf("load account registry: %v", err)
	}
	return New(Options{
		Accounts:          reg,
		Concurrency:       scheduler.NewConcurrencyManager(nil),
		RPM:               scheduler.NewRPMCounter(nil),
		ProviderTransport: websocketCapabilityProvider{caps: caps, mode: mode},
	})
}

func serveWebSocketPreflight(t *testing.T, p *Pipeline, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET(path, func(c *gin.Context) {
		// Preflight cases exercise native Codex websocket policy; identify the
		// synthetic caller as the official CLI so the test reaches that policy
		// instead of the shared-alias client boundary.
		c.Request.Header.Set("Originator", "codex_cli_rs")
		c.Set(middleware.CtxKeyKeyInfo, &auth.APIKeyInfo{
			KeyID: 1, UserID: 2, GroupID: 7, UserBalance: 100,
		})
	}, p.HandleResponsesWebSocket)
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}

func assertWebSocketUpgradeRequired(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Code != http.StatusUpgradeRequired {
		t.Fatalf("status = %d, want 426; body = %s", response.Code, response.Body.String())
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode 426 body: %v", err)
	}
	if envelope.Error.Code != "websocket_not_available" {
		t.Fatalf("error code = %q, want websocket_not_available", envelope.Error.Code)
	}
	if response.Header().Get("Upgrade") != "websocket" || response.Header().Get("Connection") != "Upgrade" {
		t.Fatalf("upgrade headers = Connection:%q Upgrade:%q", response.Header().Get("Connection"), response.Header().Get("Upgrade"))
	}
}

func TestHandleResponsesWebSocketPreflightRejectsTransportWithoutWebSocketCapability(t *testing.T) {
	p := newWebSocketPreflightPipeline(t, providertransport.Capabilities{HTTP: true, Streaming: true}, accountreg.Snapshot{
		ID: 1, Platform: "codex", Type: "oauth", State: accountreg.StateActive,
		Credentials: map[string]string{"access_token": "token"},
		Models:      map[string]struct{}{"gpt-5-codex": {}}, GroupIDs: map[int]struct{}{7: {}},
	})
	assertWebSocketUpgradeRequired(t, serveWebSocketPreflight(t, p, "/responses", nil))
}

func TestHandleResponsesWebSocketPreflightRejectsCPAOnlyAccounts(t *testing.T) {
	p := newWebSocketPreflightPipelineWithMode(t, "cpa_only", providertransport.Capabilities{HTTP: true, Streaming: true, WebSocket: true}, accountreg.Snapshot{
		ID: 1, Platform: "codex", Type: "oauth", State: accountreg.StateActive,
		Credentials: map[string]string{"access_token": "token"},
		Models:      map[string]struct{}{"gpt-5-codex": {}}, GroupIDs: map[int]struct{}{7: {}},
	})
	assertWebSocketUpgradeRequired(t, serveWebSocketPreflight(t, p, "/responses", nil))
}

func TestHandleResponsesWebSocketPreflightRejectsCPAOnlyModelHintWhenNativeAccountExists(t *testing.T) {
	p := newWebSocketPreflightPipelineWithMode(t, "cpa_only", providertransport.Capabilities{HTTP: true, Streaming: true, WebSocket: true},
		accountreg.Snapshot{
			ID: 1, Platform: "codex", Type: "oauth", State: accountreg.StateActive,
			Credentials: map[string]string{"access_token": "native-token"},
			Models:      map[string]struct{}{"gpt-5-native": {}}, GroupIDs: map[int]struct{}{7: {}},
		},
		accountreg.Snapshot{
			ID: 2, Platform: "codex", Type: "oauth", State: accountreg.StateActive,
			Credentials: map[string]string{"access_token": "cpa-token"},
			Models:      map[string]struct{}{"gpt-5-cpa": {}}, GroupIDs: map[int]struct{}{7: {}},
		},
	)
	response := serveWebSocketPreflight(t, p, "/responses", map[string]string{
		"X-Codex-Routing-Hint": "model=gpt-5-cpa;tier=fast",
	})
	assertWebSocketUpgradeRequired(t, response)
}

func TestNativeCodexAccountSelectionRejectsNonCanonicalPlatformAliases(t *testing.T) {
	for _, platform := range []string{"openai", "openai-codex", "openai_codex"} {
		t.Run(platform, func(t *testing.T) {
			p := newWebSocketPreflightPipeline(t, providertransport.Capabilities{HTTP: true, Streaming: true, WebSocket: true}, accountreg.Snapshot{
				ID: 9, Platform: platform, Type: "oauth", State: accountreg.StateActive,
				Credentials: map[string]string{"access_token": "token"},
				Models:      map[string]struct{}{"gpt-5-codex": {}}, GroupIDs: map[int]struct{}{7: {}},
			})
			if p.hasNativeCodexAccount(7) {
				t.Fatalf("platform %q was incorrectly detected as a native Codex account", platform)
			}
			if p.hasNativeCodexAccountForModel(7, "gpt-5-codex") {
				t.Fatalf("platform %q was incorrectly detected for its model", platform)
			}
			if selected := p.pickNativeCodexAccount(7, "gpt-5-codex"); selected != nil {
				t.Fatalf("selected account = %+v, want no account", selected)
			}
		})
	}
}
