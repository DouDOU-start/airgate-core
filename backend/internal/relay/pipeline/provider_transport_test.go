package pipeline

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/errlog"
	"github.com/DouDOU-start/airgate-core/internal/pluginruntime"
	"github.com/DouDOU-start/airgate-core/internal/pluginruntime/protocol"
	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/cpa"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
	"github.com/DouDOU-start/airgate-core/internal/relay/pricing"
	providertransport "github.com/DouDOU-start/airgate-core/internal/relay/transport"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"
)

type fixedProviderTransport struct{ result providertransport.Result }

func (f fixedProviderTransport) Execute(context.Context, providertransport.Request) providertransport.Result {
	return f.result
}

type staticCodexPolicy string

func (s staticCodexPolicy) CodexTransportMode() string { return string(s) }

type countingProviderTransport struct {
	result providertransport.Result
	calls  int
	mode   string
}

func (f *countingProviderTransport) Execute(context.Context, providertransport.Request) providertransport.Result {
	f.calls++
	return f.result
}

func (f *countingProviderTransport) CodexTransportMode() string { return f.mode }

type countingLegacyForwarder struct{ calls int }

func (f *countingLegacyForwarder) Forward(context.Context, *gin.Context, cpa.ForwardRequest) cpa.ForwardResult {
	f.calls++
	return cpa.ForwardResult{StatusCode: 204, Done: true}
}

type capturingLegacyForwarder struct {
	calls   int
	request cpa.ForwardRequest
	result  cpa.ForwardResult
}

func (f *capturingLegacyForwarder) Forward(_ context.Context, _ *gin.Context, req cpa.ForwardRequest) cpa.ForwardResult {
	f.calls++
	f.request = req
	return f.result
}

type scriptedCodexManager struct {
	events  []protocol.CodexExecuteEvent
	err     error
	calls   int
	mode    string
	request protocol.CodexExecuteRequest
}

func (m *scriptedCodexManager) CodexTransportMode() string { return m.mode }

func (m *scriptedCodexManager) ExecuteCodex(_ context.Context, request protocol.CodexExecuteRequest, emit func(protocol.CodexExecuteEvent) error) error {
	m.calls++
	m.request = request
	for _, event := range m.events {
		if err := emit(event); err != nil {
			return err
		}
	}
	return m.err
}

func TestExecuteProviderPropagatesCodexClientToNativeExecutor(t *testing.T) {
	mgr := &scriptedCodexManager{events: []protocol.CodexExecuteEvent{{Type: protocol.CodexEventCompleted}}}
	pipe := &Pipeline{providerTransport: providertransport.NewCodexPluginTransport(mgr)}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("Originator", "codex_cli_rs")

	result := pipe.executeProvider(context.Background(), c, codexForwardRequest("", ""))
	if result.BuildErr != nil || mgr.calls != 1 {
		t.Fatalf("result=%+v plugin_calls=%d", result, mgr.calls)
	}
	if mgr.request.Client != "codex" {
		t.Fatalf("executor client = %q, want codex", mgr.request.Client)
	}
}

func codexForwardRequest(modeKey, modeValue string) cpa.ForwardRequest {
	// Routing policy is plugin-scoped. Keep the helper parameters for call-site
	// compatibility while deliberately ignoring the retired account fields.
	_ = modeKey
	_ = modeValue
	credentials := map[string]string{"access_token": "token"}
	return cpa.ForwardRequest{Account: cpa.AccountAuthInput{AccountID: 42, Platform: "codex", Type: "oauth", Credentials: credentials}, Model: "gpt-5", Endpoint: "responses", EntryProtocol: "openai", Stream: true, Payload: []byte(`{"model":"gpt-5","stream":true}`)}
}

func TestExecuteProviderNativeResponsesUsesPlugin(t *testing.T) {
	mgr := &scriptedCodexManager{events: []protocol.CodexExecuteEvent{{Type: protocol.CodexEventResponseHeaders, StatusCode: 200}, {Type: protocol.CodexEventData, Data: []byte("data: ok\n\n")}, {Type: protocol.CodexEventCompleted}}}
	legacy := &countingLegacyForwarder{}
	p := &Pipeline{providerTransport: providertransport.NewCodexPluginTransport(mgr), cpa: legacy}
	result := p.executeProvider(context.Background(), nil, codexForwardRequest("", ""))
	if result.StatusCode != 200 || !result.Done || legacy.calls != 0 {
		t.Fatalf("result=%+v cpa_calls=%d", result, legacy.calls)
	}
}

func TestExecuteProviderCodexModesForceCPA(t *testing.T) {
	for _, value := range []string{"cpa_translate", "cpa_only"} {
		mgr := &scriptedCodexManager{mode: value}
		legacy := &countingLegacyForwarder{}
		p := &Pipeline{providerTransport: providertransport.NewCodexPluginTransport(mgr), cpa: legacy}
		result := p.executeProvider(context.Background(), nil, codexForwardRequest("", ""))
		if legacy.calls != 1 || mgr.calls != 0 || result.StatusCode != 204 {
			t.Fatalf("mode=%s result=%+v cpa_calls=%d plugin_calls=%d", value, result, legacy.calls, mgr.calls)
		}
	}
}

