package pipeline

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/cpa"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
	providertransport "github.com/DouDOU-start/airgate-core/internal/relay/transport"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
)

func TestRealtimeCallForwardOptionsSeparateSharedOpenAIAndCodexRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	newContext := func(path string, codexHeader bool) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, path, nil)
		if codexHeader {
			c.Request.Header.Set("Originator", "codex_cli_rs")
		}
		return c
	}

	for _, test := range []struct {
		name        string
		path        string
		codexHeader bool
		wantNative  bool
	}{
		{name: "ordinary shared realtime", path: "/v1/realtime/calls"},
		{name: "ordinary shared live", path: "/v1/live"},
		{name: "Codex client on shared realtime", path: "/v1/realtime/calls", codexHeader: true, wantNative: true},
		{name: "explicit Codex alias", path: "/codex/v1/realtime/calls", wantNative: true},
		{name: "explicit backend alias", path: "/backend-api/codex/realtime/calls", wantNative: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			opts := realtimeCallForwardOptions(newContext(test.path, test.codexHeader), []byte("v=0\r\n"), "application/sdp", "/realtime/calls")
			if opts.nativeCodexAccountsOnly != test.wantNative {
				t.Fatalf("nativeCodexAccountsOnly=%v, want %v", opts.nativeCodexAccountsOnly, test.wantNative)
			}
			if opts.allowUnpriced != test.wantNative || opts.zeroBilling != test.wantNative {
				t.Fatalf("ordinary/Codex billing policy = allowUnpriced:%v zeroBilling:%v, want both %v", opts.allowUnpriced, opts.zeroBilling, test.wantNative)
			}
			if test.wantNative {
				if opts.channelRoutingProtocol != "" {
					t.Fatalf("Codex request unexpectedly overrides channel protocol: %q", opts.channelRoutingProtocol)
				}
			} else if opts.channelRoutingProtocol != registry.ProtocolOpenAI {
				t.Fatalf("ordinary shared request protocol=%q, want %q", opts.channelRoutingProtocol, registry.ProtocolOpenAI)
			}
		})
	}
}

func TestHandleRealtimeCallCreateRejectsModelessSharedRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name        string
		path        string
		body        []byte
		contentType string
		handler     func(*Pipeline, *gin.Context)
	}{
		{name: "realtime calls raw SDP", path: "/v1/realtime/calls", body: []byte("v=0\r\n"), contentType: "application/sdp", handler: (*Pipeline).HandleRealtimeCalls},
		{name: "live raw SDP", path: "/v1/live", body: []byte("v=0\r\n"), contentType: "application/sdp", handler: (*Pipeline).HandleRealtimeLive},
		{name: "JSON session without model", path: "/v1/realtime/calls", body: []byte(`{"sdp":"v=0","session":{"voice":"cove"}}`), contentType: "application/json", handler: (*Pipeline).HandleRealtimeCalls},
		{name: "multipart session without model", path: "/v1/live", body: realtimeMultipartSessionBody(t, `{"voice":"cove"}`), contentType: "multipart/form-data; boundary=codex-realtime-call-boundary", handler: (*Pipeline).HandleRealtimeLive},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			c.Request = httptest.NewRequest(http.MethodPost, test.path, bytes.NewReader(test.body))
			c.Request.Header.Set("Content-Type", test.contentType)
			c.Set(middleware.CtxKeyKeyInfo, &auth.APIKeyInfo{
				KeyID: 1, UserID: 2, GroupID: 7, UserBalance: 100,
			})

			test.handler(&Pipeline{}, c)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body = %s", response.Code, response.Body.String())
			}
			if !bytes.Contains(response.Body.Bytes(), []byte(`"code":"missing_model"`)) {
				t.Fatalf("body = %s, want missing_model error", response.Body.String())
			}
		})
	}
}

func TestRealtimeCallModelPreservesOfficialBodyShapes(t *testing.T) {
	if got := realtimeCallModel([]byte("v=0\r\n"), "application/sdp"); got != "" {
		t.Fatalf("SDP model = %q, want empty", got)
	}
	if got := realtimeCallModel([]byte(`{"sdp":"v=0","session":{"model":"gpt-realtime-1.5"}}`), "application/json"); got != "gpt-realtime-1.5" {
		t.Fatalf("backend JSON model = %q", got)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.SetBoundary("codex-realtime-call-boundary"); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormField("session")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte(`{"model":"session-model"}`))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if got := realtimeCallModel(body.Bytes(), writer.FormDataContentType()); got != "session-model" {
		t.Fatalf("multipart session model = %q", got)
	}
}

