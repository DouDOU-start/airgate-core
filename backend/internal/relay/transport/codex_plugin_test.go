package transport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/pluginruntime"
	"github.com/DouDOU-start/airgate-core/internal/pluginruntime/protocol"
)

type fakeCodexManager struct {
	req    protocol.CodexExecuteRequest
	events []protocol.CodexExecuteEvent
	err    error
	called bool
	mode   string
}

type concurrentCodexManager struct{}

func (concurrentCodexManager) ExecuteCodex(_ context.Context, _ protocol.CodexExecuteRequest, emit func(protocol.CodexExecuteEvent) error) error {
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = emit(protocol.CodexExecuteEvent{Type: protocol.CodexEventData, Data: []byte{byte(i)}})
		}(i)
	}
	wg.Wait()
	return nil
}

func (f *fakeCodexManager) CodexTransportMode() string { return f.mode }

func (f *fakeCodexManager) ExecuteCodex(_ context.Context, req protocol.CodexExecuteRequest, emit func(protocol.CodexExecuteEvent) error) error {
	f.req = req
	f.called = true
	for _, event := range f.events {
		if err := emit(event); err != nil {
			return err
		}
	}
	return f.err
}

func TestCodexPluginTransportEligibility(t *testing.T) {
	for _, req := range []Request{
		{Account: Account{Platform: "claude"}, EntryProtocol: "openai", Endpoint: "responses"},
		{Account: Account{Platform: "openai-compatible"}, EntryProtocol: "openai", Endpoint: "responses"},
		{Account: Account{Platform: "openai_compatible"}, EntryProtocol: "openai", Endpoint: "responses"},
		{Account: Account{Platform: "openai-compatibility"}, EntryProtocol: "openai", Endpoint: "responses"},
		{Account: Account{Platform: "codex"}, EntryProtocol: "anthropic", Endpoint: "responses"},
		{Account: Account{Platform: "codex"}, EntryProtocol: "openai", Endpoint: "chat_completions"},
	} {
		mgr := &fakeCodexManager{}
		transport := NewCodexPluginTransport(mgr)
		result := transport.Execute(context.Background(), req)
		if !errors.Is(result.BuildErr, ErrCodexPluginUnsupported) || mgr.called {
			t.Fatalf("req=%+v result=%+v called=%v", req, result, mgr.called)
		}
	}
}

func TestCodexPluginTransportRejectsUnexpandedRemoteControlPath(t *testing.T) {
	for _, endpoint := range []string{
		protocol.CodexEndpointRemoteControlClientsList,
		protocol.CodexEndpointRemoteControlClientRevoke,
	} {
		mgr := &fakeCodexManager{}
		transport := NewCodexPluginTransport(mgr)
		result := transport.Execute(context.Background(), Request{
			Account:       Account{Platform: "codex", Type: "oauth"},
			EntryProtocol: "openai",
			Endpoint:      endpoint,
			Method:        http.MethodGet,
		})
		if !errors.Is(result.BuildErr, ErrCodexRemoteControlPathMissing) || mgr.called {
			t.Fatalf("endpoint=%q result=%+v called=%v", endpoint, result, mgr.called)
		}
	}
}

func TestCodexPluginTransportAcceptsExpandedRemoteControlPath(t *testing.T) {
	mgr := &fakeCodexManager{}
	transport := NewCodexPluginTransport(mgr)
	result := transport.Execute(context.Background(), Request{
		Account:       Account{Platform: "codex", Type: "oauth"},
		EntryProtocol: "openai",
		Endpoint:      protocol.CodexEndpointRemoteControlClientsList,
		Method:        http.MethodGet,
		BaseURL:       "https://chatgpt.com/backend-api",
		Path:          "/remote/control/environments/env-1/clients",
	})
	if errors.Is(result.BuildErr, ErrCodexRemoteControlPathMissing) || !mgr.called {
		t.Fatalf("result=%+v called=%v", result, mgr.called)
	}
}