func TestExecuteProviderCodexImageEditJSONKeepsCPAContract(t *testing.T) {
	for _, mode := range []string{"auto", "native", "native_only", "cpa_translate", "cpa_only"} {
		t.Run(mode, func(t *testing.T) {
			legacy := &capturingLegacyForwarder{result: cpa.ForwardResult{StatusCode: http.StatusOK, Done: true}}
			p := &Pipeline{cpa: legacy, codexTransportPolicy: staticCodexPolicy(mode)}
			payload := []byte(`{"model":"gpt-image-public","prompt":"edit",` +
				`"images":[{"image_url":"data:image/png;base64,QUJD"}]}`)
			req := codexForwardRequest("", "")
			req.Model = "gpt-image-public"
			req.UpstreamModel = "gpt-image-upstream"
			req.Endpoint = "images_edits"
			req.Stream = false
			req.Payload = payload
			req.Headers = http.Header{"Content-Type": []string{"application/json"}}

			result := p.executeProvider(context.Background(), nil, req)
			if result.StatusCode != http.StatusOK || legacy.calls != 1 {
				t.Fatalf("result=%+v CPA calls=%d", result, legacy.calls)
			}
			if string(legacy.request.Payload) != string(payload) || legacy.request.RawBody != nil {
				t.Fatalf("CPA JSON payload changed: payload=%s raw=%q", legacy.request.Payload, legacy.request.RawBody)
			}
			if legacy.request.Endpoint != "images_edits" || legacy.request.EntryProtocol != "openai" {
				t.Fatalf("CPA image contract metadata = endpoint:%q protocol:%q", legacy.request.Endpoint, legacy.request.EntryProtocol)
			}
			if legacy.request.UpstreamModel != "gpt-image-upstream" || legacy.request.Headers.Get("Content-Type") != "application/json" {
				t.Fatalf("CPA image routing metadata = model:%q content-type:%q", legacy.request.UpstreamModel, legacy.request.Headers.Get("Content-Type"))
			}
		})
	}
}

func TestExecuteProviderCPAForcedModesBypassNativeTransport(t *testing.T) {
	for _, mode := range []string{"cpa_translate", "cpa_only"} {
		t.Run(mode, func(t *testing.T) {
			native := &countingProviderTransport{result: providertransport.Result{StatusCode: http.StatusCreated, Done: true}, mode: mode}
			legacy := &countingLegacyForwarder{}
			p := &Pipeline{providerTransport: native, cpa: legacy}
			result := p.executeProvider(context.Background(), nil, codexForwardRequest("", ""))
			if native.calls != 0 || legacy.calls != 1 || result.StatusCode != http.StatusNoContent {
				t.Fatalf("mode=%s result=%+v native_calls=%d cpa_calls=%d", mode, result, native.calls, legacy.calls)
			}
		})
	}
}

func TestExecuteProviderCPATranslateKeepsNativeOnlyContractsOnPlugin(t *testing.T) {
	for _, endpoint := range []string{"compact", "alpha_search"} {
		t.Run(endpoint, func(t *testing.T) {
			mgr := &scriptedCodexManager{mode: "cpa_translate", events: []protocol.CodexExecuteEvent{
				{Type: protocol.CodexEventResponseHeaders, StatusCode: 200},
				{Type: protocol.CodexEventData, Data: []byte(`{"ok":true}`)},
				{Type: protocol.CodexEventCompleted},
			}}
			legacy := &countingLegacyForwarder{}
			p := &Pipeline{providerTransport: providertransport.NewCodexPluginTransport(mgr), cpa: legacy}
			req := codexForwardRequest("", "")
			req.Endpoint = endpoint
			req.Stream = false
			result := p.executeProvider(context.Background(), nil, req)
			if legacy.calls != 0 || mgr.calls != 1 || result.StatusCode != 200 || !result.Done {
				t.Fatalf("endpoint=%s result=%+v cpa_calls=%d plugin_calls=%d", endpoint, result, legacy.calls, mgr.calls)
			}
		})
	}
}

func TestExecuteProviderCPAOnlyFailsClosedForNativeOnlyContracts(t *testing.T) {
	for _, test := range []struct {
		endpoint string
		wantErr  error
	}{
		{endpoint: "compact", wantErr: cpa.ErrCompactUnsupported},
		{endpoint: "alpha_search", wantErr: providertransport.ErrCodexCPAUnsupported},
		{endpoint: "predict", wantErr: providertransport.ErrCodexCPAUnsupported},
	} {
		t.Run(test.endpoint, func(t *testing.T) {
			mgr := &scriptedCodexManager{mode: "cpa_only"}
			legacy := &countingLegacyForwarder{}
			p := &Pipeline{providerTransport: providertransport.NewCodexPluginTransport(mgr), cpa: legacy}
			req := codexForwardRequest("", "")
			req.Endpoint = test.endpoint
			result := p.executeProvider(context.Background(), nil, req)
			if legacy.calls != 0 || mgr.calls != 0 || !errors.Is(result.BuildErr, test.wantErr) {
				t.Fatalf("endpoint=%s result=%+v cpa_calls=%d plugin_calls=%d", test.endpoint, result, legacy.calls, mgr.calls)
			}
		})
	}
}

func TestExecuteProviderCPAOnlyFailsClosedWithoutProviderTransport(t *testing.T) {
	legacy := &countingLegacyForwarder{}
	p := &Pipeline{cpa: legacy, codexTransportPolicy: staticCodexPolicy("cpa_only")}
	req := codexForwardRequest("", "")
	req.Endpoint = "alpha_search"
	result := p.executeProvider(context.Background(), nil, req)
	if legacy.calls != 0 || !errors.Is(result.BuildErr, providertransport.ErrCodexCPAUnsupported) {
		t.Fatalf("result=%+v cpa_calls=%d", result, legacy.calls)
	}
}

