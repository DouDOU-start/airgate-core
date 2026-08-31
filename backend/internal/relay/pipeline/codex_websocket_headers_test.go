package pipeline

import (
	"net/http"
	"testing"
)

func TestProjectCodexWebSocketHandshakeHeadersAllowlist(t *testing.T) {
	got := projectCodexWebSocketHandshakeHeaders(map[string][]string{
		"x-codex-turn-state":   {"turn-1"},
		"X-REASONING-INCLUDED": {"true"},
		"OpenAI-Model":         {"gpt-5.6"},
		"x-models-etag":        {"etag-1"},
		"x-request-id":         {"req-1"},
		"set-cookie":           {"must-not-cross"},
		"authorization":        {"secret"},
	})

	for name, want := range map[string]string{
		"X-Codex-Turn-State":   "turn-1",
		"X-Reasoning-Included": "true",
		"OpenAI-Model":         "gpt-5.6",
		"X-Models-Etag":        "etag-1",
	} {
		if got.Get(name) != want {
			t.Fatalf("%s = %q, want %q", name, got.Get(name), want)
		}
	}
	for _, name := range []string{"X-Request-Id", "Set-Cookie", "Authorization"} {
		if got.Get(name) != "" {
			t.Fatalf("unexpected projected header %s = %q", name, got.Get(name))
		}
	}
}

func TestProjectCodexWebSocketHandshakeHeadersRejectsInjectedValuesAndDuplicates(t *testing.T) {
	got := projectCodexWebSocketHandshakeHeaders(map[string][]string{
		"x-codex-turn-state": {"bad\r\nX-Leak: yes", "turn-good"},
		"openai-model":       {"model-one", "model-two"},
	})

	if got.Get("X-Codex-Turn-State") != "turn-good" {
		t.Fatalf("turn state = %q, want first valid value", got.Get("X-Codex-Turn-State"))
	}
	if values := got.Values("OpenAI-Model"); len(values) != 1 || values[0] != "model-one" {
		t.Fatalf("model values = %#v, want [model-one]", values)
	}
}

func TestProjectCodexWebSocketHandshakeHeadersReturnsNonNilEmptyHeader(t *testing.T) {
	got := projectCodexWebSocketHandshakeHeaders(nil)
	if got == nil {
		t.Fatal("nil source returned nil header map")
	}
	if len(got) != 0 {
		t.Fatalf("nil source projected headers = %#v", got)
	}
	// Keep the expected concrete type explicit for callers passing the result
	// directly to websocket.Upgrader.Upgrade.
	var _ http.Header = got
}