func TestCodexNativeWireContractOfficialEndpoints(t *testing.T) {
	for _, endpoint := range []string{
		"responses", "compact", "alpha_search", "realtime_calls",
		"realtime_sideband", "memories_trace_summarize", "guardian", "guardian_classifier",
		"files_create", "files_finalize", "images_generations", "images_edits",
		"codex_environments", "codex_environments_by_repo",
		"codex_connectors_directory_list", "codex_connectors_directory_list_workspace",
		"codex_apps_batch", "codex_plugins_featured",
		"codex_plugins_workspace_upload_url", "codex_plugins_workspace_create",
		"codex_plugins_workspace_update", "codex_plugins_workspace_detail", "codex_plugins_workspace_delete",
		protocol.CodexEndpointRemoteControlServerWebSocket,
	} {
		if !CodexNativeWireContract("openai", endpoint) {
			t.Errorf("endpoint %q should have a native Codex wire contract", endpoint)
		}
		if endpoint != "responses" && endpoint != "images_generations" && endpoint != "images_edits" && CodexCPATranslationContract(endpoint) {
			t.Errorf("native-only endpoint %q unexpectedly has a CPA contract", endpoint)
		}
	}
	for _, endpoint := range []string{"images_generations", "images_edits"} {
		if !CodexCPATranslationContract(endpoint) {
			t.Errorf("image endpoint %q should retain its CPA fallback contract", endpoint)
		}
	}
	if CodexNativeWireContract("anthropic", "realtime_calls") {
		t.Fatal("non-OpenAI entry protocol must not enter native realtime wire")
	}
}

func TestCodexOAuthOnlyEndpointClassification(t *testing.T) {
	for _, endpoint := range []string{
		protocol.CodexEndpointHistoryListWindows,
		protocol.CodexEndpointHistoryListItems,
		protocol.CodexEndpointHistoryReadItem,
		protocol.CodexEndpointHistorySearchContents,
		protocol.CodexEndpointNotesListFilesByPrefix,
		protocol.CodexEndpointNotesReadFile,
		protocol.CodexEndpointNotesSearchContents,
		protocol.CodexEndpointNotesAppendToFile,
		protocol.CodexEndpointNotesWriteFile,
		protocol.CodexEndpointNotesThreadHint,
		"codex_connectors_directory_list", "codex_connectors_directory_list_workspace",
		"codex_apps_batch",
		"codex_plugin_legacy_enable", "codex_plugin_legacy_uninstall",
		"codex_plugins_workspace_upload_url", "codex_plugins_workspace_create",
		"codex_plugins_workspace_update", "codex_plugins_workspace_detail", "codex_plugins_workspace_delete",
	} {
		if !CodexOAuthOnlyEndpoint(endpoint) {
			t.Errorf("endpoint %q should require OAuth", endpoint)
		}
	}
	if CodexOAuthOnlyEndpoint("codex_plugins_featured") {
		t.Fatal("legacy featured discovery should remain optionally authenticated")
	}
}

func TestCodexNativeEligibilityRejectsAPIKeyForHistoryNotes(t *testing.T) {
	for _, endpoint := range []string{
		protocol.CodexEndpointHistoryListWindows,
		protocol.CodexEndpointHistorySearchContents,
		protocol.CodexEndpointNotesReadFile,
		protocol.CodexEndpointNotesWriteFile,
	} {
		request := Request{
			Account:       Account{Platform: "codex", Type: "api_key"},
			EntryProtocol: "openai",
			Endpoint:      endpoint,
		}
		if nativeEligible(request, CodexModeAuto) {
			t.Errorf("API-key account unexpectedly eligible for OAuth-only endpoint %q", endpoint)
		}
		request.Account.Type = "oauth"
		if !nativeEligible(request, CodexModeAuto) {
			t.Errorf("OAuth account unexpectedly rejected for endpoint %q", endpoint)
		}
	}
}

func TestCPATranslateKeepsNativeOnlyCodexEndpointsNative(t *testing.T) {
	base := Request{Account: Account{Platform: "codex", Type: "oauth"}, EntryProtocol: "openai"}
	for _, endpoint := range []string{"realtime_calls", "realtime_sideband", "memories_trace_summarize", "compact", "alpha_search", "guardian", "guardian_classifier"} {
		request := base
		request.Endpoint = endpoint
		if !nativeEligible(request, CodexModeCPATranslate) {
			t.Errorf("cpa_translate rejected native-only endpoint %q", endpoint)
		}
	}
	base.Endpoint = "responses"
	if nativeEligible(base, CodexModeCPATranslate) {
		t.Fatal("cpa_translate must reserve Responses for CPA translation")
	}
}

func TestCodexPluginTransportAccountEligibility(t *testing.T) {
	transport := NewCodexPluginTransport(&fakeCodexManager{})
	for _, account := range []Account{
		{Platform: "codex", Type: "oauth"},
		{Platform: " CODEX ", Type: " API_KEY "},
	} {
		if !transport.SupportsAccount(account) {
			t.Errorf("account=%+v should be eligible for native Codex transport", account)
		}
	}
	for _, account := range []Account{
		{Platform: "codex"},
		{Platform: "codex", Type: "token"},
		{Platform: "codex", Type: "openai_oauth"},
		{Platform: "xai"},
		{Platform: "openai"},
		{Platform: "openai-codex"},
		{Platform: "openai_codex"},
		{Platform: " OpenAI_CODEX "},
		{Platform: "openai-compatibility"},
		{Platform: "openai-codex-compatible"},
		{Platform: ""},
	} {
		if transport.SupportsAccount(account) {
			t.Errorf("account=%+v should remain on CPA", account)
		}
	}
}