func TestExecuteProviderCrossProtocolCodexAccountAlwaysUsesCPA(t *testing.T) {
	for _, test := range []struct {
		name          string
		entryProtocol string
		endpoint      string
	}{
		{name: "anthropic messages", entryProtocol: "anthropic", endpoint: "messages"},
		{name: "gemini generate content", entryProtocol: "gemini", endpoint: "generate_content"},
		{name: "openai chat completions", entryProtocol: "openai", endpoint: "chat_completions"},
	} {
		for _, mode := range []string{"auto", "native", "native_only", "cpa_translate", "cpa_only"} {
			t.Run(test.name+"/"+mode, func(t *testing.T) {
				native := &countingProviderTransport{result: providertransport.Result{StatusCode: http.StatusCreated, Done: true}, mode: mode}
				legacy := &countingLegacyForwarder{}
				p := &Pipeline{providerTransport: native, cpa: legacy}
				req := codexForwardRequest("", "")
				req.EntryProtocol = test.entryProtocol
				req.Endpoint = test.endpoint
				result := p.executeProvider(context.Background(), nil, req)
				if legacy.calls != 1 || native.calls != 0 || result.StatusCode != http.StatusNoContent {
					t.Fatalf("result=%+v cpa_calls=%d native_calls=%d", result, legacy.calls, native.calls)
				}
			})
		}
	}
}

func TestExecuteProviderCrossProtocolNativeOnlyFailsAsCPAUnavailable(t *testing.T) {
	native := &countingProviderTransport{result: providertransport.Result{StatusCode: http.StatusCreated, Done: true}, mode: "native_only"}
	p := &Pipeline{providerTransport: native}
	req := codexForwardRequest("", "")
	req.EntryProtocol = "anthropic"
	req.Endpoint = "messages"

	result := p.executeProvider(context.Background(), nil, req)
	if native.calls != 0 || !errors.Is(result.BuildErr, errCPAUnavailable) {
		t.Fatalf("result=%+v native_calls=%d", result, native.calls)
	}
}

func TestExecuteProviderCrossProtocolUsesTranslationProviderWhenLegacyCPAFieldIsNil(t *testing.T) {
	legacy := &countingLegacyForwarder{}
	translation := providertransport.NewCPAAdapter(legacy)
	p := &Pipeline{providerTransport: translation, codexTransportPolicy: staticCodexPolicy("native_only")}
	req := codexForwardRequest("", "")
	req.EntryProtocol = "gemini"
	req.Endpoint = "generate_content"

	result := p.executeProvider(context.Background(), nil, req)
	if legacy.calls != 1 || result.StatusCode != http.StatusNoContent {
		t.Fatalf("result=%+v cpa_calls=%d", result, legacy.calls)
	}
}

func TestExecuteProviderUnavailableBeforeDataFallsBackCPA(t *testing.T) {
	legacy := &countingLegacyForwarder{}
	p := &Pipeline{providerTransport: providertransport.NewCodexPluginTransport(&scriptedCodexManager{err: pluginruntime.ErrCodexExecutorUnavailable}), cpa: legacy}
	result := p.executeProvider(context.Background(), nil, codexForwardRequest("", ""))
	if legacy.calls != 1 || result.StatusCode != 204 {
		t.Fatalf("result=%+v calls=%d", result, legacy.calls)
	}
}

func TestExecuteProviderRefreshesCredentialsBeforeCPAFallback(t *testing.T) {
	legacy := &capturingLegacyForwarder{
		result: cpa.ForwardResult{
			StatusCode: http.StatusOK,
			Done:       true,
			RefreshedCredentials: map[string]string{
				"access_token": "cpa-token",
			},
		},
	}
	mgr := &scriptedCodexManager{
		events: []protocol.CodexExecuteEvent{
			{
				Type: protocol.CodexEventCredentialUpdate,
				CredentialUpdate: &protocol.CodexCredentialUpdate{
					AccessToken:  "native-refreshed-token",
					RefreshToken: "native-refreshed-refresh-token",
					ExpiresAt:    1900000000,
				},
			},
		},
		err: pluginruntime.ErrCodexExecutorUnavailable,
	}
	p := &Pipeline{
		providerTransport: providertransport.NewCodexPluginTransport(mgr),
		cpa:               legacy,
	}
	req := codexForwardRequest("", "")
	req.Account.Credentials["refresh_token"] = "old-refresh-token"

	result := p.executeProvider(context.Background(), nil, req)
	if legacy.calls != 1 {
		t.Fatalf("CPA calls=%d, want 1; result=%+v", legacy.calls, result)
	}
	if got := legacy.request.Account.Credentials["access_token"]; got != "native-refreshed-token" {
		t.Fatalf("CPA received access_token=%q, want native-refreshed-token", got)
	}
	if got := legacy.request.Account.Credentials["refresh_token"]; got != "native-refreshed-refresh-token" {
		t.Fatalf("CPA received refresh_token=%q, want native-refreshed-refresh-token", got)
	}
	if got := result.RefreshedCredentials["access_token"]; got != "cpa-token" {
		t.Fatalf("merged access_token=%q, want cpa-token", got)
	}
	if got := result.RefreshedCredentials["refresh_token"]; got != "native-refreshed-refresh-token" {
		t.Fatalf("merged refresh_token=%q, want native-refreshed-refresh-token", got)
	}
	if got := result.RefreshedCredentials["expires_at"]; got != "1900000000" {
		t.Fatalf("merged expires_at=%q, want native expiry", got)
	}
}

