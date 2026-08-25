package plugin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

func TestBuildWebSocketHeadersFiltersHandshakeAndCredentials(t *testing.T) {
	keyInfo := &auth.APIKeyInfo{UserID: 7, KeyID: 8, GroupID: 9}
	source := http.Header{
		"Authorization":            {"Bearer client-secret"},
		"X-API-Key":                {"sk-client"},
		"Cookie":                   {"session=secret"},
		"Host":                     {"client.example"},
		"Connection":               {"Upgrade"},
		"Upgrade":                  {"websocket"},
		"Sec-WebSocket-Key":        {"generated-by-client"},
		"Sec-WebSocket-Version":    {"13"},
		"Sec-WebSocket-Extensions": {"permessage-deflate"},
		"Sec-WebSocket-Protocol":   {"client-subprotocol"},
		"Sec-WebSocket-Accept":     {"client-accept"},
		"Content-Length":           {"123"},
		"X-Forwarded-For":          {"198.51.100.2"},
		"User-Agent":               {"codex-test/1"},
		"Traceparent":              {"00-abc-def-01"},
		"X-Client-Metadata":        {"keep-me"},
	}

	got := buildWebSocketHeaders(source, keyInfo)
	for _, name := range []string{
		"Authorization", "X-API-Key", "Cookie", "Host", "Connection", "Upgrade",
		"Sec-WebSocket-Key", "Sec-WebSocket-Version", "Sec-WebSocket-Extensions",
		"Sec-WebSocket-Protocol", "Sec-WebSocket-Accept",
		"Content-Length", "X-Forwarded-For",
	} {
		if got.Get(name) != "" {
			t.Errorf("header %s was forwarded: %q", name, got.Get(name))
		}
	}
	if got.Get("User-Agent") != "codex-test/1" || got.Get("Traceparent") != "00-abc-def-01" || got.Get("X-Client-Metadata") != "keep-me" {
		t.Fatalf("ordinary client metadata was not preserved: %#v", got)
	}
	if got.Get("X-Airgate-User-ID") != "7" || got.Get("X-Airgate-API-Key-ID") != "8" || got.Get("X-Airgate-Group-ID") != "9" {
		t.Fatalf("internal access metadata missing: %#v", got)
	}
}

func TestWebSocketConnectInfoUsesRequestMetadataAndAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	req := httptest.NewRequest(http.MethodGet, "/v1/responses?model=gpt-5.4&x=1", nil)
	req.RemoteAddr = "192.0.2.10:54321"
	req.Header.Set("Authorization", "Bearer client-secret")
	req.Header.Set("User-Agent", "codex-test/1")
	req.Header.Set("Cookie", "session=secret")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	c.Set(middleware.CtxKeyRequestID, "req-ws-1")

	state := &forwardState{
		requestPath: "/v1/responses",
		keyInfo:     &auth.APIKeyInfo{},
		account: &ent.Account{
			ID:          42,
			Name:        "oauth-account",
			Platform:    "openai",
			Type:        "oauth",
			Credentials: map[string]string{"access_token": "opaque"},
		},
	}
	info := websocketConnectInfo(c, state)
	if info.Path != "/v1/responses" || info.Query != "model=gpt-5.4&x=1" || info.RemoteAddr != "192.0.2.10:54321" {
		t.Fatalf("connect info request metadata = %#v", info)
	}
	if info.ConnectionID != "req-ws-1" || info.Account == nil || info.Account.ID != 42 {
		t.Fatalf("connect info identity = %#v", info)
	}
	if info.Headers.Get("Authorization") != "" || info.Headers.Get("Cookie") != "" {
		t.Fatalf("sensitive headers leaked: %#v", info.Headers)
	}
	if info.Headers.Get("User-Agent") != "codex-test/1" {
		t.Fatalf("user-agent was lost: %#v", info.Headers)
	}
}

func TestCheckWebSocketOrigin(t *testing.T) {
	cases := []struct {
		name   string
		origin string
		host   string
		want   bool
	}{
		{name: "non browser", host: "gateway.example", want: true},
		{name: "same origin", origin: "https://gateway.example", host: "gateway.example", want: true},
		{name: "cross origin", origin: "https://evil.example", host: "gateway.example", want: false},
		{name: "malformed", origin: "://bad", host: "gateway.example", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://"+tc.host+"/v1/responses", nil)
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if got := checkWebSocketOrigin(req); got != tc.want {
				t.Fatalf("checkWebSocketOrigin() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWebSocketSchedulingModelsUnknownUsesEmptyCandidate(t *testing.T) {
	models := websocketSchedulingModels(nil, "openai", "gateway-openai", "/v1/responses", "")
	if len(models) != 1 || models[0] != "" {
		t.Fatalf("unknown model candidates = %#v, want one empty candidate", models)
	}
}

func TestGorillaWebSocketConnRejectsUnknownMessageType(t *testing.T) {
	conn := &gorillaWebSocketConn{}
	if err := conn.WriteMessage(99, []byte("x")); err == nil {
		t.Fatal("WriteMessage accepted an unknown SDK message type")
	}
	if got := conn.ConnectInfo(); got != nil {
		t.Fatalf("nil adapter info = %#v", got)
	}
	_ = sdk.WSMessageText // keep SDK constants part of this focused adapter test
}

func TestWebSocketConnectInfoHandlesNilInputs(t *testing.T) {
	info := websocketConnectInfo(nil, nil)
	if info == nil {
		t.Fatal("nil inputs returned nil connect info")
	}
	if info.Path != "" || info.Query != "" || info.RemoteAddr != "" || info.Account != nil {
		t.Fatalf("nil input metadata = %#v", info)
	}
	if info.Headers == nil {
		t.Fatal("nil input headers should be an initialized map")
	}
	if info.ConnectionID == "" {
		t.Fatal("nil input connect info did not receive a connection id")
	}
}

func TestTruncateWebSocketCloseReason(t *testing.T) {
	long := strings.Repeat("\u754c", 100)
	got := truncateWebSocketCloseReason(long)
	if len([]byte(got)) > 123 {
		t.Fatalf("close reason is %d bytes, want <= 123", len([]byte(got)))
	}
	if !utf8.ValidString(got) {
		t.Fatalf("close reason is not valid UTF-8: %q", got)
	}
	if got == "" {
		t.Fatal("close reason was unexpectedly emptied")
	}
	if got := truncateWebSocketCloseReason("\xffbad"); got != "bad" {
		t.Fatalf("invalid UTF-8 was not sanitized: %q", got)
	}
}
