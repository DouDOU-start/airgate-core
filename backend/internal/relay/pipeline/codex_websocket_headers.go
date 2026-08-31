package pipeline

import (
	"net/http"
	"sort"
	"strings"
)

// projectCodexWebSocketHandshakeHeaders returns the small set of upstream
// response headers that the official Codex Responses WebSocket client consumes
// during the HTTP 101 handshake.
//
// Responses WebSocket account selection currently happens after Core has
// accepted the downstream upgrade, so these headers cannot be copied into the
// already-committed response in that path.  Keeping the projection in one
// helper gives the buffered/prepared-upgrade path a strict, auditable allowlist
// and prevents provider credentials or hop-by-hop fields from crossing the
// boundary accidentally.
//
// Header names are matched case-insensitively.  The upstream contract defines
// each selected field as a single value; when a malformed/proxy response
// contains duplicates, the first valid value wins.  Values containing CR/LF
// are ignored to prevent response-header injection if an executor is ever
// supplied by an untrusted process.
func projectCodexWebSocketHandshakeHeaders(src map[string][]string) http.Header {
	dst := make(http.Header)
	if src == nil {
		return dst
	}

	const (
		turnState   = "x-codex-turn-state"
		reasoning   = "x-reasoning-included"
		serverModel = "openai-model"
		modelsETag  = "x-models-etag"
	)
	allowed := map[string]string{
		turnState:   "X-Codex-Turn-State",
		reasoning:   "X-Reasoning-Included",
		serverModel: "OpenAI-Model",
		modelsETag:  "X-Models-Etag",
	}

	names := make([]string, 0, len(src))
	for name := range src {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		values := src[name]
		canonical, ok := allowed[strings.ToLower(strings.TrimSpace(name))]
		if !ok || len(values) == 0 {
			continue
		}
		for _, value := range values {
			if strings.ContainsAny(value, "\r\n") {
				continue
			}
			canonical = http.CanonicalHeaderKey(canonical)
			// The official headers are scalar.  Ignore duplicates rather than
			// allowing a proxy to make the client choose an ambiguous value.
			if _, exists := dst[canonical]; exists {
				break
			}
			dst[canonical] = []string{value}
			break
		}
	}
	return dst
}