func TestExecuteProviderNativeOnlyNeverFallbackCPAButNativeMay(t *testing.T) {
	for _, mode := range []string{"native_only"} {
		t.Run(mode, func(t *testing.T) {
			mgr := &scriptedCodexManager{mode: mode, err: pluginruntime.ErrCodexExecutorUnavailable}
			legacy := &countingLegacyForwarder{}
			p := &Pipeline{providerTransport: providertransport.NewCodexPluginTransport(mgr), cpa: legacy}
			result := p.executeProvider(context.Background(), nil, codexForwardRequest("", ""))
			if legacy.calls != 0 || mgr.calls != 1 || result.BuildErr != nil || !errors.Is(result.NetErr, providertransport.ErrCodexPluginUnavailable) {
				t.Fatalf("mode=%s result=%+v cpa_calls=%d plugin_calls=%d", mode, result, legacy.calls, mgr.calls)
			}
		})
	}
	legacy := &countingLegacyForwarder{}
	p := &Pipeline{providerTransport: providertransport.NewCodexPluginTransport(&scriptedCodexManager{mode: "native", err: pluginruntime.ErrCodexExecutorUnavailable}), cpa: legacy}
	result := p.executeProvider(context.Background(), nil, codexForwardRequest("", ""))
	if legacy.calls != 1 || result.StatusCode != http.StatusNoContent || result.BuildErr != nil {
		// The scripted legacy forwarder returns a normal 204 response; this
		// assertion documents that plain native remains eligible for safe CPA
		// fallback while native_only does not.
		t.Fatalf("native mode did not fall back safely: result=%+v calls=%d", result, legacy.calls)
	}
}

func TestExecuteProviderIgnoresCodexModeAliasesOnNonCodexAccounts(t *testing.T) {
	legacy := &countingLegacyForwarder{}
	p := &Pipeline{providerTransport: fixedProviderTransport{result: providertransport.Result{BuildErr: providertransport.ErrCodexPluginUnsupported}}, cpa: legacy}
	req := cpa.ForwardRequest{
		Account: cpa.AccountAuthInput{
			Platform: "anthropic",
			Credentials: map[string]string{
				"transport_mode": "native_only",
			},
		},
		Endpoint: "messages",
	}
	result := p.executeProvider(context.Background(), nil, req)
	if legacy.calls != 1 || result.StatusCode != 204 {
		t.Fatalf("result=%+v cpa_calls=%d", result, legacy.calls)
	}
}

func TestExecuteProviderCodexPluginLeavesNonCodexAccountsOnCPA(t *testing.T) {
	for _, test := range []struct {
		platform string
		endpoint string
	}{
		{platform: "xai", endpoint: "responses"},
		{platform: "xai", endpoint: "xai_videos_generations"},
		{platform: "xai", endpoint: "xai_videos_retrieve"},
		{platform: "openai-compatibility", endpoint: "images_generations"},
		{platform: "openai", endpoint: "responses"},
		{platform: "openai-codex", endpoint: "responses"},
		{platform: "openai_codex", endpoint: "responses"},
	} {
		for _, mode := range []string{"auto", "native", "native_only", "cpa_translate", "cpa_only"} {
			t.Run(test.platform+"/"+test.endpoint+"/"+mode, func(t *testing.T) {
				mgr := &scriptedCodexManager{mode: mode}
				legacy := &countingLegacyForwarder{}
				p := &Pipeline{providerTransport: providertransport.NewCodexPluginTransport(mgr), cpa: legacy}
				req := cpa.ForwardRequest{
					Account:       cpa.AccountAuthInput{Platform: test.platform},
					Model:         "provider-model",
					Endpoint:      test.endpoint,
					EntryProtocol: "openai",
					Payload:       []byte(`{"model":"provider-model"}`),
				}

				result := p.executeProvider(context.Background(), nil, req)
				if mgr.calls != 0 || legacy.calls != 1 || result.StatusCode != http.StatusNoContent {
					t.Fatalf("result=%+v plugin_calls=%d cpa_calls=%d", result, mgr.calls, legacy.calls)
				}
			})
		}
	}
}

func TestExecuteProviderBeforeHeadersNetworkErrorFallsBackCPA(t *testing.T) {
	legacy := &countingLegacyForwarder{}
	mgr := &scriptedCodexManager{events: []protocol.CodexExecuteEvent{{Type: protocol.CodexEventError, Error: &protocol.CodexExecutorError{Code: "connect_failed", Phase: "before_headers", Retryable: true}}}}
	p := &Pipeline{providerTransport: providertransport.NewCodexPluginTransport(mgr), cpa: legacy}
	result := p.executeProvider(context.Background(), nil, codexForwardRequest("", ""))
	if legacy.calls != 1 || result.StatusCode != 204 {
		t.Fatalf("result=%+v calls=%d", result, legacy.calls)
	}
}