func TestIsCodexPlatformRequiresCanonicalPlatform(t *testing.T) {
	for _, platform := range []string{"codex", " CODEX ", "CoDeX"} {
		if !IsCodexPlatform(platform) {
			t.Errorf("IsCodexPlatform(%q) = false, want true", platform)
		}
	}
	for _, platform := range []string{"", "openai", "openai-codex", "openai_codex", "claude", "openai-compatibility", "openai-codex-compatible", "codex-cli"} {
		if IsCodexPlatform(platform) {
			t.Errorf("IsCodexPlatform(%q) = true, want false", platform)
		}
	}
}

func TestCodexNativeAuthTypeRequiresCanonicalValues(t *testing.T) {
	for _, authType := range []string{"oauth", " OAUTH ", "api_key", " API_KEY "} {
		if !IsCodexNativeAuthType(authType) {
			t.Errorf("IsCodexNativeAuthType(%q) = false, want true", authType)
		}
	}
	for _, authType := range []string{"", "refresh_token", "api-key", "apikey", "key", "unknown"} {
		if IsCodexNativeAuthType(authType) {
			t.Errorf("IsCodexNativeAuthType(%q) = true, want false", authType)
		}
	}

	request := Request{
		Account:       Account{Platform: "codex", Type: "api-key"},
		EntryProtocol: "openai",
		Endpoint:      "responses",
	}
	if nativeEligible(request, CodexModeAuto) {
		t.Fatal("pre-normalized API-key alias unexpectedly entered the native executor")
	}
}

func TestNormalizeCodexTransportMode(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  string
		want CodexTransportMode
	}{
		{name: "empty is auto", raw: "", want: CodexModeAuto},
		{name: "case and whitespace", raw: "  CPA_Translate  ", want: CodexModeCPATranslate},
		{name: "unknown is auto", raw: "future_mode", want: CodexModeAuto},
		{name: "native only", raw: "native_only", want: CodexModeNativeOnly},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := NormalizeCodexTransportMode(test.raw); got != test.want {
				t.Fatalf("NormalizeCodexTransportMode()=%q want %q", got, test.want)
			}
		})
	}
}

func TestCredentialHeaderUsesBearerOnlyForAuthorization(t *testing.T) {
	tests := []struct {
		name        string
		credentials map[string]string
		wantName    string
		wantPrefix  string
		wantSet     bool
	}{
		{name: "default authorization", credentials: nil, wantName: "Authorization", wantPrefix: "Bearer ", wantSet: false},
		{name: "custom api key is raw", credentials: map[string]string{"auth_header_name": "X-API-Key"}, wantName: "X-API-Key", wantPrefix: "", wantSet: false},
		{name: "explicit custom prefix", credentials: map[string]string{"auth_header_name": "X-API-Key", "auth_header_prefix": "Token "}, wantName: "X-API-Key", wantPrefix: "Token ", wantSet: true},
		{name: "explicit empty authorization prefix", credentials: map[string]string{"auth_header_name": "Authorization", "auth_header_prefix": ""}, wantName: "Authorization", wantPrefix: "", wantSet: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotName, gotPrefix, gotSet := credentialHeader(tt.credentials)
			if gotName != tt.wantName || gotPrefix != tt.wantPrefix || gotSet != tt.wantSet {
				t.Fatalf("credentialHeader() = (%q, %q, %v), want (%q, %q, %v)", gotName, gotPrefix, gotSet, tt.wantName, tt.wantPrefix, tt.wantSet)
			}
		})
	}
}

func TestCodexPluginTransportUsesPluginPolicyNotCredentials(t *testing.T) {
	mgr := &fakeCodexManager{mode: "cpa_translate"}
	transport := NewCodexPluginTransport(mgr)
	if got := transport.CodexTransportMode(); got != string(CodexModeCPATranslate) {
		t.Fatalf("plugin mode = %q", got)
	}
	// Retired account fields must not override the plugin policy.
	result := transport.Execute(context.Background(), Request{Account: Account{Platform: "codex", Credentials: map[string]string{"codex_mode": "native_only", "preferred_mode": "native_only", "transport_mode": "native_only"}}, EntryProtocol: "openai", Endpoint: "responses"})
	if !errors.Is(result.BuildErr, ErrCodexPluginUnsupported) || mgr.called {
		t.Fatalf("account routing fields affected dispatch: result=%+v called=%v", result, mgr.called)
	}
}

