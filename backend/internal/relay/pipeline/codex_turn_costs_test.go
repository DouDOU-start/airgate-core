package pipeline

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/pluginruntime/protocol"
	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/cpa"
	providertransport "github.com/DouDOU-start/airgate-core/internal/relay/transport"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
)

func TestCodexTurnCostsProviderRequestUsesPublicV1Path(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "http://gateway.test/v1/analytics/codex/turn-costs", nil)

	provider := providerRequestFromCPA(c.Request.Context(), c, cpa.ForwardRequest{
		Account: cpa.AccountAuthInput{
			Platform: "codex",
			Type:     "api_key",
			Credentials: map[string]string{
				"api_key":  "sk-test",
				"base_url": "https://api.openai.com/v1",
			},
		},
		Endpoint: adaptor.EndpointCodexTurnCosts,
		Method:   http.MethodPost,
		Path:     "/analytics/codex/turn-costs",
		Payload:  []byte(`{"turn_ids":["turn-1"]}`),
	})

	if provider.BaseURL != "https://api.openai.com/v1" {
		t.Fatalf("base URL = %q, want https://api.openai.com/v1", provider.BaseURL)
	}
	if provider.Path != "/analytics/codex/turn-costs" {
		t.Fatalf("path = %q, want /analytics/codex/turn-costs", provider.Path)
	}
	parsed, err := url.Parse(provider.BaseURL)
	if err != nil {
		t.Fatalf("parse base URL: %v", err)
	}
	joined := parsed.EscapedPath() + provider.Path
	if joined != "/v1/analytics/codex/turn-costs" {
		t.Fatalf("joined URL path = %q, want /v1/analytics/codex/turn-costs", joined)
	}
}

func TestCodexTurnCostsNativeContract(t *testing.T) {
	if !providertransport.CodexNativeWireContract("openai", adaptor.EndpointCodexTurnCosts) {
		t.Fatal("turn-costs endpoint should be native for OpenAI protocol")
	}
	if providertransport.CodexCPATranslationContract(adaptor.EndpointCodexTurnCosts) {
		t.Fatal("turn-costs endpoint must not be a CPA translation contract")
	}
}

type turnCostsAccountLoader struct {
	accounts []accountreg.Snapshot
}

func (l turnCostsAccountLoader) LoadAllForAccountRegistry(context.Context) ([]accountreg.Snapshot, error) {
	return l.accounts, nil
}

type turnCostsIntegrationManager struct {
	mu       sync.Mutex
	requests []protocol.CodexExecuteRequest
}

func (m *turnCostsIntegrationManager) CodexTransportMode() string {
	return string(providertransport.CodexModeNativeOnly)
}

func (m *turnCostsIntegrationManager) ExecuteCodex(_ context.Context, request protocol.CodexExecuteRequest, emit func(protocol.CodexExecuteEvent) error) error {
	m.mu.Lock()
	m.requests = append(m.requests, request)
	m.mu.Unlock()
	if request.Endpoint != adaptor.EndpointCodexTurnCosts {
		return nil
	}
	if err := emit(protocol.CodexExecuteEvent{
		Type:       protocol.CodexEventResponseHeaders,
		StatusCode: http.StatusOK,
		Header:     map[string][]string{"Content-Type": {"application/json"}},
	}); err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]any{"turns": []any{}})
	if err := emit(protocol.CodexExecuteEvent{Type: protocol.CodexEventData, Data: body}); err != nil {
		return err
	}
	return emit(protocol.CodexExecuteEvent{Type: protocol.CodexEventCompleted})
}

func (m *turnCostsIntegrationManager) snapshot() []protocol.CodexExecuteRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]protocol.CodexExecuteRequest(nil), m.requests...)
}

// TestCodexTurnCostsSkipsOAuthAndSelectsAPIKey verifies the endpoint-specific
// auth contract at the route-loop boundary. Even when an OAuth account has a
// higher priority, the public turn-cost worker must consume an API-key account
// and must not spend a native attempt on the OAuth account first.
func TestCodexTurnCostsSkipsOAuthAndSelectsAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	accounts := accountreg.New(turnCostsAccountLoader{accounts: []accountreg.Snapshot{
		{
			ID: 1, Name: "oauth-first", Platform: "codex", Type: "oauth", Priority: 100,
			State: accountreg.StateActive, Credentials: map[string]string{
				"access_token": "oauth-token", "chatgpt_account_id": "oauth-acct",
			}, GroupIDs: map[int]struct{}{7: {}},
		},
		{
			ID: 2, Name: "api-key-second", Platform: "codex", Type: "api_key", Priority: 1,
			State: accountreg.StateActive, Credentials: map[string]string{
				"api_key": "sk-api", "base_url": "https://api.openai.com/v1",
			}, GroupIDs: map[int]struct{}{7: {}},
		},
	}}, nil)
	if err := accounts.Reload(context.Background()); err != nil {
		t.Fatalf("load account registry: %v", err)
	}
	manager := &turnCostsIntegrationManager{}
	pipe := New(Options{
		Accounts:          accounts,
		ProviderTransport: providertransport.NewCodexPluginTransport(manager),
		Concurrency:       scheduler.NewConcurrencyManager(nil),
		RPM:               scheduler.NewRPMCounter(nil),
	})

	engine := gin.New()
	engine.POST("/v1/analytics/codex/turn-costs", func(c *gin.Context) {
		c.Set(middleware.CtxKeyKeyInfo, testKeyInfo())
	}, pipe.HandleCodexBackendClient)

	request := httptest.NewRequest(http.MethodPost, "/v1/analytics/codex/turn-costs", strings.NewReader(`{"turn_ids":["turn-1"]}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Originator", "codex_cli_rs")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("turn-costs status = %d, body = %s", response.Code, response.Body.String())
	}
	if got := response.Body.String(); got != `{"turns":[]}` {
		t.Fatalf("turn-costs body = %s", got)
	}

	requests := manager.snapshot()
	if len(requests) != 1 {
		t.Fatalf("native request count = %d, want 1", len(requests))
	}
	selected := requests[0]
	if selected.Credential.AuthKind != "api_key" {
		t.Fatalf("selected auth kind = %q, want api_key", selected.Credential.AuthKind)
	}
	if selected.Credential.APIKey != "sk-api" {
		t.Fatalf("selected API key = %q, want sk-api", selected.Credential.APIKey)
	}
	if selected.Credential.AccessToken == "oauth-token" {
		t.Fatalf("OAuth token leaked into API-key lease: %q", selected.Credential.AccessToken)
	}
	if selected.Endpoint != adaptor.EndpointCodexTurnCosts || selected.Method != http.MethodPost || selected.Path != "/analytics/codex/turn-costs" {
		t.Fatalf("native request = endpoint:%q method:%q path:%q", selected.Endpoint, selected.Method, selected.Path)
	}
}