func TestExecuteProviderAfterHeadersRetryableErrorDoesNotFallbackCPA(t *testing.T) {
	legacy := &countingLegacyForwarder{}
	mgr := &scriptedCodexManager{events: []protocol.CodexExecuteEvent{{Type: protocol.CodexEventResponseHeaders, StatusCode: 200}, {Type: protocol.CodexEventError, Error: &protocol.CodexExecutorError{Code: "read_failed", Phase: "after_headers", Retryable: true}}}}
	p := &Pipeline{providerTransport: providertransport.NewCodexPluginTransport(mgr), cpa: legacy}
	result := p.executeProvider(context.Background(), nil, codexForwardRequest("", ""))
	var pluginErr *providertransport.CodexPluginError
	if legacy.calls != 0 || result.StatusCode != 200 || result.BuildErr != nil || !errors.As(result.NetErr, &pluginErr) {
		t.Fatalf("result=%+v calls=%d", result, legacy.calls)
	}
}

func TestExecuteProviderEmptyHeadersNeverFallsBackCPA(t *testing.T) {
	legacy := &countingLegacyForwarder{}
	mgr := &scriptedCodexManager{events: []protocol.CodexExecuteEvent{
		{Type: protocol.CodexEventResponseHeaders},
		// Keep a contradictory phase to exercise the concrete response-boundary
		// marker rather than relying on the executor's advisory phase label.
		{Type: protocol.CodexEventError, Error: &protocol.CodexExecutorError{Code: "read_failed", Phase: "before_headers", Retryable: true}},
	}}
	p := &Pipeline{providerTransport: providertransport.NewCodexPluginTransport(mgr), cpa: legacy}
	result := p.executeProvider(context.Background(), nil, codexForwardRequest("", ""))
	var pluginErr *providertransport.CodexPluginError
	if legacy.calls != 0 || !result.ResponseStarted || result.BuildErr != nil || !errors.As(result.NetErr, &pluginErr) {
		t.Fatalf("empty response headers incorrectly replayed through CPA: result=%+v cpa_calls=%d", result, legacy.calls)
	}
}

func TestExecuteProviderObservedHeadersOverrideContradictoryBeforeHeadersError(t *testing.T) {
	legacy := &countingLegacyForwarder{}
	mgr := &scriptedCodexManager{events: []protocol.CodexExecuteEvent{
		{Type: protocol.CodexEventResponseHeaders, StatusCode: http.StatusOK, Header: map[string][]string{"X-Upstream-Request-ID": {"request-1"}}},
		{Type: protocol.CodexEventError, Error: &protocol.CodexExecutorError{
			Code: "read_failed", Phase: "before_headers", Retryable: true,
		}},
	}}
	p := &Pipeline{providerTransport: providertransport.NewCodexPluginTransport(mgr), cpa: legacy}

	result := p.executeProvider(context.Background(), nil, codexForwardRequest("", ""))

	var pluginErr *providertransport.CodexPluginError
	if legacy.calls != 0 || result.StatusCode != http.StatusOK || !errors.As(result.NetErr, &pluginErr) {
		t.Fatalf("contradictory phase replayed through CPA: result=%+v cpa_calls=%d", result, legacy.calls)
	}
}

func TestProviderInfrastructureBuildErrorsBecomeRetryableTransportErrors(t *testing.T) {
	for _, want := range []error{
		providertransport.ErrCodexPluginUnavailable,
		providertransport.ErrCodexPluginUnsupported,
	} {
		t.Run(want.Error(), func(t *testing.T) {
			result := providerResultToCPA(providertransport.Result{BuildErr: want})
			if result.BuildErr != nil || !errors.Is(result.NetErr, want) {
				t.Fatalf("result=%+v, want retryable transport error %v", result, want)
			}
		})
	}
}

func TestProviderInfrastructureErrorUsesTransientAccountOutcome(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	mapped := providerResultToCPA(providertransport.Result{BuildErr: providertransport.ErrCodexPluginUnavailable})
	result := attemptResult{buildErr: mapped.BuildErr, netErr: mapped.NetErr}
	p := &Pipeline{rpm: scheduler.NewRPMCounter(nil)}
	acc := &accountreg.Snapshot{ID: 17, Name: "codex", Platform: "codex", Type: "oauth"}
	req := &dto.ChatRequest{Model: "gpt-5", Stream: false}
	keyInfo := &auth.APIKeyInfo{}
	softExclude := make([]int, 0, 1)
	summary := failureSummary{}
	hops := make([]errlog.AttemptHop, 0)

	if done := p.handleAccountOutcome(c, keyInfo, acc, req, "responses", result, time.Now(), pricing.Price{}, GatewaySettings{}, forwardOptions{}, 1, 1, &hops, &summary, &[]int{}, &softExclude, 0, 0); done {
		t.Fatal("plugin infrastructure failure should remain eligible for account failover")
	}
	if recorder.Code != http.StatusOK || recorder.Body.Len() != 0 {
		t.Fatalf("plugin infrastructure failure was written as an HTTP response: code=%d body=%q", recorder.Code, recorder.Body.String())
	}
	if !summary.transient || len(softExclude) != 1 || softExclude[0] != acc.ID {
		t.Fatalf("outcome=%+v soft_exclude=%v, want transient failover", summary, softExclude)
	}
}

func TestExecuteProviderHTTPFailureNeverFallsBackCPA(t *testing.T) {
	legacy := &countingLegacyForwarder{}
	p := &Pipeline{providerTransport: fixedProviderTransport{result: providertransport.Result{StatusCode: 429, BuildErr: providertransport.ErrCodexPluginUnavailable}}, cpa: legacy}
	result := p.executeProvider(context.Background(), nil, codexForwardRequest("", ""))
	if legacy.calls != 0 || result.StatusCode != 429 {
		t.Fatalf("result=%+v calls=%d", result, legacy.calls)
	}
}