func TestRealtimeCallProviderQueryDefaultsFollowOfficialWireShapes(t *testing.T) {
	tests := []struct {
		name        string
		body        []byte
		contentType string
		query       map[string][]string
		want        map[string][]string
	}{
		{
			name:        "raw sdp has no session defaults",
			body:        []byte("v=0\r\n"),
			contentType: "application/sdp",
			want:        nil,
		},
		{
			name:        "backend json session gets avas defaults",
			body:        []byte(`{"sdp":"v=0","session":{"type":"quicksilver","model":"gpt-realtime"}}`),
			contentType: "application/json",
			want:        map[string][]string{"intent": {"quicksilver"}, "architecture": {"avas"}},
		},
		{
			name:        "backend json frameless session still gets avas defaults",
			body:        []byte(`{"sdp":"v=0","session":{"model":"gpt-live","delegation":{"type":"client"}}}`),
			contentType: "application/json",
			want:        map[string][]string{"intent": {"quicksilver"}, "architecture": {"avas"}},
		},
		{
			name:        "explicit query values win independently",
			body:        []byte(`{"sdp":"v=0","session":{"type":"quicksilver"}}`),
			contentType: "application/json",
			query:       map[string][]string{"intent": {"custom"}},
			want:        map[string][]string{"architecture": {"avas"}},
		},
		{
			name:        "public frameless multipart omits avas defaults",
			body:        realtimeMultipartSessionBody(t, `{"model":"gpt-live","delegation":{"type":"client"}}`),
			contentType: "multipart/form-data; boundary=codex-realtime-call-boundary",
			want:        nil,
		},
		{
			name:        "multipart v1 session gets avas defaults",
			body:        realtimeMultipartSessionBody(t, `{"type":"quicksilver","model":"gpt-realtime"}`),
			contentType: "multipart/form-data; boundary=codex-realtime-call-boundary",
			want:        map[string][]string{"intent": {"quicksilver"}, "architecture": {"avas"}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := realtimeCallProviderQueryDefaults(test.body, test.contentType, test.query)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("query defaults = %#v, want %#v", got, test.want)
			}
		})
	}
}

