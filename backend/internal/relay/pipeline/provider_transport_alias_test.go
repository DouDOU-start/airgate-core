package pipeline

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/cpa"
)

func TestCodexBaseAliasClassificationCoversOfficialVersionedShapes(t *testing.T) {
	tests := []struct {
		raw         string
		wantCodex   bool
		wantBackend bool
		wantAPI     bool
	}{
		{raw: "https://gateway.example/tenant/backend-api/codex/v1", wantCodex: true},
		{raw: "https://gateway.example/tenant/backend-api/wham", wantBackend: true},
		{raw: "https://gateway.example/tenant/backend-api/wham/v1", wantBackend: true},
		{raw: "https://gateway.example/tenant/backend-api/v1", wantBackend: true},
		{raw: "https://gateway.example/tenant/api/codex/v1", wantAPI: true},
		{raw: "https://gateway.example/tenant/api/codex", wantAPI: true},
		{raw: "https://gateway.example/tenant/backend-api/codex-v2", wantCodex: false},
		{raw: "https://gateway.example/not-backend-api/codex", wantCodex: false},
		{raw: "https://gateway.example/xbackend-api/codex", wantCodex: false},
		{raw: "https://gateway.example/api/codexx", wantAPI: false},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			gotCodex, gotBackend := codexBackendBasePathKinds(tt.raw)
			if gotCodex != tt.wantCodex || gotBackend != tt.wantBackend {
				t.Fatalf("codexBackendBasePathKinds(%q)=(%v,%v), want (%v,%v)", tt.raw, gotCodex, gotBackend, tt.wantCodex, tt.wantBackend)
			}
			if got := isCodexAPIBasePath(tt.raw); got != tt.wantAPI {
				t.Fatalf("isCodexAPIBasePath(%q)=%v, want %v", tt.raw, got, tt.wantAPI)
			}
		})
	}
}

func TestCodexRoutePathClassificationCoversOfficialAliases(t *testing.T) {
	for _, path := range []string{
		"/backend-api/codex/v1/responses",
		"/backend-api/wham/usage",
		"/backend-api/wham/v1/usage",
		"/backend-api/v1/wham/usage",
		"/backend-api/v1/responses",
		"/backend-api/files/file-1/uploaded",
		"/backend-api/alpha/history/v2/list_windows",
		"/backend-api/wham/agent-identities/jwks",
		"/api/codex/v1/responses",
		"/wham/v1/usage",
		"/wham/tasks/task-1",
	} {
		if !isCodexRoutePath(path) {
			t.Errorf("isCodexRoutePath(%q)=false, want true", path)
		}
	}
	for _, path := range []string{
		"",
		"/v1/responses",
		"/backend-api",
		"/backend-api/v1",
		"/backend-api/not-a-codex-route",
		"/backend-api/v1/not-a-codex-route",
		"/backend-api//responses",
		"/backend-api/v1//responses",
		"/backend-api/codex",
		"/backend-api/codex/not-a-codex-route",
		"/api/codex",
		"/api/codex/not-a-codex-route",
		"/codex",
		"/codex/not-a-codex-route",
		"/backend-api/wham",
		"/backend-api/wham/unknown",
		"/backend-api/v1/wham/unknown",
		"/wham",
		"/wham/unknown",
		"/wham/environmentsx",
	} {
		if isCodexRoutePath(path) {
			t.Errorf("isCodexRoutePath(%q)=true, want false", path)
		}
	}
}

func TestSharedResponsesRouteRequiresCodexClientHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newContext := func() *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5"}`))
		return c
	}

	ordinary := newContext()
	if isCodexClientRequest(ordinary) {
		t.Fatal("ordinary /v1/responses request was classified as Codex")
	}

	codex := newContext()
	codex.Request.Header.Set("Originator", "codex_cli_rs")
	if !isCodexClientRequest(codex) {
		t.Fatal("official Codex Originator header was not recognized on shared /v1/responses")
	}
	if got := relayClientType(codex); got != "codex" {
		t.Fatalf("relayClientType = %q, want codex", got)
	}

	sdk := newContext()
	sdk.Request.Header.Set("Originator", "codex_sdk_ts")
	if !isCodexClientRequest(sdk) {
		t.Fatal("official TypeScript SDK Originator header was not recognized on shared /v1/responses")
	}
	if got := relayClientType(sdk); got != "codex" {
		t.Fatalf("relayClientType for TypeScript SDK = %q, want codex", got)
	}
}

func TestSharedCodexAliasesDoNotIdentifyOrdinaryClientsByPath(t *testing.T) {
	for _, path := range []string{
		"/v1/responses",
		"/v1/realtime/calls",
		"/backend-api/v1/responses",
		"/backend-api/wham/usage",
		"/wham/v1/tasks",
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, path, nil)
		if isCodexClientRequest(c) {
			t.Errorf("ordinary shared path %q was classified as Codex", path)
		}
		if got := relayClientType(c); got != "" {
			t.Errorf("relayClientType(%q) = %q, want empty", path, got)
		}
	}
}

func TestClaudeIdentityWinsOverCodexRouteSignals(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/codex/v1/responses", nil)
	c.Request.Header.Set("User-Agent", "claude-cli/2.1.0")
	c.Request.Header.Set("Originator", "codex_cli_rs")
	if isCodexClientRequest(c) {
		t.Fatal("Claude Code request with a Codex route/header was classified as Codex")
	}
	if got := relayClientType(c); got != "claude_code" {
		t.Fatalf("relayClientType = %q, want claude_code", got)
	}
}

