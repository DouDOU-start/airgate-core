package pipeline

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/pluginruntime/protocol"
	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	providertransport "github.com/DouDOU-start/airgate-core/internal/relay/transport"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
)

type sidebandTestTransport struct {
	requestCh chan providertransport.Request
	frameCh   chan protocol.CodexWebSocketFrame
	mode      string
}

func (t *sidebandTestTransport) Execute(context.Context, providertransport.Request) providertransport.Result {
	return providertransport.Result{}
}

func (t *sidebandTestTransport) Capabilities() providertransport.Capabilities {
	return providertransport.Capabilities{HTTP: true, Streaming: true, WebSocket: true}
}

func (t *sidebandTestTransport) CodexTransportMode() string { return t.mode }

func (t *sidebandTestTransport) ExecuteWebSocket(
	ctx context.Context,
	req providertransport.Request,
	frames <-chan protocol.CodexWebSocketFrame,
	emit func(protocol.CodexWebSocketFrame) error,
) (map[string]string, error) {
	select {
	case t.requestCh <- req:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := emit(protocol.CodexWebSocketFrame{
		Version: protocol.CodexExecutorVersion, Type: protocol.CodexWebSocketFrameHeaders,
		StatusCode: http.StatusSwitchingProtocols,
	}); err != nil {
		return nil, err
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case frame := <-frames:
			select {
			case t.frameCh <- frame:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			switch frame.Type {
			case protocol.CodexWebSocketFrameText, protocol.CodexWebSocketFrameBinary:
				if err := emit(frame); err != nil {
					return nil, err
				}
			case protocol.CodexWebSocketFrameClose:
				if err := emit(frame); err != nil {
					return nil, err
				}
				return nil, nil
			}
		}
	}
}

func newSidebandTestPipeline(t *testing.T, transport providertransport.ProviderTransport, platform string) *Pipeline {
	t.Helper()
	return newSidebandTestPipelineWithAccounts(t, transport, accountreg.Snapshot{
		ID: 1, Platform: platform, Type: "oauth", State: accountreg.StateActive,
		Credentials: map[string]string{"access_token": "provider-token", "base_url": "https://provider.test/v1"},
		Models:      map[string]struct{}{"gpt-realtime": {}}, GroupIDs: map[int]struct{}{7: {}},
	})
}

func newSidebandTestPipelineWithAccounts(t *testing.T, transport providertransport.ProviderTransport, accounts ...accountreg.Snapshot) *Pipeline {
	t.Helper()
	reg := accountreg.New(websocketAccountLoader{accounts: accounts}, nil)
	if err := reg.Reload(context.Background()); err != nil {
		t.Fatalf("load account registry: %v", err)
	}
	return New(Options{
		Accounts: reg, Concurrency: scheduler.NewConcurrencyManager(nil), RPM: scheduler.NewRPMCounter(nil),
		ProviderTransport: transport,
	})
}

func sidebandTestServer(p *Pipeline, pattern string) *httptest.Server {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.UseRawPath = true
	engine.UnescapePathValues = true
	engine.GET(pattern, func(c *gin.Context) {
		// This fixture represents an official Codex sideband websocket. The
		// production handler deliberately rejects ordinary callers on shared
		// /realtime and /live aliases before account selection.
		c.Request.Header.Set("Originator", "codex_cli_rs")
		c.Set(middleware.CtxKeyKeyInfo, &auth.APIKeyInfo{KeyID: 1, UserID: 2, GroupID: 7})
	}, p.HandleRealtimeSidebandWebSocket)
	return httptest.NewServer(engine)
}

func TestRealtimeSidebandStartsWithoutResponseCreateAndPreservesWireContract(t *testing.T) {
	transport := &sidebandTestTransport{
		requestCh: make(chan providertransport.Request, 1),
		frameCh:   make(chan protocol.CodexWebSocketFrame, 16),
		// Realtime sideband has no CPA translation contract, so the plugin-wide
		// translate mode must still dispatch this endpoint through native WS.
		mode: "cpa_translate",
	}
	p := newSidebandTestPipeline(t, transport, "codex")
	server := sidebandTestServer(p, "/live/:call_id")
	defer server.Close()

	header := http.Header{
		"Authorization":                {"Bearer downstream-key"},
		"Cookie":                       {"downstream-session=secret"},
		"X-OpenAI-Actor-Authorization": {"downstream-actor-secret"},
		"X-Session-Id":                 {"session-123"},
		"X-Oai-Attestation":            {"attestation"},
		"X-OpenAI-Subagent":            {"voice"},
		"X-Future-Realtime":            {"preserved"},
		"Sec-WebSocket-Protocol":       {"realtime"},
	}
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/live/call-123?intent=quicksilver&architecture=avas&access_token=downstream-secret"
	conn, response, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		if response != nil {
			t.Fatalf("dial sideband: %v (status=%d)", err, response.StatusCode)
		}
		t.Fatalf("dial sideband: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// The provider request must be dispatched immediately after the downstream
	// 101; no response.create or any other client data frame has been sent yet.
	var req providertransport.Request
	select {
	case req = <-transport.requestCh:
	case <-time.After(2 * time.Second):
		t.Fatal("sideband waited for a response.create frame")
	}
	if req.Endpoint != "realtime_sideband" || req.Method != http.MethodGet || req.Transport != protocol.CodexTransportWebSocket {
		t.Fatalf("wire contract = endpoint:%q method:%q transport:%q", req.Endpoint, req.Method, req.Transport)
	}
	if req.Path != "/live/call-123" || req.Query["intent"][0] != "quicksilver" || req.Query["architecture"][0] != "avas" {
		t.Fatalf("provider target = path:%q query:%#v", req.Path, req.Query)
	}
	if _, leaked := req.Query["access_token"]; leaked {
		t.Fatalf("downstream query credential leaked: %#v", req.Query)
	}
	if req.Headers.Get("Authorization") != "" || req.Headers.Get("Cookie") != "" || req.Headers.Get("X-OpenAI-Actor-Authorization") != "" {
		t.Fatalf("downstream credentials leaked: %#v", req.Headers)
	}
	if req.Headers.Get("Sec-WebSocket-Protocol") != "" {
		t.Fatalf("downstream websocket negotiation header leaked: %#v", req.Headers)
	}
	for name, want := range map[string]string{
		"X-Session-Id": "session-123", "X-Oai-Attestation": "attestation",
		"X-OpenAI-Subagent": "voice", "X-Future-Realtime": "preserved",
	} {
		if got := req.Headers.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}

	binary := []byte{0, 1, 2, 0xff}
	if err := conn.WriteMessage(websocket.BinaryMessage, binary); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	messageType, echoed, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read binary echo: %v", err)
	}
	if messageType != websocket.BinaryMessage || string(echoed) != string(binary) {
		t.Fatalf("binary echo = type:%d data:%v", messageType, echoed)
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"session.update"}`)); err != nil {
		t.Fatalf("write text: %v", err)
	}
	messageType, echoed, err = conn.ReadMessage()
	if err != nil || messageType != websocket.TextMessage || string(echoed) != `{"type":"session.update"}` {
		t.Fatalf("text echo = type:%d data:%q err:%v", messageType, echoed, err)
	}

	if err := conn.WriteControl(websocket.PingMessage, []byte("ping-data"), time.Now().Add(time.Second)); err != nil {
		t.Fatalf("write ping: %v", err)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case frame := <-transport.frameCh:
			if frame.Type == protocol.CodexWebSocketFramePing {
				if string(frame.Data) != "ping-data" {
					t.Fatalf("forwarded ping data = %q", frame.Data)
				}
				return
			}
		case <-deadline:
			t.Fatal("ping control frame was not forwarded")
		}
	}
}

func TestRealtimeSidebandUsesCallCreationAccountAffinity(t *testing.T) {
	transport := &sidebandTestTransport{
		requestCh: make(chan providertransport.Request, 1),
		frameCh:   make(chan protocol.CodexWebSocketFrame, 4),
		mode:      "native",
	}
	account := func(id int) accountreg.Snapshot {
		return accountreg.Snapshot{
			ID: id, Platform: "codex", Type: "oauth", State: accountreg.StateActive,
			Credentials: map[string]string{"access_token": "token", "base_url": "https://provider.test/v1"},
			Models:      map[string]struct{}{"gpt-realtime": {}}, GroupIDs: map[int]struct{}{7: {}},
		}
	}
	p := newSidebandTestPipelineWithAccounts(t, transport, account(1), account(2))
	p.rememberRealtimeCallAccount(2, 7, "https://api.openai.com/v1/realtime/calls/rtc_bound?foo=bar", 2)
	server := sidebandTestServer(p, "/realtime")
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/realtime?call_id=rtc_bound&intent=quicksilver"
	conn, response, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		if response != nil {
			t.Fatalf("dial sideband: %v (status=%d)", err, response.StatusCode)
		}
		t.Fatalf("dial sideband: %v", err)
	}
	defer func() { _ = conn.Close() }()

	select {
	case req := <-transport.requestCh:
		if req.Account.ID != 2 {
			t.Fatalf("sideband account = %d, want call-creation account 2", req.Account.ID)
		}
		if req.Path != "/realtime" || req.Query["call_id"][0] != "rtc_bound" {
			t.Fatalf("sideband target = path:%q query:%#v", req.Path, req.Query)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("sideband provider request was not dispatched")
	}
}

func TestRealtimeSidebandCPAOnlyAndNonCodexPreflightFailClosed(t *testing.T) {
	for _, test := range []struct {
		name     string
		mode     string
		platform string
	}{
		{name: "cpa only", mode: "cpa_only", platform: "codex"},
		{name: "non codex account", mode: "native", platform: "openai-compatible"},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := &sidebandTestTransport{
				requestCh: make(chan providertransport.Request, 1), frameCh: make(chan protocol.CodexWebSocketFrame, 1), mode: test.mode,
			}
			p := newSidebandTestPipeline(t, transport, test.platform)
			gin.SetMode(gin.TestMode)
			engine := gin.New()
			engine.GET("/realtime", func(c *gin.Context) {
				c.Set(middleware.CtxKeyKeyInfo, &auth.APIKeyInfo{KeyID: 1, UserID: 2, GroupID: 7})
			}, p.HandleRealtimeSidebandWebSocket)
			req := httptest.NewRequest(http.MethodGet, "/realtime?call_id=call-123", nil)
			req.Header.Set("Connection", "Upgrade")
			req.Header.Set("Upgrade", "websocket")
			req.Header.Set("Sec-WebSocket-Version", "13")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, req)
			assertWebSocketUpgradeRequired(t, response)
			select {
			case got := <-transport.requestCh:
				t.Fatalf("provider was called after fail-closed preflight: %+v", got)
			default:
			}
		})
	}
}

func TestRealtimeSidebandProviderPathRejectsDotSegments(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, callID := range []string{".", ".."} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Params = gin.Params{{Key: "call_id", Value: callID}}
		if _, err := realtimeSidebandProviderPathChecked(c); err == nil {
			t.Fatalf("call id %q was accepted", callID)
		}
	}
}

func TestValidateRealtimeCallIDRejectsUnsafeValues(t *testing.T) {
	for _, callID := range []string{"", ".", "..", `a\\b`, " a", "a ", "a\x00b", "a\nb", strings.Repeat("x", 513)} {
		if err := validateRealtimeCallID(callID); err == nil {
			t.Errorf("unsafe call id %q was accepted", callID)
		}
	}
	for _, callID := range []string{"call-123", "rtc_abc123", "550e8400-e29b-41d4-a716-446655440000", "../../opaque", "a%2Fb", "a?b#c"} {
		if err := validateRealtimeCallID(callID); err != nil {
			t.Errorf("valid call id %q was rejected: %v", callID, err)
		}
	}
}

func TestRealtimeSidebandQueryCallIDIsValidatedBeforeUpgrade(t *testing.T) {
	transport := &sidebandTestTransport{
		requestCh: make(chan providertransport.Request, 1),
		frameCh:   make(chan protocol.CodexWebSocketFrame, 1),
		mode:      "native",
	}
	p := newSidebandTestPipeline(t, transport, "codex")
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/realtime", func(c *gin.Context) {
		c.Set(middleware.CtxKeyKeyInfo, &auth.APIKeyInfo{KeyID: 1, UserID: 2, GroupID: 7})
	}, p.HandleRealtimeSidebandWebSocket)

	for _, query := range []string{"", "?call_id=..", "?call_id=a%5Cb", "?call_id=a%00b", "?call_id=%20rtc_1", "?call_id=rtc_1&call_id=rtc_2"} {
		req := httptest.NewRequest(http.MethodGet, "/realtime"+query, nil)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Sec-WebSocket-Version", "13")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, req)
		if response.Code != http.StatusBadRequest {
			t.Errorf("query %q status = %d, want 400", query, response.Code)
		}
	}
	select {
	case got := <-transport.requestCh:
		t.Fatalf("provider was called for invalid query call id: %+v", got)
	default:
	}
}

func TestRealtimeSidebandPreservesOfficialEscapedOpaqueCallID(t *testing.T) {
	transport := &sidebandTestTransport{
		requestCh: make(chan providertransport.Request, 1),
		frameCh:   make(chan protocol.CodexWebSocketFrame, 1),
		mode:      "native",
	}
	p := newSidebandTestPipeline(t, transport, "codex")
	p.rememberRealtimeCallAccount(2, 7, "/v1/realtime/calls/..%2F..%2Fopaque", 1)
	server := sidebandTestServer(p, "/live/:call_id")
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/live/..%2F..%2Fopaque"
	conn, response, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		if response != nil {
			t.Fatalf("dial sideband: %v (status=%d)", err, response.StatusCode)
		}
		t.Fatalf("dial sideband: %v", err)
	}
	defer func() { _ = conn.Close() }()

	select {
	case req := <-transport.requestCh:
		if req.Account.ID != 1 {
			t.Fatalf("sideband account = %d, want affinity account 1", req.Account.ID)
		}
		if req.Path != "/live/..%2F..%2Fopaque" {
			t.Fatalf("sideband path = %q", req.Path)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("sideband provider request was not dispatched")
	}
}

func TestRealtimeSidebandRejectsMixedPathAndQueryCallIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/live/:call_id", func(c *gin.Context) {
		c.Set(middleware.CtxKeyKeyInfo, &auth.APIKeyInfo{KeyID: 1, UserID: 2, GroupID: 7})
	}, func(c *gin.Context) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid_call_id"})
	})
	for _, query := range []string{"?call_id=rtc_other", "?call_id=rtc_same", "?call_id=rtc_1&call_id=rtc_2"} {
		req := httptest.NewRequest(http.MethodGet, "/live/rtc_1"+query, nil)
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, req)
		if response.Code != http.StatusBadRequest {
			t.Errorf("query %q status = %d, want 400", query, response.Code)
		}
	}
}