func realtimeMultipartSessionBody(t *testing.T, session string) []byte {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.SetBoundary("codex-realtime-call-boundary"); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormField("session")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(session)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

func TestNativeCodexRouteExcludesChannelsAndNonCodexAccounts(t *testing.T) {
	p := newWebSocketPreflightPipeline(t, providertransport.Capabilities{HTTP: true},
		accountreg.Snapshot{ID: 1, Platform: "claude", State: accountreg.StateActive, Priority: 100,
			Models: map[string]struct{}{"gpt-test": {}}, GroupIDs: map[int]struct{}{7: {}}},
		accountreg.Snapshot{ID: 2, Platform: "codex", Type: "oauth", State: accountreg.StateActive, Priority: 1,
			Models: map[string]struct{}{"gpt-test": {}}, GroupIDs: map[int]struct{}{7: {}}},
	)
	target, ok := p.pickNativeCodexRoute(7, "gpt-test", nil)
	if !ok || target.kind != routeAccount || target.account == nil || target.account.ID != 2 {
		t.Fatalf("native route = %+v, ok=%v", target, ok)
	}
	// A model-less SDP request must still find the group's native account.
	if target, ok = p.pickNativeCodexRoute(7, "", nil); !ok || target.account.ID != 2 {
		t.Fatalf("model-less native route = %+v, ok=%v", target, ok)
	}
}

func TestNativeCodexRouteIncludesAccountsWithoutModelCatalog(t *testing.T) {
	p := newWebSocketPreflightPipeline(t, providertransport.Capabilities{HTTP: true},
		accountreg.Snapshot{ID: 1, Platform: "claude", State: accountreg.StateActive, Priority: 100,
			Models: map[string]struct{}{"gpt-test": {}}, GroupIDs: map[int]struct{}{7: {}}},
		accountreg.Snapshot{ID: 2, Platform: "codex", Type: "oauth", State: accountreg.StateActive, Priority: 1,
			Models: nil, GroupIDs: map[int]struct{}{7: {}}},
	)
	for _, model := range []string{"", "gpt-realtime-not-in-catalog"} {
		target, ok := p.pickNativeCodexRoute(7, model, nil)
		if !ok || target.account == nil || target.account.ID != 2 {
			t.Fatalf("model %q native route = %+v, ok=%v", model, target, ok)
		}
	}
	if !p.hasNativeCodexAccount(7) {
		t.Fatal("model-less native account failed websocket preflight")
	}
	account, model := p.pickAnyNativeCodexAccount(7)
	if account == nil || account.ID != 2 || model != "" {
		t.Fatalf("model-less sideband account = %+v, model=%q", account, model)
	}
}

func TestNativeCodexRouteSkipsInvalidAuthTypes(t *testing.T) {
	invalid := accountreg.Snapshot{ID: 1, Platform: "codex", Type: "", State: accountreg.StateActive, Priority: 100,
		Models: map[string]struct{}{"gpt-test": {}}, GroupIDs: map[int]struct{}{7: {}}}
	valid := accountreg.Snapshot{ID: 2, Platform: "codex", Type: "api_key", State: accountreg.StateActive, Priority: 1,
		Models: map[string]struct{}{"gpt-test": {}}, GroupIDs: map[int]struct{}{7: {}}}
	p := newWebSocketPreflightPipeline(t, providertransport.Capabilities{HTTP: true}, invalid, valid)
	target, ok := p.pickNativeCodexRoute(7, "gpt-test", nil)
	if !ok || target.account == nil || target.account.ID != valid.ID {
		t.Fatalf("native route = %+v, ok=%v; invalid auth type must not shadow valid account", target, ok)
	}

	for _, authType := range []string{"", "unknown", "refresh_token", "api-key"} {
		t.Run("reject_"+authType, func(t *testing.T) {
			account := invalid
			account.ID = 10
			account.Type = authType
			p := newWebSocketPreflightPipeline(t, providertransport.Capabilities{HTTP: true}, account)
			if target, ok := p.pickNativeCodexRoute(7, "gpt-test", nil); ok || target.account != nil {
				t.Fatalf("auth type %q selected native account: %+v, ok=%v", authType, target, ok)
			}
		})
	}
}

func TestNativeCodexRouteMixesExactAndWildcardAccountsInPriorityTier(t *testing.T) {
	exact := accountreg.Snapshot{ID: 1, Platform: "codex", Type: "oauth", State: accountreg.StateActive, Priority: 10, Weight: 1,
		Models: map[string]struct{}{"gpt-realtime": {}}, GroupIDs: map[int]struct{}{7: {}}}
	wildcard := accountreg.Snapshot{ID: 2, Platform: "codex", Type: "oauth", State: accountreg.StateActive, Priority: 20, Weight: 1,
		Models: nil, GroupIDs: map[int]struct{}{7: {}}}
	p := newWebSocketPreflightPipeline(t, providertransport.Capabilities{HTTP: true}, exact, wildcard)
	if target, ok := p.pickNativeCodexRoute(7, "gpt-realtime", nil); !ok || target.account == nil || target.account.ID != 2 {
		t.Fatalf("higher-priority wildcard was not selected: %+v, ok=%v", target, ok)
	}
	// Reverse priorities: exact and wildcard must both remain eligible, and the
	// exact account should win solely because its priority is higher.
	exact.Priority, wildcard.Priority = 30, 1
	p = newWebSocketPreflightPipeline(t, providertransport.Capabilities{HTTP: true}, exact, wildcard)
	if target, ok := p.pickNativeCodexRoute(7, "gpt-realtime", nil); !ok || target.account == nil || target.account.ID != 1 {
		t.Fatalf("higher-priority exact account was not selected: %+v, ok=%v", target, ok)
	}
}

func TestRealtimeCallLocationBindsCreatingAccount(t *testing.T) {
	p := newWebSocketPreflightPipeline(t, providertransport.Capabilities{HTTP: true},
		accountreg.Snapshot{ID: 2, Platform: "codex", Type: "oauth", State: accountreg.StateActive,
			Models: map[string]struct{}{"gpt-test": {}}, GroupIDs: map[int]struct{}{7: {}}},
	)
	p.rememberRealtimeCallAccount(11, 7, "/v1/realtime/calls/calls/rtc_same_account?x=1", 2)
	account, ok := p.realtimeCallAccount(11, 7, "rtc_same_account")
	if !ok || account.ID != 2 {
		t.Fatalf("bound account = %+v, ok=%v", account, ok)
	}
	if _, ok := p.realtimeCallAccount(12, 7, "rtc_same_account"); ok {
		t.Fatal("realtime call affinity crossed user boundary")
	}
}

func TestRealtimeCallIDFromLocationPreservesEscapedSegmentBoundaries(t *testing.T) {
	validUUID := "019eb97d-8e9a-7ff3-94b0-ea019babd5d7"
	for _, test := range []struct {
		name     string
		location string
		want     string
	}{
		{name: "rtc", location: "/v1/realtime/calls/calls/rtc_test?foo=bar", want: "rtc_test"},
		{name: "uuid", location: "https://api.openai.com/v1/realtime/calls/" + validUUID, want: validUUID},
		{name: "private live opaque id", location: "https://gateway.example/v1/live/call_private_123", want: "call_private_123"},
		{name: "private realtime opaque id", location: "https://gateway.example/backend-api/codex/realtime/calls/call_private_456", want: "call_private_456"},
		{name: "private duplicated calls opaque id", location: "/proxy/realtime/calls/calls/session-opaque", want: "session-opaque"},
		{name: "unknown path does not bind", location: "/v1/other/call_private_789", want: ""},
		{name: "malformed trailing segment does not bind earlier id", location: "/v1/live/call_private_123/extra", want: ""},
		{name: "encoded leading slash remains opaque", location: "/v1/realtime/calls/%2Frtc_injected", want: "/rtc_injected"},
		{name: "encoded dot segments remain opaque", location: "/v1/realtime/calls/..%2F..%2Fadmin", want: "../../admin"},
		{name: "encoded slash in rtc id", location: "/v1/realtime/calls/rtc_nested%2Fchild", want: "rtc_nested/child"},
		{name: "double encoded slash remains literal", location: "/v1/realtime/calls/rtc_literal%252Fchild", want: "rtc_literal%2Fchild"},
		{name: "dot", location: "/v1/realtime/calls/.", want: ""},
		{name: "dot dot", location: "/v1/realtime/calls/..", want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := realtimeCallIDFromLocation(test.location); got != test.want {
				t.Fatalf("call id = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCodexWebSocketBaseURLUsesOfficialSidebandOrigin(t *testing.T) {
	for _, test := range []struct {
		name        string
		credentials map[string]string
		endpoint    string
		want        string
	}{
		{
			name:        "OAuth default uses public API",
			credentials: map[string]string{"access_token": "token"},
			endpoint:    "realtime_sideband",
			want:        "https://api.openai.com/v1",
		},
		{
			name:        "Responses OAuth keeps ChatGPT backend",
			credentials: map[string]string{"access_token": "token"},
			endpoint:    "responses",
			want:        "https://chatgpt.com/backend-api/codex",
		},
		{
			name: "ChatGPT backend base does not follow sideband",
			credentials: map[string]string{
				"access_token": "token", "base_url": "https://chatgpt.com/backend-api/codex/",
			},
			endpoint: "realtime_sideband", want: "https://api.openai.com/v1",
		},
		{
			name: "chat.openai backend base does not follow sideband",
			credentials: map[string]string{
				"access_token": "token", "base_url": "https://chat.openai.com/backend-api/codex/responses",
			},
			endpoint: "realtime_sideband", want: "https://api.openai.com/v1",
		},
		{
			name: "explicit sideband override wins",
			credentials: map[string]string{
				"access_token": "token", "base_url": "https://chatgpt.com/backend-api/codex",
				"realtime_sideband_base_url": "https://sideband.gateway.example/openai/v1/",
			},
			endpoint: "realtime_sideband", want: "https://sideband.gateway.example/openai/v1",
		},
		{
			name: "custom base remains supported",
			credentials: map[string]string{
				"access_token": "token", "base_url": "https://gateway.example/openai/v1/",
			},
			endpoint: "realtime_sideband", want: "https://gateway.example/openai/v1",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			account := &accountreg.Snapshot{Type: "oauth", Credentials: test.credentials}
			if got := codexWebSocketBaseURL(account, test.endpoint); got != test.want {
				t.Fatalf("sideband base = %q, want %q", got, test.want)
			}
		})
	}
}

func TestIsChatGPTCodexBackendBaseURL(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want bool
	}{
		{raw: "https://chatgpt.com/backend-api/codex", want: true},
		{raw: "https://chatgpt.com/backend-api/codex/responses", want: true},
		{raw: "https://chat.openai.com/backend-api/codex/", want: true},
		{raw: "https://CHATGPT.COM/backend-api/codex?x=1", want: true},
		{raw: "https://gateway.example/backend-api/codex", want: false},
		{raw: "https://chatgpt.com/api/codex", want: false},
		{raw: "not a url", want: false},
	} {
		t.Run(test.raw, func(t *testing.T) {
			if got := isChatGPTCodexBackendBaseURL(test.raw); got != test.want {
				t.Fatalf("isChatGPTCodexBackendBaseURL(%q) = %v, want %v", test.raw, got, test.want)
			}
		})
	}
}

func TestCodexBackendPathRecognizesPrivateReverseProxy(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want bool
	}{
		{raw: "https://gateway.example/backend-api/codex", want: true},
		{raw: "https://gateway.example/backend-api/codex/realtime/calls", want: true},
		{raw: "https://gateway.example/tenant-a/backend-api/codex", want: true},
		{raw: "https://gateway.example/tenant-a/backend-api/codex/realtime/calls", want: true},
		{raw: "https://gateway.example/not-backend-api/codex", want: false},
		{raw: "https://gateway.example/tenant-not-backend-api/codex/realtime/calls", want: false},
		{raw: "https://gateway.example/tenant-a/backend-api/codex-v2", want: false},
		{raw: "https://gateway.example/openai/v1", want: false},
		{raw: "not a url", want: false},
	} {
		if got := isCodexBackendBaseURLPath(test.raw); got != test.want {
			t.Fatalf("isCodexBackendBaseURLPath(%q) = %v, want %v", test.raw, got, test.want)
		}
	}
}

func TestProviderRequestPreservesRawRealtimeWire(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/live?intent=quicksilver&architecture=avas", nil)
	raw := []byte("v=0\r\n")
	request := providerRequestFromCPA(c.Request.Context(), c, cpa.ForwardRequest{
		Account: cpa.AccountAuthInput{Platform: "codex", Type: "api_key", Credentials: map[string]string{"base_url": "https://api.openai.com/v1"}}, Endpoint: "realtime_calls",
		Method: http.MethodPost, Path: "/live", RawBody: raw,
		RawContentType: "application/sdp", Headers: make(http.Header),
	})
	if request.Path != "/live" || !bytes.Equal(request.Payload, raw) || request.Headers.Get("Content-Type") != "application/sdp" {
		t.Fatalf("provider request lost raw wire: %+v", request)
	}
	if request.Query["intent"][0] != "quicksilver" || request.Query["architecture"][0] != "avas" {
		t.Fatalf("provider query = %#v", request.Query)
	}
}

func TestCodexProviderPathForAccountMapsFramelessOAuthBackend(t *testing.T) {
	tests := []struct {
		name      string
		account   cpa.AccountAuthInput
		endpoint  string
		requested string
		want      string
	}{
		{
			name:      "default OAuth ChatGPT backend",
			account:   cpa.AccountAuthInput{Type: "oauth"},
			endpoint:  "realtime_calls",
			requested: "/live",
			want:      "/realtime/calls",
		},
		{
			name:      "explicit ChatGPT backend URL",
			account:   cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{"base_url": "https://chat.openai.com/backend-api/codex/responses"}},
			endpoint:  "realtime_calls",
			requested: "/live",
			want:      "/realtime/calls",
		},
		{
			name:      "public API key keeps Frameless path",
			account:   cpa.AccountAuthInput{Type: "api_key", Credentials: map[string]string{"base_url": "https://api.openai.com/v1"}},
			endpoint:  "realtime_calls",
			requested: "/live",
			want:      "/live",
		},
		{
			name:      "private gateway keeps requested path",
			account:   cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{"base_url": "https://gateway.example/openai/v1"}},
			endpoint:  "realtime_calls",
			requested: "/live",
			want:      "/live",
		},
		{
			name:      "private ChatGPT-shaped gateway uses backend path",
			account:   cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{"base_url": "https://gateway.example/backend-api/codex"}},
			endpoint:  "realtime_calls",
			requested: "/live",
			want:      "/realtime/calls",
		},
		{
			name:      "ordinary Responses path is unchanged",
			account:   cpa.AccountAuthInput{Type: "oauth"},
			endpoint:  "responses",
			requested: "/responses",
			want:      "/responses",
		},
		{
			name: "Files on an already-rooted ChatGPT backend",
			account: cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
				"base_url": "https://gateway.example/backend-api",
			}},
			endpoint:  adaptor.EndpointFilesCreate,
			requested: "/files",
			want:      "/files",
		},
		{
			name: "history path prefix is idempotent",
			account: cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
				"base_url": "https://gateway.example/backend-api",
			}},
			endpoint:  adaptor.EndpointHistoryListWindows,
			requested: "/codex/alpha/history/v2/list_windows",
			want:      "/codex/alpha/history/v2/list_windows",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := codexProviderPathForAccount(test.account, test.endpoint, test.requested); got != test.want {
				t.Fatalf("provider path = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCodexProviderBaseURLForAccountUsesOfficialFilesRoot(t *testing.T) {
	tests := []struct {
		name     string
		account  cpa.AccountAuthInput
		endpoint string
		want     string
	}{
		{
			name:     "default OAuth Files create",
			account:  cpa.AccountAuthInput{Type: "oauth"},
			endpoint: adaptor.EndpointFilesCreate,
			want:     "https://chatgpt.com/backend-api",
		},
		{
			name:     "default OAuth Files finalize",
			account:  cpa.AccountAuthInput{Type: "oauth"},
			endpoint: adaptor.EndpointFilesFinalize,
			want:     "https://chatgpt.com/backend-api",
		},
		{
			name: "explicit ChatGPT backend",
			account: cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
				"base_url": "https://chat.openai.com/backend-api/codex/",
			}},
			endpoint: adaptor.EndpointFilesCreate,
			want:     "https://chat.openai.com/backend-api",
		},
		{
			name: "private reverse proxy",
			account: cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
				"base_url": "https://gateway.example/backend-api/codex",
			}},
			endpoint: adaptor.EndpointFilesFinalize,
			want:     "https://gateway.example/backend-api",
		},
		{
			name: "backend root already selected",
			account: cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
				"base_url": "https://gateway.example/backend-api",
			}},
			endpoint: adaptor.EndpointFilesCreate,
			want:     "https://gateway.example/backend-api",
		},
		{
			name: "public API key",
			account: cpa.AccountAuthInput{Type: "api_key", Credentials: map[string]string{
				"base_url": "https://api.openai.com/v1",
			}},
			endpoint: adaptor.EndpointFilesCreate,
			want:     "https://api.openai.com/v1",
		},
		{
			name: "ordinary custom gateway",
			account: cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
				"base_url": "https://gateway.example/openai/v1",
			}},
			endpoint: adaptor.EndpointFilesCreate,
			want:     "https://gateway.example/openai/v1",
		},
		{
			name: "non Files endpoint unchanged",
			account: cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
				"base_url": "https://chatgpt.com/backend-api/codex",
			}},
			endpoint: adaptor.EndpointResponses,
			want:     "https://chatgpt.com/backend-api/codex",
		},
		{
			name: "query preserved",
			account: cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
				"base_url": "https://chatgpt.com/backend-api/codex?tenant=one",
			}},
			endpoint: adaptor.EndpointFilesCreate,
			want:     "https://chatgpt.com/backend-api?tenant=one",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := codexProviderBaseURLForAccount(test.account, test.endpoint); got != test.want {
				t.Fatalf("provider base URL = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCodexTurnCostsProviderURLMatchesOfficialBackendClient(t *testing.T) {
	tests := []struct {
		name      string
		account   cpa.AccountAuthInput
		requested string
		wantBase  string
		wantPath  string
	}{
		{
			name:      "default OAuth ChatGPT origin",
			account:   cpa.AccountAuthInput{Type: "oauth"},
			requested: "/analytics/codex/turn-costs",
			wantBase:  "https://api.chatgpt.com/v1",
			wantPath:  "/analytics/codex/turn-costs",
		},
		{
			name: "chat.openai.com rewrites host and clears URL metadata",
			account: cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
				"base_url": "https://chat.openai.com/backend-api/codex?tenant=one",
			}},
			requested: "/codex/v1/analytics/codex/turn-costs",
			wantBase:  "https://api.chatgpt.com/v1",
			wantPath:  "/analytics/codex/turn-costs",
		},
		{
			name: "staging ChatGPT origin",
			account: cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
				"base_url": "https://chatgpt-staging.com/backend-api/",
			}},
			requested: "/v1/analytics/codex/turn-costs",
			wantBase:  "https://api.chatgpt-staging.com/v1",
			wantPath:  "/analytics/codex/turn-costs",
		},
		{
			name: "public API origin normalizes to v1",
			account: cpa.AccountAuthInput{Type: "api_key", Credentials: map[string]string{
				"base_url": "https://api.openai.com?tenant=one",
			}},
			requested: "/analytics/codex/turn-costs",
			wantBase:  "https://api.openai.com/v1",
			wantPath:  "/analytics/codex/turn-costs",
		},
		{
			name: "custom reverse proxy preserves prefix and query",
			account: cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
				"base_url": "https://proxy.example/tenant-a/backend-api/codex?tenant=one",
			}},
			requested: "/backend-api/codex/v1/analytics/codex/turn-costs",
			wantBase:  "https://proxy.example/tenant-a/backend-api/codex?tenant=one",
			wantPath:  "/analytics/codex/turn-costs",
		},
	}

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "http://gateway.test/v1/analytics/codex/turn-costs", nil)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := providerRequestFromCPA(c.Request.Context(), c, cpa.ForwardRequest{
				Account: test.account, Endpoint: adaptor.EndpointCodexTurnCosts,
				Method: http.MethodPost, Path: test.requested,
			})
			if provider.BaseURL != test.wantBase {
				t.Fatalf("provider base URL = %q, want %q", provider.BaseURL, test.wantBase)
			}
			if provider.Path != test.wantPath {
				t.Fatalf("provider path = %q, want %q", provider.Path, test.wantPath)
			}
			base, err := url.Parse(provider.BaseURL)
			if err != nil {
				t.Fatalf("provider base URL parse failed: %v", err)
			}
			joinedPath := strings.TrimRight(base.EscapedPath(), "/") + "/" + strings.TrimLeft(provider.Path, "/")
			if test.name == "custom reverse proxy preserves prefix and query" {
				if joinedPath != "/tenant-a/backend-api/codex/analytics/codex/turn-costs" {
					t.Fatalf("custom joined path = %q", joinedPath)
				}
				if base.Query().Get("tenant") != "one" {
					t.Fatalf("custom provider query was lost: %q", provider.BaseURL)
				}
				return
			}
			if joinedPath != "/v1/analytics/codex/turn-costs" {
				t.Fatalf("joined provider URL path = %q, want /v1/analytics/codex/turn-costs", joinedPath)
			}
			if base.RawQuery != "" || base.Fragment != "" {
				t.Fatalf("official turn-cost base retained query/fragment: %q", provider.BaseURL)
			}
		})
	}
}