func TestCodexPluginTransportRejectsUnknownCredentialFamily(t *testing.T) {
	for _, authKind := range []string{"", "session", "future_auth"} {
		mgr := &fakeCodexManager{}
		result := NewCodexPluginTransport(mgr).Execute(context.Background(), Request{
			Account: Account{Platform: "codex", Type: authKind, Credentials: map[string]string{
				"access_token": "must-not-forward",
			}},
			EntryProtocol: "openai",
			Endpoint:      "responses",
		})
		if !errors.Is(result.BuildErr, ErrCodexPluginUnsupported) || mgr.called {
			t.Fatalf("auth kind %q reached native executor: result=%+v called=%v", authKind, result, mgr.called)
		}
	}
}

func TestCodexPluginTransportPropagatesNormalizedClient(t *testing.T) {
	mgr := &fakeCodexManager{events: []protocol.CodexExecuteEvent{{Type: protocol.CodexEventCompleted}}}
	result := NewCodexPluginTransport(mgr).Execute(context.Background(), Request{
		Account:       Account{Platform: "codex", Type: "oauth", Credentials: map[string]string{"access_token": "token"}},
		RequestID:     "request-1",
		Client:        "codex",
		EntryProtocol: "openai",
		Endpoint:      "responses",
	})
	if result.BuildErr != nil || !mgr.called {
		t.Fatalf("execution failed: result=%+v called=%v", result, mgr.called)
	}
	if mgr.req.Client != "codex" {
		t.Fatalf("executor client = %q, want codex", mgr.req.Client)
	}
}

func TestCodexPluginTransportTurnCostsIsAPIKeyOnlyAndProjectsHeaders(t *testing.T) {
	mgr := &fakeCodexManager{events: []protocol.CodexExecuteEvent{{Type: protocol.CodexEventCompleted}}}
	result := NewCodexPluginTransport(mgr).Execute(context.Background(), Request{
		Account: Account{Platform: "codex", Type: "api_key", Credentials: map[string]string{
			"api_key": "sk-selected", "access_token": "stale-oauth", "chatgpt_account_id": "stale-workspace",
		}},
		Endpoint: protocol.CodexEndpointTurnCosts, EntryProtocol: "openai", Method: http.MethodPost,
		Headers: http.Header{
			"OpenAI-Organization": {"org-1"}, "OpenAI-Project": {"proj-1"},
			"X-Codex-Routing-Hint": {"leak"}, "Cookie": {"leak"},
			"Authorization": {"Bearer caller"}, "Content-Type": {"application/json"},
		},
	})
	if result.BuildErr != nil || !mgr.called {
		t.Fatalf("turn-cost execution failed: result=%+v called=%v", result, mgr.called)
	}
	if mgr.req.Credential.AccessToken != "" || mgr.req.Credential.APIKey != "sk-selected" {
		t.Fatalf("credential family leaked/mis-selected: %+v", mgr.req.Credential)
	}
	if mgr.req.Credential.AccountID != "" {
		t.Fatalf("API-key lease leaked ChatGPT account id %q", mgr.req.Credential.AccountID)
	}
	projected := headerFromValues(mgr.req.Header)
	if projected.Get("OpenAI-Organization") != "org-1" || projected.Get("OpenAI-Project") != "proj-1" {
		t.Fatalf("provider scope headers missing: %#v", mgr.req.Header)
	}
	for _, name := range []string{"X-Codex-Routing-Hint", "Cookie", "Authorization"} {
		if projected.Get(name) != "" {
			t.Fatalf("header %s crossed turn-cost boundary: %#v", name, mgr.req.Header)
		}
	}
}

func TestCodexPluginTransportTurnCostsRejectsOAuth(t *testing.T) {
	mgr := &fakeCodexManager{}
	result := NewCodexPluginTransport(mgr).Execute(context.Background(), Request{
		Account:  Account{Platform: "codex", Type: "oauth", Credentials: map[string]string{"access_token": "oauth"}},
		Endpoint: protocol.CodexEndpointTurnCosts, EntryProtocol: "openai", Method: http.MethodPost,
	})
	if !errors.Is(result.BuildErr, ErrCodexPluginUnsupported) || mgr.called {
		t.Fatalf("OAuth turn-cost request was not rejected: result=%+v called=%v", result, mgr.called)
	}
}

