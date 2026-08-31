package pipeline

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

const (
	// These are the two optional headers emitted by the official Codex Remote
	// Control websocket client.  They are deliberately parsed into explicit
	// protocol fields instead of being forwarded through the generic X-Codex-*
	// header projection.
	codexRemoteControlHostDeviceKindHeader  = "X-Codex-Host-Device-Kind"
	codexRemoteControlSubscribeCursorHeader = "X-Codex-Subscribe-Cursor"
	codexRemoteControlHostDeviceKindMacMini = "mac_mini"
	// Subscribe cursors are opaque provider state.  Keep enough room for a
	// normal cursor while bounding memory and header amplification at the trust
	// boundary.  This matches the existing 4 KiB Remote Control token limit.
	codexRemoteControlSubscribeCursorMaxBytes = 4096
)

var errCodexRemoteControlWebSocketOptionalHeader = errors.New("invalid Codex Remote Control websocket optional header")

// codexRemoteControlWebSocketOptionalHeaders extracts the official optional
// handshake headers exactly once.  A header with more than one value is
// rejected rather than choosing an arbitrary value; this prevents a proxy or
// caller from creating divergent Core/plugin/upstream views of the handshake.
func codexRemoteControlWebSocketOptionalHeaders(c *gin.Context) (hostDeviceKind, subscribeCursor string, err error) {
	if c == nil || c.Request == nil {
		return "", "", nil
	}
	hostDeviceKind, err = codexRemoteControlSingleOptionalHeader(c.Request.Header, codexRemoteControlHostDeviceKindHeader)
	if err != nil {
		return "", "", err
	}
	subscribeCursor, err = codexRemoteControlSingleOptionalHeader(c.Request.Header, codexRemoteControlSubscribeCursorHeader)
	if err != nil {
		return "", "", err
	}
	return hostDeviceKind, subscribeCursor, nil
}

func codexRemoteControlSingleOptionalHeader(headers http.Header, name string) (string, error) {
	values := make([]string, 0, 1)
	for key, entries := range headers {
		if !strings.EqualFold(strings.TrimSpace(key), name) {
			continue
		}
		values = append(values, entries...)
	}
	if len(values) > 1 {
		return "", fmt.Errorf("%w: %s must contain exactly one value", errCodexRemoteControlWebSocketOptionalHeader, name)
	}
	if len(values) == 0 {
		return "", nil
	}
	raw := values[0]
	if !utf8.ValidString(raw) {
		return "", fmt.Errorf("%w: %s is not valid UTF-8", errCodexRemoteControlWebSocketOptionalHeader, name)
	}
	for _, r := range raw {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("%w: %s contains a control character", errCodexRemoteControlWebSocketOptionalHeader, name)
		}
	}

	switch strings.ToLower(name) {
	case strings.ToLower(codexRemoteControlHostDeviceKindHeader):
		// The current official client only emits mac_mini.  Empty is treated as
		// absent so an explicitly blank optional header remains harmless.
		value := strings.TrimSpace(raw)
		if value == "" {
			return "", nil
		}
		if value != raw || value != codexRemoteControlHostDeviceKindMacMini {
			return "", fmt.Errorf("%w: %s must be %q", errCodexRemoteControlWebSocketOptionalHeader, name, codexRemoteControlHostDeviceKindMacMini)
		}
		return value, nil
	case strings.ToLower(codexRemoteControlSubscribeCursorHeader):
		if len(raw) == 0 || len(raw) > codexRemoteControlSubscribeCursorMaxBytes || strings.TrimSpace(raw) == "" {
			return "", fmt.Errorf("%w: %s must be a non-blank value no longer than %d bytes", errCodexRemoteControlWebSocketOptionalHeader, name, codexRemoteControlSubscribeCursorMaxBytes)
		}
		return raw, nil
	default:
		return "", fmt.Errorf("%w: unsupported header %s", errCodexRemoteControlWebSocketOptionalHeader, name)
	}
}