func TestProviderRequestFilesURLShapeUsesBackendRoot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "http://gateway.test/v1/files", nil)
	for _, endpoint := range []string{adaptor.EndpointFilesCreate, adaptor.EndpointFilesFinalize} {
		req := cpa.ForwardRequest{
			Account:  cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{"access_token": "token"}},
			Endpoint: endpoint,
			Method:   http.MethodPost,
			Path:     "/files",
		}
		if endpoint == adaptor.EndpointFilesFinalize {
			req.Path = "/files/file-123/uploaded"
		}
		provider := providerRequestFromCPA(c.Request.Context(), c, req)
		if provider.BaseURL != "https://chatgpt.com/backend-api" {
			t.Fatalf("endpoint %s base URL = %q, want https://chatgpt.com/backend-api", endpoint, provider.BaseURL)
		}
		if endpoint == adaptor.EndpointFilesCreate && provider.Path != "/files" {
			t.Fatalf("Files create path = %q, want /files", provider.Path)
		}
		if endpoint == adaptor.EndpointFilesFinalize && provider.Path != "/files/file-123/uploaded" {
			t.Fatalf("Files finalize path = %q, want /files/file-123/uploaded", provider.Path)
		}
	}
}

func TestCodexProviderRouteMatrixPreservesOfficialURLShapes(t *testing.T) {
	tests := []struct {
		name      string
		account   cpa.AccountAuthInput
		endpoint  string
		requested string
		wantBase  string
		wantPath  string
		wantURL   string
	}{
		// ChatGPT OAuth's normal inference base.
		{
			name: "backend-api/codex Responses",
			account: cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
				"base_url": "https://chatgpt.com/backend-api/codex",
			}},
			endpoint: adaptor.EndpointResponses, requested: "/responses",
			wantBase: "https://chatgpt.com/backend-api/codex", wantPath: "/responses",
			wantURL: "/backend-api/codex/responses",
		},
		{
			name: "backend-api/codex Files",
			account: cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
				"base_url": "https://chatgpt.com/backend-api/codex",
			}},
			endpoint: adaptor.EndpointFilesCreate, requested: "/files",
			wantBase: "https://chatgpt.com/backend-api", wantPath: "/files",
			wantURL: "/backend-api/files",
		},
		{
			name: "backend-api/codex Analytics",
			account: cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
				"base_url": "https://chatgpt.com/backend-api/codex",
			}},
			endpoint: adaptor.EndpointAnalyticsEvents, requested: "/analytics-events/events",
			wantBase: "https://chatgpt.com/backend-api/codex", wantPath: "/analytics-events/events",
			wantURL: "/backend-api/codex/analytics-events/events",
		},
		{
			name: "backend-api/codex History",
			account: cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
				"base_url": "https://chatgpt.com/backend-api/codex",
			}},
			endpoint: adaptor.EndpointHistoryListWindows, requested: "/alpha/history/v2/list_windows",
			wantBase: "https://chatgpt.com/backend-api/codex", wantPath: "/alpha/history/v2/list_windows",
			wantURL: "/backend-api/codex/alpha/history/v2/list_windows",
		},
		{
			name: "backend-api/codex Notes",
			account: cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
				"base_url": "https://chatgpt.com/backend-api/codex",
			}},
			endpoint: adaptor.EndpointNotesWriteFile, requested: "/alpha/notes/v2/write_file",
			wantBase: "https://chatgpt.com/backend-api/codex", wantPath: "/alpha/notes/v2/write_file",
			wantURL: "/backend-api/codex/alpha/notes/v2/write_file",
		},

		// Public/compatibility Codex base (`/codex/v1`) has no ChatGPT path
		// rewriting; every endpoint remains under the configured prefix.
		{
			name: "codex/v1 Responses",
			account: cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
				"base_url": "https://gateway.example/codex/v1",
			}},
			endpoint: adaptor.EndpointResponses, requested: "/responses",
			wantBase: "https://gateway.example/codex/v1", wantPath: "/responses",
			wantURL: "/codex/v1/responses",
		},
		{
			name: "codex/v1 Files",
			account: cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
				"base_url": "https://gateway.example/codex/v1",
			}},
			endpoint: adaptor.EndpointFilesCreate, requested: "/files",
			wantBase: "https://gateway.example/codex/v1", wantPath: "/files",
			wantURL: "/codex/v1/files",
		},
		{
			name: "codex/v1 Analytics",
			account: cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
				"base_url": "https://gateway.example/codex/v1",
			}},
			endpoint: adaptor.EndpointAnalyticsEvents, requested: "/analytics-events/events",
			wantBase: "https://gateway.example/codex/v1", wantPath: "/analytics-events/events",
			wantURL: "/codex/v1/analytics-events/events",
		},
		{
			name: "codex/v1 History",
			account: cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
				"base_url": "https://gateway.example/codex/v1",
			}},
			endpoint: adaptor.EndpointHistoryListWindows, requested: "/alpha/history/v2/list_windows",
			wantBase: "https://gateway.example/codex/v1", wantPath: "/alpha/history/v2/list_windows",
			wantURL: "/codex/v1/alpha/history/v2/list_windows",
		},
		{
			name: "codex/v1 Notes",
			account: cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
				"base_url": "https://gateway.example/codex/v1",
			}},
			endpoint: adaptor.EndpointNotesWriteFile, requested: "/alpha/notes/v2/write_file",
			wantBase: "https://gateway.example/codex/v1", wantPath: "/alpha/notes/v2/write_file",
			wantURL: "/codex/v1/alpha/notes/v2/write_file",
		},

		// A backend root is used by the official Files API and requires an
		// explicit `/codex` segment for the other control-plane families.
		{
			name: "backend-api Responses",
			account: cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
				"base_url": "https://gateway.example/backend-api",
			}},
			endpoint: adaptor.EndpointResponses, requested: "/responses",
			wantBase: "https://gateway.example/backend-api", wantPath: "/responses",
			wantURL: "/backend-api/responses",
		},
		{
			name: "backend-api Files",
			account: cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
				"base_url": "https://gateway.example/backend-api",
			}},
			endpoint: adaptor.EndpointFilesCreate, requested: "/files",
			wantBase: "https://gateway.example/backend-api", wantPath: "/files",
			wantURL: "/backend-api/files",
		},
		{
			name: "backend-api Analytics",
			account: cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
				"base_url": "https://gateway.example/backend-api",
			}},
			endpoint: adaptor.EndpointAnalyticsEvents, requested: "/analytics-events/events",
			wantBase: "https://gateway.example/backend-api", wantPath: "/codex/analytics-events/events",
			wantURL: "/backend-api/codex/analytics-events/events",
		},
		{
			name: "backend-api History",
			account: cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
				"base_url": "https://gateway.example/backend-api",
			}},
			endpoint: adaptor.EndpointHistoryListWindows, requested: "/alpha/history/v2/list_windows",
			wantBase: "https://gateway.example/backend-api", wantPath: "/codex/alpha/history/v2/list_windows",
			wantURL: "/backend-api/codex/alpha/history/v2/list_windows",
		},
		{
			name: "backend-api Notes",
			account: cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
				"base_url": "https://gateway.example/backend-api",
			}},
			endpoint: adaptor.EndpointNotesWriteFile, requested: "/alpha/notes/v2/write_file",
			wantBase: "https://gateway.example/backend-api", wantPath: "/codex/alpha/notes/v2/write_file",
			wantURL: "/backend-api/codex/alpha/notes/v2/write_file",
		},

		// API-key accounts use the public `/v1` contract without any ChatGPT
		// backend prefix transformations.
		{
			name: "API key v1 Responses",
			account: cpa.AccountAuthInput{Type: "api_key", Credentials: map[string]string{
				"base_url": "https://api.openai.com/v1",
			}},
			endpoint: adaptor.EndpointResponses, requested: "/responses",
			wantBase: "https://api.openai.com/v1", wantPath: "/responses",
			wantURL: "/v1/responses",
		},
		{
			name: "API key v1 Files",
			account: cpa.AccountAuthInput{Type: "api_key", Credentials: map[string]string{
				"base_url": "https://api.openai.com/v1",
			}},
			endpoint: adaptor.EndpointFilesCreate, requested: "/files",
			wantBase: "https://api.openai.com/v1", wantPath: "/files",
			wantURL: "/v1/files",
		},
		{
			name: "API key v1 Analytics",
			account: cpa.AccountAuthInput{Type: "api_key", Credentials: map[string]string{
				"base_url": "https://api.openai.com/v1",
			}},
			endpoint: adaptor.EndpointAnalyticsEvents, requested: "/analytics-events/events",
			wantBase: "https://api.openai.com/v1", wantPath: "/analytics-events/events",
			wantURL: "/v1/analytics-events/events",
		},
		{
			name: "API key v1 History",
			account: cpa.AccountAuthInput{Type: "api_key", Credentials: map[string]string{
				"base_url": "https://api.openai.com/v1",
			}},
			endpoint: adaptor.EndpointHistoryListWindows, requested: "/alpha/history/v2/list_windows",
			wantBase: "https://api.openai.com/v1", wantPath: "/alpha/history/v2/list_windows",
			wantURL: "/v1/alpha/history/v2/list_windows",
		},
		{
			name: "API key v1 Notes",
			account: cpa.AccountAuthInput{Type: "api_key", Credentials: map[string]string{
				"base_url": "https://api.openai.com/v1",
			}},
			endpoint: adaptor.EndpointNotesWriteFile, requested: "/alpha/notes/v2/write_file",
			wantBase: "https://api.openai.com/v1", wantPath: "/alpha/notes/v2/write_file",
			wantURL: "/v1/alpha/notes/v2/write_file",
		},

		// A private reverse proxy retaining the official backend path must get
		// the same split as chatgpt.com; host identity must not control routing.
		{
			name: "private backend-api/codex Responses",
			account: cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
				"base_url": "https://proxy.example/tenant-a/backend-api/codex",
			}},
			endpoint: adaptor.EndpointResponses, requested: "/responses",
			wantBase: "https://proxy.example/tenant-a/backend-api/codex", wantPath: "/responses",
			wantURL: "/tenant-a/backend-api/codex/responses",
		},
		{
			name: "private backend-api/codex Files",
			account: cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
				"base_url": "https://proxy.example/tenant-a/backend-api/codex",
			}},
			endpoint: adaptor.EndpointFilesCreate, requested: "/files",
			wantBase: "https://proxy.example/tenant-a/backend-api", wantPath: "/files",
			wantURL: "/tenant-a/backend-api/files",
		},
		{
			name: "private backend-api/codex Analytics",
			account: cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
				"base_url": "https://proxy.example/tenant-a/backend-api/codex",
			}},
			endpoint: adaptor.EndpointAnalyticsEvents, requested: "/analytics-events/events",
			wantBase: "https://proxy.example/tenant-a/backend-api/codex", wantPath: "/analytics-events/events",
			wantURL: "/tenant-a/backend-api/codex/analytics-events/events",
		},
		{
			name: "private backend-api/codex History",
			account: cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
				"base_url": "https://proxy.example/tenant-a/backend-api/codex",
			}},
			endpoint: adaptor.EndpointHistoryListWindows, requested: "/alpha/history/v2/list_windows",
			wantBase: "https://proxy.example/tenant-a/backend-api/codex", wantPath: "/alpha/history/v2/list_windows",
			wantURL: "/tenant-a/backend-api/codex/alpha/history/v2/list_windows",
		},
		{
			name: "private backend-api/codex Notes",
			account: cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{
				"base_url": "https://proxy.example/tenant-a/backend-api/codex",
			}},
			endpoint: adaptor.EndpointNotesWriteFile, requested: "/alpha/notes/v2/write_file",
			wantBase: "https://proxy.example/tenant-a/backend-api/codex", wantPath: "/alpha/notes/v2/write_file",
			wantURL: "/tenant-a/backend-api/codex/alpha/notes/v2/write_file",
		},
	}

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "http://gateway.test/v1/responses", nil)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := cpa.ForwardRequest{Account: test.account, Endpoint: test.endpoint, Method: http.MethodPost, Path: test.requested}
			provider := providerRequestFromCPA(c.Request.Context(), c, req)
			if provider.BaseURL != test.wantBase {
				t.Fatalf("provider base URL = %q, want %q", provider.BaseURL, test.wantBase)
			}
			if provider.Path != test.wantPath {
				t.Fatalf("provider path = %q, want %q", provider.Path, test.wantPath)
			}
			parsed, err := url.Parse(provider.BaseURL)
			if err != nil {
				t.Fatalf("provider base URL parse failed: %v", err)
			}
			joined := strings.TrimRight(parsed.EscapedPath(), "/") + "/" + strings.TrimLeft(provider.Path, "/")
			if joined != test.wantURL {
				t.Fatalf("joined provider URL path = %q, want %q", joined, test.wantURL)
			}
		})
	}
}