func TestCodexPluginTransportDefersHeadersUntilData(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	mgr := &fakeCodexManager{events: []protocol.CodexExecuteEvent{
		{Type: protocol.CodexEventResponseHeaders, StatusCode: 200, Header: map[string][]string{"Content-Type": {"text/event-stream"}}},
		{Type: protocol.CodexEventError, Error: &protocol.CodexExecutorError{Code: "unsupported_capability", Phase: "before_headers"}},
	}}
	result := NewCodexPluginTransport(mgr).Execute(context.Background(), Request{Account: Account{Platform: "codex", Type: "oauth"}, EntryProtocol: "openai", Endpoint: "responses", Stream: true, LegacyContext: c})
	if !errors.Is(result.BuildErr, ErrCodexPluginUnsupported) || result.Written || recorder.Code != 200 || recorder.Body.Len() != 0 {
		t.Fatalf("result=%+v recorder=%+v", result, recorder)
	}
}

func TestCodexPluginTransportStripsConnectionNominatedResponseHeaders(t *testing.T) {
	mgr := &fakeCodexManager{events: []protocol.CodexExecuteEvent{
		{Type: protocol.CodexEventResponseHeaders, StatusCode: http.StatusOK, Header: map[string][]string{
			"Content-Type":      {"application/json"},
			"Connection":        {"keep-alive, X-Hop-By-Hop"},
			"X-Hop-By-Hop":      {"must-not-leak"},
			"X-End-To-End":      {"preserved"},
			"Transfer-Encoding": {"chunked"},
		}},
		{Type: protocol.CodexEventData, Data: []byte(`{"ok":true}`)},
		{Type: protocol.CodexEventCompleted},
	}}
	result := NewCodexPluginTransport(mgr).Execute(context.Background(), Request{
		Account: Account{Platform: "codex", Type: "oauth"}, EntryProtocol: "openai", Endpoint: "responses",
	})
	if got := result.Headers.Get("X-Hop-By-Hop"); got != "" {
		t.Fatalf("Connection-nominated response header leaked: %q", got)
	}
	if got := result.Headers.Get("X-End-To-End"); got != "preserved" {
		t.Fatalf("end-to-end response header = %q, want preserved", got)
	}
	for _, name := range []string{"Connection", "Transfer-Encoding"} {
		if got := result.Headers.Get(name); got != "" {
			t.Fatalf("hop-by-hop response header %s leaked: %q", name, got)
		}
	}
}

func TestCodexPluginTransportMergesCredentialUpdate(t *testing.T) {
	mgr := &fakeCodexManager{events: []protocol.CodexExecuteEvent{{Type: protocol.CodexEventCredentialUpdate, CredentialUpdate: &protocol.CodexCredentialUpdate{AccessToken: "new", RefreshToken: "new-refresh", SessionToken: "session-new", AccountID: "acct-new", ExpiresAt: 123}}, {Type: protocol.CodexEventCompleted}}}
	result := NewCodexPluginTransport(mgr).Execute(context.Background(), Request{Account: Account{Platform: "codex", Type: "oauth", Credentials: map[string]string{"access_token": "old", "email": "kept", "session_token": "session-old", "codex_mode": "native_only", "preferred_mode": "native_only", "transport_mode": "native_only"}}, EntryProtocol: "openai", Endpoint: "responses"})
	if result.RefreshedCredentials["access_token"] != "new" || result.RefreshedCredentials["refresh_token"] != "new-refresh" || result.RefreshedCredentials["session_token"] != "session-new" || result.RefreshedCredentials["chatgpt_account_id"] != "acct-new" || result.RefreshedCredentials["email"] != "kept" || result.RefreshedCredentials["expires_at"] != "123" || result.RefreshedCredentials["expired"] != "1970-01-01T00:02:03Z" {
		t.Fatalf("credentials=%+v", result.RefreshedCredentials)
	}
	for _, key := range []string{"codex_mode", "preferred_mode", "transport_mode"} {
		if _, exists := result.RefreshedCredentials[key]; exists {
			t.Fatalf("retired routing key persisted: %q", key)
		}
	}
}

func TestCodexPluginTransportOnlyUsesDedicatedChatGPTAccountID(t *testing.T) {
	mgr := &fakeCodexManager{events: []protocol.CodexExecuteEvent{{Type: protocol.CodexEventCompleted}}}
	transport := NewCodexPluginTransport(mgr)
	base := Request{Account: Account{ID: 99, Platform: "codex", Type: "oauth", Credentials: map[string]string{"account_id": "generic"}}, EntryProtocol: "openai", Endpoint: "responses"}
	transport.Execute(context.Background(), base)
	if mgr.req.Credential.AccountID != "" {
		t.Fatalf("generic/local account id leaked: %q", mgr.req.Credential.AccountID)
	}
	base.Account.Credentials["chatgpt_account_id"] = "chatgpt-specific"
	transport.Execute(context.Background(), base)
	if mgr.req.Credential.AccountID != "chatgpt-specific" {
		t.Fatalf("account id=%q", mgr.req.Credential.AccountID)
	}
}