func TestCodexProviderBaseURLNormalizesVersionedReverseProxyAliases(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		endpoint string
		want     string
	}{
		{
			name:     "backend codex v1 management",
			baseURL:  "https://proxy.example/tenant/backend-api/codex/v1?tenant=one",
			endpoint: adaptor.EndpointCodexUsage,
			want:     "https://proxy.example/tenant/backend-api?tenant=one",
		},
		{
			name:     "backend wham management",
			baseURL:  "https://proxy.example/tenant/backend-api/wham",
			endpoint: adaptor.EndpointCodexUsage,
			want:     "https://proxy.example/tenant/backend-api",
		},
		{
			name:     "backend wham v1 plugin",
			baseURL:  "https://proxy.example/tenant/backend-api/wham/v1",
			endpoint: adaptor.EndpointCodexPluginsList,
			want:     "https://proxy.example/tenant/backend-api",
		},
		{
			name:     "backend root v1 files",
			baseURL:  "https://proxy.example/tenant/backend-api/v1",
			endpoint: adaptor.EndpointFilesCreate,
			want:     "https://proxy.example/tenant/backend-api",
		},
		{
			name:     "api codex v1 management",
			baseURL:  "https://proxy.example/tenant/api/codex/v1?tenant=one",
			endpoint: adaptor.EndpointCodexUsage,
			want:     "https://proxy.example/tenant?tenant=one",
		},
		{
			name:     "canonical backend codex inference remains unchanged",
			baseURL:  "https://proxy.example/tenant/backend-api/codex",
			endpoint: adaptor.EndpointResponses,
			want:     "https://proxy.example/tenant/backend-api/codex",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := cpa.AccountAuthInput{Platform: "codex", Type: "oauth", Credentials: map[string]string{"base_url": tt.baseURL}}
			if got := codexProviderBaseURLForAccount(account, tt.endpoint); got != tt.want {
				t.Fatalf("codexProviderBaseURLForAccount(%q,%q)=%q, want %q", tt.baseURL, tt.endpoint, got, tt.want)
			}
		})
	}
}

func TestCodexProviderPathNormalizesVersionedAliasesByAccountStyle(t *testing.T) {
	tests := []struct {
		name     string
		account  cpa.AccountAuthInput
		endpoint string
		path     string
		want     string
	}{
		{
			name: "OAuth backend codex v1",
			account: cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
				"base_url": "https://proxy.example/backend-api/codex/v1",
			}},
			endpoint: adaptor.EndpointCodexUsage, path: "/backend-api/codex/v1/usage", want: "/wham/usage",
		},
		{
			name: "OAuth backend wham",
			account: cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
				"base_url": "https://proxy.example/backend-api/wham",
			}},
			endpoint: adaptor.EndpointCodexUsage, path: "/backend-api/wham/usage", want: "/wham/usage",
		},
		{
			name: "OAuth backend root v1",
			account: cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
				"base_url": "https://proxy.example/backend-api/v1",
			}},
			endpoint: adaptor.EndpointCodexUsage, path: "/backend-api/v1/usage", want: "/wham/usage",
		},
		{
			name: "API style codex v1",
			account: cpa.AccountAuthInput{Type: "api_key", Credentials: map[string]string{
				"base_url": "https://proxy.example/api/codex/v1",
			}},
			endpoint: adaptor.EndpointCodexUsage, path: "/api/codex/v1/usage", want: "/api/codex/usage",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := codexProviderPathForAccount(tt.account, tt.endpoint, tt.path); got != tt.want {
				t.Fatalf("codexProviderPathForAccount(%q)=%q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestCodexProviderAliasRewritePreservesProxyPrefixAndQuery(t *testing.T) {
	account := cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
		"base_url": "https://proxy.example/tenant/backend-api/wham/v1?region=cn",
	}}
	base := codexProviderBaseURLForAccount(account, adaptor.EndpointCodexPluginsList)
	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.EscapedPath() != "/tenant/backend-api" || parsed.Query().Get("region") != "cn" {
		t.Fatalf("normalized base lost proxy metadata: %q", base)
	}
	joined := strings.TrimRight(parsed.EscapedPath(), "/") + "/ps/plugins/list"
	if joined != "/tenant/backend-api/ps/plugins/list" {
		t.Fatalf("joined plugin URL path=%q", joined)
	}
}

func TestCodexProviderBaseURLDoesNotSilentlyDropFragments(t *testing.T) {
	for _, test := range []struct {
		base     string
		endpoint string
	}{
		{base: "https://api.openai.com/v1#signed-material", endpoint: adaptor.EndpointCodexTurnCosts},
		{base: "https://gateway.example/backend-api/codex#", endpoint: adaptor.EndpointCodexUsage},
	} {
		account := cpa.AccountAuthInput{Platform: "codex", Type: "api_key", Credentials: map[string]string{"base_url": test.base}}
		got := codexProviderBaseURLForAccount(account, test.endpoint)
		if got != test.base {
			t.Errorf("base URL changed a fragment-bearing value: got %q want %q", got, test.base)
		}
	}
}
