package pipeline

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	providertransport "github.com/DouDOU-start/airgate-core/internal/relay/transport"
)

func TestCodexWebSocketTransportModeBoundary(t *testing.T) {
	responses := codexWebSocketRoute{endpoint: adaptor.EndpointResponses}
	remoteControl := codexWebSocketRoute{
		endpoint:      adaptor.EndpointCodexRemoteControlServerWebSocket,
		remoteControl: true,
	}
	realtime := codexWebSocketRoute{endpoint: adaptor.EndpointRealtimeSideband}

	tests := []struct {
		name  string
		route codexWebSocketRoute
		mode  providertransport.CodexTransportMode
		want  bool
	}{
		{name: "responses cpa only", route: responses, mode: providertransport.CodexModeCPAOnly, want: true},
		{name: "remote control cpa only", route: remoteControl, mode: providertransport.CodexModeCPAOnly, want: true},
		{name: "realtime cpa only", route: realtime, mode: providertransport.CodexModeCPAOnly, want: true},
		{name: "responses cpa translate", route: responses, mode: providertransport.CodexModeCPATranslate, want: true},
		{name: "remote control cpa translate", route: remoteControl, mode: providertransport.CodexModeCPATranslate, want: false},
		{name: "realtime cpa translate", route: realtime, mode: providertransport.CodexModeCPATranslate, want: false},
		{name: "responses native only", route: responses, mode: providertransport.CodexModeNativeOnly, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := codexWebSocketBlockedByTransportMode(test.route, test.mode); got != test.want {
				t.Fatalf("blocked = %v, want %v", got, test.want)
			}
		})
	}
}

func TestHandleResponsesWebSocketFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/responses", (&Pipeline{}).HandleResponsesWebSocketFallback)

	req := httptest.NewRequest(http.MethodGet, "/responses", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	resp := httptest.NewRecorder()
	engine.ServeHTTP(resp, req)

	if resp.Code != http.StatusUpgradeRequired {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusUpgradeRequired)
	}
	if got := resp.Header().Get("Connection"); got != "Upgrade" {
		t.Fatalf("Connection = %q, want Upgrade", got)
	}
	if got := resp.Header().Get("Upgrade"); got != "websocket" {
		t.Fatalf("Upgrade = %q, want websocket", got)
	}
	if got := resp.Header().Get("Sec-WebSocket-Version"); got != "13" {
		t.Fatalf("Sec-WebSocket-Version = %q, want 13", got)
	}
	if got := resp.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if !strings.Contains(resp.Body.String(), "websocket_not_available") {
		t.Fatalf("body = %q, want websocket_not_available error", resp.Body.String())
	}
}

func TestHandleCodexGuardianUnsupported(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/guardian", (&Pipeline{}).HandleCodexGuardianUnsupported)

	req := httptest.NewRequest(http.MethodPost, "/guardian", strings.NewReader(`{"model":"codex-auto-review"}`))
	resp := httptest.NewRecorder()
	engine.ServeHTTP(resp, req)

	if resp.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusNotImplemented)
	}
	if got := resp.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	body := resp.Body.String()
	if !strings.Contains(body, `"unsupported_endpoint"`) {
		t.Fatalf("body = %q, want unsupported_endpoint error", body)
	}
	if strings.Contains(body, "codex-auto-review") {
		t.Fatalf("body leaked request payload: %q", body)
	}
}

func TestHandleCodexAgentIdentityJWKSUnsupported(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/api/codex/agent-identities/jwks", (&Pipeline{}).HandleCodexAgentIdentityJWKSUnsupported)
	engine.POST("/api/codex/agent-identities/jwks", (&Pipeline{}).HandleCodexAgentIdentityJWKSUnsupported)

	resp := httptest.NewRecorder()
	engine.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/codex/agent-identities/jwks", nil))
	if resp.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusNotImplemented)
	}
	if got := resp.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if got := resp.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	body := resp.Body.String()
	if !strings.Contains(body, `"agent_identity_unsupported"`) {
		t.Fatalf("body = %q, want agent_identity_unsupported error", body)
	}

	resp = httptest.NewRecorder()
	engine.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, "/api/codex/agent-identities/jwks", nil))
	if resp.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want %d", resp.Code, http.StatusMethodNotAllowed)
	}
	if got := resp.Header().Get("Allow"); got != http.MethodGet {
		t.Fatalf("POST Allow = %q, want GET", got)
	}
}