func TestExecuteProviderBufferedDataNeverFallsBackCPA(t *testing.T) {
	legacy := &countingLegacyForwarder{}
	p := &Pipeline{providerTransport: fixedProviderTransport{result: providertransport.Result{StatusCode: 200, DataReceived: true, Body: []byte("partial"), BuildErr: providertransport.ErrCodexPluginUnavailable}}, cpa: legacy}
	result := p.executeProvider(context.Background(), nil, codexForwardRequest("", ""))
	if legacy.calls != 0 || !result.DataReceived || string(result.Body) != "partial" {
		t.Fatalf("result=%+v calls=%d", result, legacy.calls)
	}
}

func TestProviderResultToCPADataReceivedPreserved(t *testing.T) {
	result := providerResultToCPA(providertransport.Result{
		StatusCode:   http.StatusOK,
		DataReceived: true,
		Body:         []byte("partial"),
		NetErr:       errors.New("read failed"),
	})
	if !result.DataReceived || string(result.Body) != "partial" || result.NetErr == nil {
		t.Fatalf("provider result lost buffered-data state: %+v", result)
	}
}

func TestHandleAccountOutcomeStopsAfterBufferedData(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	partialErr := errors.New("upstream read failed after data")
	result := attemptResult{
		statusCode:   http.StatusOK,
		dataReceived: true,
		netErr:       partialErr,
		body:         []byte(`{"partial":true}`),
	}
	p := &Pipeline{rpm: scheduler.NewRPMCounter(nil)}
	acc := &accountreg.Snapshot{ID: 17, Name: "codex", Platform: "codex", Type: "oauth"}
	req := &dto.ChatRequest{Model: "gpt-5", Stream: false}
	softExclude := make([]int, 0, 1)
	summary := failureSummary{}
	hops := make([]errlog.AttemptHop, 0)

	if done := p.handleAccountOutcome(c, &auth.APIKeyInfo{}, acc, req, "responses", result, time.Now(), pricing.Price{}, GatewaySettings{}, forwardOptions{}, 1, 1, &hops, &summary, &[]int{}, &softExclude, 0, 0); !done {
		t.Fatal("buffered provider data must terminate the request instead of failing over")
	}
	if len(softExclude) != 0 || summary.transient {
		t.Fatalf("buffered provider data was classified as retryable: summary=%+v soft=%v", summary, softExclude)
	}
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status=%d, want 502", recorder.Code)
	}
}

func TestExecuteProviderAfterDataNeverFallsBackCPA(t *testing.T) {
	legacy := &countingLegacyForwarder{}
	mgr := &scriptedCodexManager{events: []protocol.CodexExecuteEvent{{Type: protocol.CodexEventResponseHeaders, StatusCode: 200}, {Type: protocol.CodexEventData, Data: []byte("data: partial\n\n")}}, err: pluginruntime.ErrCodexExecutorUnavailable}
	p := &Pipeline{providerTransport: providertransport.NewCodexPluginTransport(mgr), cpa: legacy}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	result := p.executeProvider(context.Background(), ctx, codexForwardRequest("", ""))
	if legacy.calls != 0 || !result.Written {
		t.Fatalf("result=%+v calls=%d", result, legacy.calls)
	}
}

func TestExecuteProviderFallsBackToCPAWhenPluginUnavailableBeforeOutput(t *testing.T) {
	legacy := &countingLegacyForwarder{}
	p := &Pipeline{providerTransport: fixedProviderTransport{result: providertransport.Result{BuildErr: providertransport.ErrCodexPluginUnavailable}}, cpa: legacy}
	result := p.executeProvider(context.Background(), nil, cpa.ForwardRequest{Endpoint: "responses"})
	if legacy.calls != 1 || result.StatusCode != 204 {
		t.Fatalf("calls=%d result=%+v", legacy.calls, result)
	}
}

func TestExecuteProviderAlphaSearchNeverFallsBackToCPA(t *testing.T) {
	legacy := &countingLegacyForwarder{}
	p := &Pipeline{providerTransport: fixedProviderTransport{result: providertransport.Result{BuildErr: providertransport.ErrCodexPluginUnavailable}}, cpa: legacy}
	result := p.executeProvider(context.Background(), nil, cpa.ForwardRequest{Endpoint: "alpha_search"})
	if legacy.calls != 0 || result.BuildErr != nil || !errors.Is(result.NetErr, providertransport.ErrCodexPluginUnavailable) {
		t.Fatalf("calls=%d result=%+v", legacy.calls, result)
	}
}

func TestExecuteProviderDoesNotFallbackAfterOutput(t *testing.T) {
	legacy := &countingLegacyForwarder{}
	p := &Pipeline{providerTransport: fixedProviderTransport{result: providertransport.Result{Written: true, StreamErr: providertransport.ErrCodexPluginUnavailable}}, cpa: legacy}
	result := p.executeProvider(context.Background(), nil, cpa.ForwardRequest{})
	if legacy.calls != 0 || !result.Written {
		t.Fatalf("calls=%d result=%+v", legacy.calls, result)
	}
}

