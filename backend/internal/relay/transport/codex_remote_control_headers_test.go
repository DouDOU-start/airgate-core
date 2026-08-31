package transport

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/DouDOU-start/airgate-core/internal/pluginruntime/protocol"
)

type optionalHeaderWebSocketManager struct {
	request protocol.CodexExecuteRequest
	called  bool
}

func (m *optionalHeaderWebSocketManager) CodexTransportMode() string { return "native" }

func (m *optionalHeaderWebSocketManager) ExecuteCodex(context.Context, protocol.CodexExecuteRequest, func(protocol.CodexExecuteEvent) error) error {
	return nil
}

func (m *optionalHeaderWebSocketManager) ExecuteCodexWebSocket(
	_ context.Context,
	request protocol.CodexExecuteRequest,
	frames <-chan protocol.CodexWebSocketFrame,
	emit func(protocol.CodexWebSocketFrame) error,
) error {
	m.called = true
	m.request = request
	for range frames {
	}
	return nil
}

func TestCodexPluginTransportMapsRemoteControlOptionalWebSocketHeaders(t *testing.T) {
	m := &optionalHeaderWebSocketManager{}
	frames := make(chan protocol.CodexWebSocketFrame)
	close(frames)
	_, err := NewCodexPluginTransport(m).ExecuteWebSocket(context.Background(), Request{
		Account:   Account{ID: 7, Platform: "codex", Type: "oauth", Credentials: map[string]string{"access_token": "token"}},
		RequestID: "remote-optional", Client: "codex", Method: http.MethodGet,
		BaseURL: "https://chatgpt.com/backend-api/codex", Path: "/wham/remote/control/server",
		Endpoint: protocol.CodexEndpointRemoteControlServerWebSocket, EntryProtocol: "openai", Transport: protocol.CodexTransportWebSocket,
		RemoteControlToken: "remote-token", RemoteControlServerID: "server-1", RemoteControlName: "server",
		RemoteControlProtocolVersion: "3", InstallationID: "install-1",
		RemoteControlHostDeviceKind: "mac_mini", RemoteControlSubscribeCursor: "cursor /?",
		Headers: map[string][]string{
			"X-Codex-Host-Device-Kind": {"spoofed-host"},
			"X-Codex-Subscribe-Cursor": {"spoofed-cursor"},
		},
	}, frames, func(protocol.CodexWebSocketFrame) error { return nil })
	if err != nil || !m.called {
		t.Fatalf("ExecuteWebSocket() error=%v called=%v", err, m.called)
	}
	if m.request.RemoteControlHostDeviceKind != "mac_mini" || m.request.RemoteControlSubscribeCursor != "cursor /?" {
		t.Fatalf("optional fields not mapped: %+v", m.request)
	}
	if m.request.Header != nil {
		for name := range m.request.Header {
			if strings.EqualFold(name, "X-Codex-Host-Device-Kind") || strings.EqualFold(name, "X-Codex-Subscribe-Cursor") {
				t.Fatalf("optional spoof header crossed Core transport boundary: %#v", m.request.Header)
			}
		}
	}
}

func TestCodexPluginTransportRejectsInvalidRemoteControlOptionalWebSocketHeaders(t *testing.T) {
	cases := []struct {
		name   string
		host   string
		cursor string
	}{
		{name: "unsupported host", host: "macbook"},
		{name: "host whitespace", host: " mac_mini "},
		{name: "blank cursor", cursor: " "},
		{name: "cursor control", cursor: "cursor\nvalue"},
		{name: "cursor too long", cursor: strings.Repeat("x", codexRemoteControlSubscribeCursorMaxBytes+1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &optionalHeaderWebSocketManager{}
			frames := make(chan protocol.CodexWebSocketFrame)
			close(frames)
			_, err := NewCodexPluginTransport(m).ExecuteWebSocket(context.Background(), Request{
				Account: Account{ID: 7, Platform: "codex", Type: "oauth"}, RequestID: "remote-invalid", Method: http.MethodGet,
				BaseURL: "https://chatgpt.com/backend-api/codex", Path: "/wham/remote/control/server",
				Endpoint: protocol.CodexEndpointRemoteControlServerWebSocket, EntryProtocol: "openai", Transport: protocol.CodexTransportWebSocket,
				RemoteControlToken: "token", RemoteControlServerID: "server", RemoteControlName: "name",
				RemoteControlProtocolVersion: "3", InstallationID: "install", RemoteControlHostDeviceKind: tc.host,
				RemoteControlSubscribeCursor: tc.cursor,
			}, frames, func(protocol.CodexWebSocketFrame) error { return nil })
			if !errors.Is(err, ErrCodexRemoteControlWebSocketOptionalHeaderInvalid) || m.called {
				t.Fatalf("error=%v called=%v, want validation failure before manager", err, m.called)
			}
		})
	}
}
