package pipeline

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
)

func newRemoteControlHeaderTestContext(headers http.Header) *gin.Context {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "http://example.test/remote/control/server", nil)
	c.Request.Header = headers
	return c
}

func TestHandleCodexRemoteControlWebSocketRejectsInvalidOptionalHeaderBeforeUpgrade(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "http://example.test/remote/control/server", nil)
	c.Request.Header["X-Codex-Subscribe-Cursor"] = []string{"one", "two"}
	c.Set(middleware.CtxKeyCodexRemoteControlToken, "remote-token")
	c.Set(middleware.CtxKeyCodexRemoteControlAccountID, 7)

	(*Pipeline)(nil).HandleCodexRemoteControlWebSocket(c)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "invalid_remote_control_header") {
		t.Fatalf("response body = %s", recorder.Body.String())
	}
}

func TestCodexRemoteControlWebSocketOptionalHeadersExtractExplicitly(t *testing.T) {
	c := newRemoteControlHeaderTestContext(http.Header{
		"X-Codex-Host-Device-Kind": {"mac_mini"},
		"x-codex-subscribe-cursor": {"cursor /?"},
	})
	host, cursor, err := codexRemoteControlWebSocketOptionalHeaders(c)
	if err != nil {
		t.Fatal(err)
	}
	if host != "mac_mini" || cursor != "cursor /?" {
		t.Fatalf("optional metadata = (%q, %q)", host, cursor)
	}
	projected := remoteControlWebSocketHeaders(c)
	if projected.Get("X-Codex-Host-Device-Kind") != "" || projected.Get("X-Codex-Subscribe-Cursor") != "" {
		t.Fatalf("optional headers leaked into generic projection: %#v", projected)
	}
}

func TestCodexWebSocketProviderRequestCarriesOptionalFieldsOutOfBand(t *testing.T) {
	c := newRemoteControlHeaderTestContext(http.Header{
		"X-Codex-Host-Device-Kind": {"mac_mini"},
		"X-Codex-Subscribe-Cursor": {"cursor /?"},
	})
	request := codexWebSocketProviderRequest(c, codexWebSocketRoute{
		endpoint:     adaptor.EndpointCodexRemoteControlServerWebSocket,
		providerPath: "/wham/remote/control/server", remoteControl: true,
	}, &accountreg.Snapshot{ID: 7, Platform: "codex", Type: "oauth", Credentials: map[string]string{
		"base_url": "https://chatgpt.com/backend-api/codex",
	}}, nil, "codex", "", time.Now(), nil)
	if request.RemoteControlHostDeviceKind != "mac_mini" || request.RemoteControlSubscribeCursor != "cursor /?" {
		t.Fatalf("provider request optional fields = (%q, %q)", request.RemoteControlHostDeviceKind, request.RemoteControlSubscribeCursor)
	}
	if request.Headers.Get("X-Codex-Host-Device-Kind") != "" || request.Headers.Get("X-Codex-Subscribe-Cursor") != "" {
		t.Fatalf("optional fields also remained in generic headers: %#v", request.Headers)
	}
}

func TestCodexRemoteControlWebSocketOptionalHeadersAllowMissingOrBlankHostKind(t *testing.T) {
	for _, headers := range []http.Header{
		{},
		{"X-Codex-Host-Device-Kind": {""}},
		{"X-Codex-Host-Device-Kind": {"   "}},
	} {
		host, cursor, err := codexRemoteControlWebSocketOptionalHeaders(newRemoteControlHeaderTestContext(headers))
		if err != nil || host != "" || cursor != "" {
			t.Fatalf("headers=%#v -> (%q, %q, %v)", headers, host, cursor, err)
		}
	}
}

func TestCodexRemoteControlWebSocketOptionalHeadersRejectDuplicateOrUnsafeValues(t *testing.T) {
	longCursor := strings.Repeat("x", codexRemoteControlSubscribeCursorMaxBytes+1)
	cases := []struct {
		name    string
		headers http.Header
	}{
		{name: "duplicate cursor", headers: http.Header{"X-Codex-Subscribe-Cursor": {"one", "two"}}},
		{name: "duplicate case variant", headers: http.Header{"X-Codex-Subscribe-Cursor": {"one"}, "x-codex-subscribe-cursor": {"two"}}},
		{name: "unsupported host kind", headers: http.Header{"X-Codex-Host-Device-Kind": {"macbook"}}},
		{name: "host kind whitespace", headers: http.Header{"X-Codex-Host-Device-Kind": {" mac_mini "}}},
		{name: "blank cursor", headers: http.Header{"X-Codex-Subscribe-Cursor": {"   "}}},
		{name: "cursor control", headers: http.Header{"X-Codex-Subscribe-Cursor": {"cursor\nvalue"}}},
		{name: "cursor too long", headers: http.Header{"X-Codex-Subscribe-Cursor": {longCursor}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := codexRemoteControlWebSocketOptionalHeaders(newRemoteControlHeaderTestContext(tc.headers))
			if !errors.Is(err, errCodexRemoteControlWebSocketOptionalHeader) {
				t.Fatalf("error = %v, want optional-header validation error", err)
			}
		})
	}
}
