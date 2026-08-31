package transport

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/DouDOU-start/airgate-core/internal/pluginruntime/protocol"
)

const (
	codexRemoteControlHostDeviceKindMacMini   = "mac_mini"
	codexRemoteControlSubscribeCursorMaxBytes = 4096
)

// ErrCodexRemoteControlWebSocketOptionalHeaderInvalid marks a malformed
// optional Remote Control handshake field at the Core/plugin trust boundary.
// The plugin is expected to perform the same validation before dialing so a
// direct RPC caller cannot bypass Core's HTTP-header checks.
var ErrCodexRemoteControlWebSocketOptionalHeaderInvalid = errors.New("invalid Codex Remote Control websocket optional header")

func validateCodexRemoteControlWebSocketOptionalHeaders(hostDeviceKind, subscribeCursor string) error {
	if err := validateCodexRemoteControlHostDeviceKind(hostDeviceKind); err != nil {
		return err
	}
	if err := validateCodexRemoteControlSubscribeCursor(subscribeCursor); err != nil {
		return err
	}
	return nil
}

func validateCodexRemoteControlHostDeviceKind(value string) error {
	if value == "" {
		return nil
	}
	if !utf8.ValidString(value) || strings.TrimSpace(value) != value || value != codexRemoteControlHostDeviceKindMacMini {
		return fmt.Errorf("%w: host device kind must be empty or %q", ErrCodexRemoteControlWebSocketOptionalHeaderInvalid, codexRemoteControlHostDeviceKindMacMini)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: host device kind contains a control character", ErrCodexRemoteControlWebSocketOptionalHeaderInvalid)
		}
	}
	return nil
}

func validateCodexRemoteControlSubscribeCursor(value string) error {
	if value == "" {
		return nil
	}
	if !utf8.ValidString(value) || len(value) > codexRemoteControlSubscribeCursorMaxBytes || strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: subscribe cursor must be empty or a non-blank value no longer than %d bytes", ErrCodexRemoteControlWebSocketOptionalHeaderInvalid, codexRemoteControlSubscribeCursorMaxBytes)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: subscribe cursor must not contain control characters", ErrCodexRemoteControlWebSocketOptionalHeaderInvalid)
		}
	}
	return nil
}

func isCodexRemoteControlWebSocketRequest(endpoint, path string) bool {
	switch strings.ToLower(strings.TrimSpace(endpoint)) {
	case protocol.CodexEndpointRemoteControl, protocol.CodexEndpointRemoteControlServerWebSocket, "remote_control", "remote-control":
		return true
	case "":
		canonical := strings.ToLower(strings.TrimRight(strings.TrimSpace(path), "/"))
		return strings.HasSuffix(canonical, "/remote/control/server") || canonical == "remote/control/server"
	default:
		return false
	}
}
