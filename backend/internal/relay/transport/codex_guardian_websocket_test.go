package transport

import (
	"context"
	"testing"

	"github.com/DouDOU-start/airgate-core/internal/pluginruntime/protocol"
)

type guardianWebSocketManager struct {
	request protocol.CodexExecuteRequest
}

func (m *guardianWebSocketManager) CodexTransportMode() string { return "native" }

func (m *guardianWebSocketManager) ExecuteCodex(context.Context, protocol.CodexExecuteRequest, func(protocol.CodexExecuteEvent) error) error {
	return nil
}

func (m *guardianWebSocketManager) ExecuteCodexWebSocket(
	_ context.Context,
	request protocol.CodexExecuteRequest,
	frames <-chan protocol.CodexWebSocketFrame,
	emit func(protocol.CodexWebSocketFrame) error,
) error {
	m.request = request
	for range frames {
		// The transport contract test only exercises endpoint admission. A real
		// executor owns the duplex frame loop.
	}
	return nil
}

func TestCodexPluginTransportAdmitsGuardianWebSocketEndpoints(t *testing.T) {
	for _, endpoint := range []string{"guardian", "guardian_classifier"} {
		t.Run(endpoint, func(t *testing.T) {
			manager := &guardianWebSocketManager{}
			transport := NewCodexPluginTransport(manager)
			frames := make(chan protocol.CodexWebSocketFrame)
			close(frames)
			if _, err := transport.ExecuteWebSocket(context.Background(), Request{
				Account:   Account{ID: 7, Platform: "codex", Type: "oauth", Credentials: map[string]string{"access_token": "token"}},
				RequestID: "guardian-test", Client: "codex", GroupID: 11, Method: "GET", BaseURL: "https://chatgpt.com/backend-api/codex",
				Path: "/" + endpoint, Endpoint: endpoint, EntryProtocol: "openai", Transport: protocol.CodexTransportWebSocket,
			}, frames, func(protocol.CodexWebSocketFrame) error { return nil }); err != nil {
				t.Fatalf("ExecuteWebSocket(%q) error = %v", endpoint, err)
			}
			if manager.request.Endpoint != endpoint || manager.request.Transport != protocol.CodexTransportWebSocket || manager.request.Client != "codex" {
				t.Fatalf("manager request = %+v", manager.request)
			}
		})
	}
}