func TestCodexPluginTransportPassesFedRAMPAccountFlag(t *testing.T) {
	mgr := &fakeCodexManager{events: []protocol.CodexExecuteEvent{{Type: protocol.CodexEventCompleted}}}
	transport := NewCodexPluginTransport(mgr)
	transport.Execute(context.Background(), Request{Account: Account{
		ID: 7, Platform: "codex", Type: "oauth",
		Credentials: map[string]string{"access_token": "token", "chatgpt_account_is_fedramp": "true"},
	}, EntryProtocol: "openai", Endpoint: "responses"})
	if !mgr.req.Credential.ChatGPTAccountIsFedramp {
		t.Fatalf("fedramp flag was not passed: %#v", mgr.req.Credential)
	}
}

func TestCodexPluginTransportFillsCodexOAuthDefaults(t *testing.T) {
	mgr := &fakeCodexManager{events: []protocol.CodexExecuteEvent{{Type: protocol.CodexEventCompleted}}}
	NewCodexPluginTransport(mgr).Execute(context.Background(), Request{Account: Account{Platform: "codex", Type: "oauth", Credentials: map[string]string{"refresh_token": "refresh"}}, EntryProtocol: "openai", Endpoint: "responses"})
	if mgr.req.Credential.TokenURL != defaultCodexOAuthTokenURL || mgr.req.Credential.ClientID != defaultCodexOAuthClientID {
		t.Fatalf("credential=%+v", mgr.req.Credential)
	}
	if mgr.req.Credential.SessionURL != "" || mgr.req.Credential.SessionToken != "" {
		t.Fatalf("session fields should be omitted without a session import: %+v", mgr.req.Credential)
	}

	mgr = &fakeCodexManager{events: []protocol.CodexExecuteEvent{{Type: protocol.CodexEventCompleted}}}
	NewCodexPluginTransport(mgr).Execute(context.Background(), Request{Account: Account{Platform: "codex", Type: "oauth", Credentials: map[string]string{"session_token": "session", "session_url": "https://chatgpt.example/session"}}, EntryProtocol: "openai", Endpoint: "responses"})
	if mgr.req.Credential.SessionToken != "session" || mgr.req.Credential.SessionURL != "https://chatgpt.example/session" {
		t.Fatalf("session lease was not propagated: %+v", mgr.req.Credential)
	}
}

type fakeUpstreamAuditSink struct {
	request UpstreamAuditRequest
	attempt *fakeUpstreamAuditAttempt
	err     error
}

func (s *fakeUpstreamAuditSink) BeginUpstreamAttempt(_ context.Context, request UpstreamAuditRequest) (UpstreamAuditAttempt, error) {
	s.request = request
	if s.err != nil {
		return nil, s.err
	}
	s.attempt = &fakeUpstreamAuditAttempt{}
	return s.attempt, nil
}

type fakeUpstreamAuditAttempt struct{ result UpstreamAuditResult }

func (a *fakeUpstreamAuditAttempt) FinishUpstreamAttempt(result UpstreamAuditResult) {
	a.result = result
}

func TestCodexPluginTransportCapturesNativeAuditEventsWithoutCredentialHeaders(t *testing.T) {
	sink := &fakeUpstreamAuditSink{}
	mgr := &fakeCodexManager{events: []protocol.CodexExecuteEvent{
		{Type: protocol.CodexEventAuditRequest, Audit: &protocol.CodexAuditEvent{
			AttemptID: "req:native:1", Method: http.MethodPost, URL: "https://example.test/responses?access_token=%5Bredacted%5D",
			Header: map[string][]string{"Authorization": {"Bearer secret"}, "X-Codex-Test": {"ok"}},
		}, Data: []byte(`{"input":"hello"}`)},
		{Type: protocol.CodexEventAuditResult, Audit: &protocol.CodexAuditEvent{
			AttemptID: "req:native:1", StatusCode: 200, LatencyMs: 12, ResponseStarted: true, StreamCompleted: true,
		}},
		{Type: protocol.CodexEventResponseHeaders, StatusCode: 200},
		{Type: protocol.CodexEventData, Data: []byte(`{"ok":true}`)},
		{Type: protocol.CodexEventCompleted},
	}}
	result := NewCodexPluginTransport(mgr).Execute(context.Background(), Request{
		Account: Account{Platform: "codex", Type: "oauth"}, RequestID: "req", Endpoint: "responses", EntryProtocol: "openai", UpstreamAudit: sink,
	})
	if result.BuildErr != nil || sink.attempt == nil {
		t.Fatalf("audit capture failed: result=%+v sink=%+v", result, sink)
	}
	if sink.request.Headers.Get("Authorization") != "" || sink.request.Headers.Get("X-Codex-Test") != "ok" {
		t.Fatalf("credential header crossed audit boundary: %#v", sink.request.Headers)
	}
	if string(sink.request.Body) != `{"input":"hello"}` || sink.request.URL == "" {
		t.Fatalf("audit request payload missing: %+v", sink.request)
	}
	if sink.attempt.result.StatusCode != 200 || !sink.attempt.result.StreamCompleted || sink.attempt.result.LatencyMs != 12 {
		t.Fatalf("audit result missing: %+v", sink.attempt.result)
	}
}