func TestCodexProviderPathClassificationIgnoresBaseURLQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "http://gateway.test/v1/responses", nil)
	tests := []struct {
		name      string
		baseURL   string
		endpoint  string
		requested string
		wantBase  string
		wantPath  string
	}{
		{
			name:     "codex base query",
			baseURL:  "https://proxy.example/backend-api/codex?tenant=one",
			endpoint: adaptor.EndpointHistoryListWindows, requested: "/alpha/history/v2/list_windows",
			wantBase: "https://proxy.example/backend-api/codex?tenant=one", wantPath: "/alpha/history/v2/list_windows",
		},
		{
			name:     "backend root query",
			baseURL:  "https://proxy.example/backend-api?tenant=one",
			endpoint: adaptor.EndpointHistoryListWindows, requested: "/alpha/history/v2/list_windows",
			wantBase: "https://proxy.example/backend-api?tenant=one", wantPath: "/codex/alpha/history/v2/list_windows",
		},
		{
			name:     "backend root query analytics",
			baseURL:  "https://proxy.example/backend-api?tenant=one",
			endpoint: adaptor.EndpointAnalyticsEvents, requested: "/analytics-events/events",
			wantBase: "https://proxy.example/backend-api?tenant=one", wantPath: "/codex/analytics-events/events",
		},
		{
			name:     "files query strips codex only from path",
			baseURL:  "https://proxy.example/backend-api/codex?tenant=one",
			endpoint: adaptor.EndpointFilesCreate, requested: "/files",
			wantBase: "https://proxy.example/backend-api?tenant=one", wantPath: "/files",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := providerRequestFromCPA(c.Request.Context(), c, cpa.ForwardRequest{
				Account:  cpa.AccountAuthInput{Type: "oauth", Credentials: map[string]string{"base_url": test.baseURL}},
				Endpoint: test.endpoint, Method: http.MethodPost, Path: test.requested,
			})
			if provider.BaseURL != test.wantBase || provider.Path != test.wantPath {
				t.Fatalf("provider request = base:%q path:%q, want base:%q path:%q", provider.BaseURL, provider.Path, test.wantBase, test.wantPath)
			}
		})
	}
}