func TestExecuteProviderCompactNeverFallsBackToCPA(t *testing.T) {
	legacy := &countingLegacyForwarder{}
	p := &Pipeline{providerTransport: fixedProviderTransport{result: providertransport.Result{BuildErr: providertransport.ErrCodexPluginUnsupported}}, cpa: legacy}
	result := p.executeProvider(context.Background(), nil, cpa.ForwardRequest{Endpoint: "compact"})
	if legacy.calls != 0 || result.BuildErr != nil || !errors.Is(result.NetErr, providertransport.ErrCodexPluginUnsupported) {
		t.Fatalf("calls=%d result=%+v", legacy.calls, result)
	}
}

func TestProviderPathCodexEndpoints(t *testing.T) {
	for endpoint, want := range map[string]string{
		"responses": "/responses", "images_generations": "/images/generations", "images_edits": "/images/edits",
		"compact": "/responses/compact", "alpha_search": "/alpha/search",
		"realtime_calls": "/realtime/calls", "realtime_sideband": "/realtime",
		"memories_trace_summarize": "/memories/trace_summarize",
		"guardian":                 "/guardian", "guardian_classifier": "/guardian-classifier",
		adaptor.EndpointCodexConnectorsDirectoryList:          "/connectors/directory/list",
		adaptor.EndpointCodexConnectorsDirectoryListWorkspace: "/connectors/directory/list_workspace",
		adaptor.EndpointCodexAppsBatch:                        "/ps/apps/batch",
		adaptor.EndpointCodexPluginsFeatured:                  "/plugins/featured",
		adaptor.EndpointCodexPluginsWorkspaceUploadURL:        "/public/plugins/workspace/upload-url",
		adaptor.EndpointCodexPluginsWorkspaceCreate:           "/public/plugins/workspace",
	} {
		if got := providerPath(endpoint); got != want {
			t.Fatalf("providerPath(%q)=%q want %q", endpoint, got, want)
		}
	}
}

func TestCodexBackendRootPathsNormalizeReverseProxyBases(t *testing.T) {
	accounts := []struct {
		name    string
		account cpa.AccountAuthInput
	}{
		{name: "chatgpt codex", account: cpa.AccountAuthInput{Platform: "codex", Type: "oauth", Credentials: map[string]string{"base_url": "https://chatgpt.com/backend-api/codex"}}},
		{name: "backend root", account: cpa.AccountAuthInput{Platform: "codex", Type: "oauth", Credentials: map[string]string{"base_url": "https://gateway.example/proxy/backend-api"}}},
		{name: "api codex", account: cpa.AccountAuthInput{Platform: "codex", Type: "oauth", Credentials: map[string]string{"base_url": "https://gateway.example/api/codex"}}},
	}
	for _, test := range accounts {
		t.Run(test.name, func(t *testing.T) {
			for _, endpoint := range []string{
				adaptor.EndpointCodexConnectorsDirectoryList,
				adaptor.EndpointCodexAppsBatch,
				adaptor.EndpointCodexPluginsFeatured,
				adaptor.EndpointCodexPluginsWorkspaceUploadURL,
			} {
				path := providerPath(endpoint)
				if got := codexProviderPathForAccount(test.account, endpoint, path); got != path {
					t.Fatalf("endpoint %s path=%q, want %q", endpoint, got, path)
				}
				base := codexProviderBaseURLForAccount(test.account, endpoint)
				if strings.Contains(strings.ToLower(base), "/codex") || strings.HasSuffix(strings.ToLower(base), "/api/codex") {
					t.Fatalf("endpoint %s base=%q retained backend codex suffix", endpoint, base)
				}
			}
		})
	}
}

func TestProviderPathRemoteControlDynamicEndpointsRequireExpandedPath(t *testing.T) {
	account := cpa.AccountAuthInput{
		Platform: "codex",
		Type:     "oauth",
		Credentials: map[string]string{
			"base_url": "https://chatgpt.com/backend-api/codex",
		},
	}
	for _, endpoint := range []string{
		adaptor.EndpointCodexRemoteControlClientsList,
		adaptor.EndpointCodexRemoteControlClientRevoke,
	} {
		if got := providerPath(endpoint); got != "" {
			t.Fatalf("providerPath(%q)=%q; dynamic endpoint must not use a guessed path", endpoint, got)
		}
		if got := codexProviderPathForAccount(account, endpoint, ""); got != "" {
			t.Fatalf("codexProviderPathForAccount(%q, empty)=%q; want empty", endpoint, got)
		}
	}
	if got := codexProviderPathForAccount(account, adaptor.EndpointCodexRemoteControlClientsList,
		"/remote/control/environments/env-1/clients"); got != "/wham/remote/control/environments/env-1/clients" {
		t.Fatalf("expanded Remote Control path=%q", got)
	}
	for _, endpoint := range []string{
		adaptor.EndpointCodexPluginDetail,
		adaptor.EndpointCodexPluginSkillDetail,
		adaptor.EndpointCodexPluginLegacyEnable,
		adaptor.EndpointCodexPluginsWorkspaceUpdate,
		adaptor.EndpointCodexPluginsWorkspaceDelete,
	} {
		if got := codexProviderPathForAccount(account, endpoint, ""); got != "" {
			t.Fatalf("codexProviderPathForAccount(%q, empty)=%q; want empty for dynamic plugin path", endpoint, got)
		}
	}
}