func TestCodexPluginTransportAuditBeginFailureBlocksNativeResult(t *testing.T) {
	sink := &fakeUpstreamAuditSink{err: errors.New("audit unavailable")}
	mgr := &fakeCodexManager{events: []protocol.CodexExecuteEvent{{Type: protocol.CodexEventAuditRequest, Audit: &protocol.CodexAuditEvent{
		AttemptID: "req:native:1", Method: http.MethodPost, URL: "https://example.test/responses",
	}}}}
	result := NewCodexPluginTransport(mgr).Execute(context.Background(), Request{Account: Account{Platform: "codex", Type: "oauth"}, Endpoint: "responses", EntryProtocol: "openai", UpstreamAudit: sink})
	if result.BuildErr == nil || !strings.Contains(result.BuildErr.Error(), "audit") {
		t.Fatalf("audit failure was not propagated: %+v", result)
	}
}

func TestCodexPluginTransportErrorWithSuccessStatusIsNetworkError(t *testing.T) {
	mgr := &fakeCodexManager{events: []protocol.CodexExecuteEvent{{Type: protocol.CodexEventResponseHeaders, StatusCode: 200}, {Type: protocol.CodexEventError, Error: &protocol.CodexExecutorError{Code: "decode_failed", UpstreamStatus: 200}}}}
	result := NewCodexPluginTransport(mgr).Execute(context.Background(), Request{Account: Account{Platform: "codex", Type: "oauth"}, EntryProtocol: "openai", Endpoint: "responses"})
	if result.StatusCode != 200 || result.NetErr == nil || result.BuildErr != nil || len(result.Body) != 0 {
		t.Fatalf("result=%+v", result)
	}
}

func TestCodexPluginTransportResponseHeadersMarkReplayBoundaryEvenWhenEmpty(t *testing.T) {
	// A legacy/third-party executor may emit a response_headers event without
	// populating status or headers. That event still proves the provider request
	// reached its response boundary; Core must not classify the following error
	// as a pre-header failure eligible for CPA replay.
	mgr := &fakeCodexManager{events: []protocol.CodexExecuteEvent{
		{Type: protocol.CodexEventResponseHeaders},
		{Type: protocol.CodexEventError, Error: &protocol.CodexExecutorError{Code: "read_failed", Phase: "before_headers", Retryable: true}},
	}}
	result := NewCodexPluginTransport(mgr).Execute(context.Background(), Request{
		Account: Account{Platform: "codex", Type: "oauth"}, Endpoint: "responses", EntryProtocol: "openai",
	})
	if !result.ResponseStarted {
		t.Fatalf("empty response_headers event did not mark response boundary: %+v", result)
	}
	if result.BuildErr != nil || result.NetErr == nil {
		t.Fatalf("empty response_headers failure was classified as build error: %+v", result)
	}
}

func TestCodexPluginTransportSerializesConcurrentEvents(t *testing.T) {
	result := NewCodexPluginTransport(concurrentCodexManager{}).Execute(context.Background(), Request{
		Account: Account{Platform: "codex", Type: "oauth"}, Endpoint: "responses", EntryProtocol: "openai",
	})
	if result.NetErr != nil || result.BuildErr != nil || !result.ResponseStarted || !result.DataReceived || len(result.Body) != 64 {
		t.Fatalf("concurrent events were not safely consumed: %+v", result)
	}
}