func TestProviderPathEnvironmentDiscoveryEndpoints(t *testing.T) {
	if got := providerPath(adaptor.EndpointCodexEnvironments); got != "/environments" {
		t.Fatalf("providerPath(environments)=%q, want /environments", got)
	}
	if got := providerPath(adaptor.EndpointCodexEnvironmentsByRepo); got != "" {
		t.Fatalf("providerPath(environments_by_repo)=%q, want empty fail-closed default", got)
	}
	accounts := []struct {
		name    string
		account cpa.AccountAuthInput
		want    string
	}{
		{
			name: "oauth wham",
			account: cpa.AccountAuthInput{Platform: "codex", Type: "oauth", Credentials: map[string]string{
				"base_url": "https://chatgpt.com/backend-api/codex",
			}},
			want: "/wham/environments/by-repo/github/open%2Fai/codex/%252Fmain",
		},
		{
			name: "api codex",
			account: cpa.AccountAuthInput{Platform: "codex", Type: "api_key", Credentials: map[string]string{
				"base_url": "https://api.openai.com/v1",
			}},
			want: "/api/codex/environments/by-repo/github/open%2Fai/codex/%252Fmain",
		},
		{
			name: "backend v1 wham alias",
			account: cpa.AccountAuthInput{Platform: "codex", Type: "oauth", Credentials: map[string]string{
				"base_url": "https://gateway.example/backend-api",
			}},
			want: "/wham/environments/by-repo/github/open%2Fai/codex/%252Fmain",
		},
	}
	path := "/backend-api/v1/wham/environments/by-repo/github/open%2Fai/codex/%252Fmain"
	for _, test := range accounts {
		t.Run(test.name, func(t *testing.T) {
			got := codexProviderPathForAccount(test.account, adaptor.EndpointCodexEnvironmentsByRepo, path)
			if got != test.want {
				t.Fatalf("codexProviderPathForAccount=%q, want %q", got, test.want)
			}
		})
	}
}

func TestCodexSharedForwardOptionsDoNotChangeAccountSelection(t *testing.T) {
	for _, mode := range []string{"auto", "native", "native_only", "cpa_translate", "cpa_only"} {
		t.Run(mode, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
			c.Request.Header.Set("Originator", "codex_cli_rs")
			p := &Pipeline{codexTransportPolicy: staticCodexPolicy(mode)}
			if opts := p.codexImagesForwardOptions(c); opts.preferNativeCodex || opts.nativeCodexAccountsOnly {
				t.Fatalf("Codex shared images unexpectedly changed account selection: %+v", opts)
			}
			if opts := p.codexResponsesForwardOptions(c); opts.preferNativeCodex || opts.nativeCodexAccountsOnly {
				t.Fatalf("Codex shared Responses unexpectedly changed account selection: %+v", opts)
			}
		})
	}

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	p := &Pipeline{codexTransportPolicy: staticCodexPolicy("native_only")}
	if opts := p.codexImagesForwardOptions(c); opts.preferNativeCodex || opts.nativeCodexAccountsOnly {
		t.Fatalf("ordinary OpenAI request unexpectedly selected native images: %+v", opts)
	}
}

func TestProviderCPAEligibleEndpoints(t *testing.T) {
	for _, endpoint := range []string{
		"chat_completions", "responses", "messages", "messages_count_tokens",
		"generate_content", "count_tokens", "images_generations", "images_edits",
	} {
		if !providerCPAEligibleEndpoint(endpoint) {
			t.Errorf("endpoint %q should be CPA eligible", endpoint)
		}
	}
	for _, endpoint := range []string{"compact", "alpha_search", "realtime_calls", "realtime_sideband", "memories_trace_summarize", "predict", "", "unknown"} {
		if providerCPAEligibleEndpoint(endpoint) {
			t.Errorf("endpoint %q should not be CPA eligible", endpoint)
		}
	}
}

func TestProviderQueryFiltersInboundCredentialMaterial(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req, err := http.NewRequest(http.MethodGet, "/codex/v1/models?client_version=0.1&foo=bar&api_key=leak&access_token=leak2&session-token=leak3", nil)
	if err != nil {
		t.Fatal(err)
	}
	c.Request = req
	got := providerQuery(c)
	if got["client_version"][0] != "0.1" || got["foo"][0] != "bar" {
		t.Fatalf("safe query values missing: %#v", got)
	}
	for _, key := range []string{"api_key", "access_token", "session-token"} {
		if _, ok := got[key]; ok {
			t.Fatalf("credential query key %q leaked: %#v", key, got)
		}
	}
}

func TestProviderQueryPreservesConnectorPaginationToken(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req, err := http.NewRequest(http.MethodGet, "/codex/connectors/directory/list?external_logos=true&token=page-2&api_key=leak", nil)
	if err != nil {
		t.Fatal(err)
	}
	c.Request = req
	got := providerQueryForEndpoint(c, adaptor.EndpointCodexConnectorsDirectoryList)
	if got["token"][0] != "page-2" || got["external_logos"][0] != "true" {
		t.Fatalf("connector query values missing: %#v", got)
	}
	if _, ok := got["api_key"]; ok {
		t.Fatalf("credential query leaked: %#v", got)
	}
	// The generic projector keeps its historical credential filtering for
	// unrelated endpoints where `token` is authentication-shaped.
	if generic := providerQueryForEndpoint(c, adaptor.EndpointCodexPluginsFeatured); generic["token"] != nil {
		t.Fatalf("featured query unexpectedly preserved token: %#v", generic)
	}
}