func TestCodexPluginTransportPreservesCachedHTTPErrorBody(t *testing.T) {
	mgr := &fakeCodexManager{events: []protocol.CodexExecuteEvent{{Type: protocol.CodexEventResponseHeaders, StatusCode: 429}, {Type: protocol.CodexEventData, Data: []byte(`{"error":"rate limited"}`)}, {Type: protocol.CodexEventError, Error: &protocol.CodexExecutorError{Code: "rate_limit", UpstreamStatus: 429}}}}
	result := NewCodexPluginTransport(mgr).Execute(context.Background(), Request{Account: Account{Platform: "codex", Type: "oauth"}, EntryProtocol: "openai", Endpoint: "responses", Stream: true})
	if string(result.Body) != `{"error":"rate limited"}` || result.StatusCode != 429 {
		t.Fatalf("result=%+v", result)
	}
}

func TestCodexPluginTransportNonStream(t *testing.T) {
	mgr := &fakeCodexManager{events: []protocol.CodexExecuteEvent{
		{Type: protocol.CodexEventResponseHeaders, StatusCode: 200, Header: map[string][]string{"Content-Type": {"application/json"}}},
		{Type: protocol.CodexEventData, Data: []byte(`{"id":"resp"}`)},
		{Type: protocol.CodexEventUsage, Usage: &protocol.CodexUsage{InputTokens: 12, CachedInputTokens: 3, OutputTokens: 4}},
		{Type: protocol.CodexEventCompleted},
	}}
	result := NewCodexPluginTransport(mgr).Execute(context.Background(), Request{
		Account:   Account{ID: 9, Platform: "codex", Type: "oauth", Credentials: map[string]string{"access_token": "token"}},
		RequestID: "req-1", GroupID: 7, Method: "POST", BaseURL: "https://example.test/v1", Path: "/responses",
		Model: "gpt-test", Endpoint: "responses", EntryProtocol: "openai", Payload: []byte(`{"model":"gpt-test"}`),
	})
	if result.StatusCode != 200 || string(result.Body) != `{"id":"resp"}` || !result.Done || result.Written {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.Usage == nil || result.Usage.PromptTokens != 12 || result.Usage.CachedTokens != 3 || result.Usage.CompletionTokens != 4 {
		t.Fatalf("unexpected usage: %+v", result.Usage)
	}
	if mgr.req.Credential.AccessToken != "token" || mgr.req.Path != "/responses" || mgr.req.GroupID != 7 {
		t.Fatalf("unexpected plugin request: %+v", mgr.req)
	}
}

func TestCodexPluginTransportCapsBufferedResponseCumulatively(t *testing.T) {
	first := make([]byte, maxCodexBufferedResponseBytes)
	mgr := &fakeCodexManager{events: []protocol.CodexExecuteEvent{
		{Type: protocol.CodexEventResponseHeaders, StatusCode: http.StatusOK},
		{Type: protocol.CodexEventData, Data: first},
		{Type: protocol.CodexEventData, Data: []byte{'x'}},
		{Type: protocol.CodexEventCompleted},
	}}
	result := NewCodexPluginTransport(mgr).Execute(context.Background(), Request{
		Account: Account{Platform: "codex", Type: "oauth"}, Endpoint: "responses", EntryProtocol: "openai",
	})
	if result.BuildErr != nil || !errors.Is(result.NetErr, errCodexBufferedResponseTooLarge) {
		t.Fatalf("result errors: build=%v net=%v", result.BuildErr, result.NetErr)
	}
	if !result.DataReceived || len(result.Body) != 0 || result.Done {
		t.Fatalf("oversized response was retained or completed: %+v", result)
	}
}

func TestCodexPluginTransportStreamsWithoutBuffering(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	mgr := &fakeCodexManager{events: []protocol.CodexExecuteEvent{
		{Type: protocol.CodexEventResponseHeaders, StatusCode: 200, Header: map[string][]string{"Content-Type": {"text/event-stream"}}},
		{Type: protocol.CodexEventData, Data: []byte("data: one\n\n")},
		{Type: protocol.CodexEventCompleted},
	}}
	result := NewCodexPluginTransport(mgr).Execute(context.Background(), Request{Account: Account{Platform: "codex", Type: "oauth"}, Endpoint: "responses", EntryProtocol: "openai", Stream: true, LegacyContext: c})
	if !result.Written || !result.Done || recorder.Body.String() != "data: one\n\n" {
		t.Fatalf("unexpected stream result=%+v body=%q", result, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content type = %q", got)
	}
}

func TestCodexPluginTransportUnavailable(t *testing.T) {
	mgr := &fakeCodexManager{err: pluginruntime.ErrCodexExecutorUnavailable}
	result := NewCodexPluginTransport(mgr).Execute(context.Background(), Request{Account: Account{Platform: "codex", Type: "oauth"}, Endpoint: "responses", EntryProtocol: "openai"})
	if !errors.Is(result.BuildErr, ErrCodexPluginUnavailable) {
		t.Fatalf("build error = %v", result.BuildErr)
	}
}
