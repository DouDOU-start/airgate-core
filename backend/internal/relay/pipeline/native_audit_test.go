package pipeline

import (
	"bytes"
	"net/http"
	"testing"
)

func TestRedactNativeAuditBodyKeepsNonRemoteBodiesByteExact(t *testing.T) {
	body := []byte(`{"model":"gpt-test","input":"hello"}`)
	got := redactNativeAuditBody("https://provider.example/v1/responses", body)
	if !bytes.Equal(got, body) {
		t.Fatalf("non-Remote-Control audit body changed: got %q want %q", got, body)
	}
	got[0] = 'X'
	if body[0] == 'X' {
		t.Fatal("audit body did not return an independent copy")
	}
}

func TestRedactNativeAuditBodyRemovesRemoteControlSecretsAndMetadata(t *testing.T) {
	body := []byte(`{"pairing_code":"PAIR-1234","nested":{"remoteControlToken":"server-token","access-token":"oauth-token"},"server_id":"server-1","environment_id":"env-1","installationId":"install-1","name":"my-laptop","safe":"kept"}`)
	got := redactNativeAuditBody("https://chatgpt.example/backend-api/wham/remote/control/server/pair", body)
	if bytes.Contains(got, []byte("PAIR-1234")) ||
		bytes.Contains(got, []byte("server-token")) ||
		bytes.Contains(got, []byte("oauth-token")) ||
		bytes.Contains(got, []byte("server-1")) ||
		bytes.Contains(got, []byte("env-1")) ||
		bytes.Contains(got, []byte("install-1")) ||
		bytes.Contains(got, []byte("my-laptop")) {
		t.Fatalf("Remote-Control audit body contains sensitive data: %s", got)
	}
	if !bytes.Contains(got, []byte(`"safe":"kept"`)) {
		t.Fatalf("non-sensitive Remote-Control field was unexpectedly removed: %s", got)
	}
	if bytes.Equal(got, body) {
		t.Fatal("Remote-Control audit body was not redacted")
	}
	if !bytes.Contains(body, []byte("PAIR-1234")) {
		t.Fatal("redaction mutated the provider body")
	}
}

func TestRedactNativeAuditBodyFailsClosedForMalformedRemoteControlBody(t *testing.T) {
	got := redactNativeAuditBody("/remote/control/server/refresh", []byte("opaque=secret-token"))
	if string(got) != "[redacted remote-control body]" {
		t.Fatalf("malformed Remote-Control body = %q", got)
	}
}

func TestSanitizeNativeAuditURLRemovesCredentialQueryAndUserinfo(t *testing.T) {
	got := sanitizeNativeAuditURL("https://user:password@example.test/remote/control/server/pair?pairing_code=PAIR&access_token=TOKEN&safe=1#fragment")
	for _, secret := range []string{"password", "PAIR", "TOKEN", "fragment"} {
		if bytes.Contains([]byte(got), []byte(secret)) {
			t.Fatalf("sanitized audit URL contains %q: %s", secret, got)
		}
	}
	if !bytes.Contains([]byte(got), []byte("pairing_code=%5Bredacted%5D")) ||
		!bytes.Contains([]byte(got), []byte("access_token=%5Bredacted%5D")) ||
		!bytes.Contains([]byte(got), []byte("safe=1")) {
		t.Fatalf("sanitized audit URL lost expected query shape: %s", got)
	}
}

func TestCloneNativeAuditHeadersDropsCredentialAndPairingHeaders(t *testing.T) {
	in := http.Header{
		"Authorization":                 {"Bearer secret"},
		"X-Codex-Remote-Control-Token":  {"server-token"},
		"X-Remote-Control-Pairing-Code": {"PAIR"},
		"X-Codex-Server-Id":             {"server-1"},
		"ChatGPT-Account-ID":            {"account-1"},
		"X-Request-Id":                  {"request-1"},
		"Content-Type":                  {"application/json"},
		"Content-Length":                {"42"},
		"X-Codex-Protocol-Version":      {"1"},
	}
	got := cloneNativeAuditHeaders(in)
	for _, key := range []string{"Authorization", "X-Codex-Remote-Control-Token", "X-Remote-Control-Pairing-Code", "X-Codex-Server-Id", "ChatGPT-Account-ID", "Content-Length"} {
		if got.Get(key) != "" {
			t.Fatalf("sensitive audit header %q was retained: %#v", key, got)
		}
	}
	for _, key := range []string{"X-Request-Id", "Content-Type", "X-Codex-Protocol-Version"} {
		if got.Get(key) == "" {
			t.Fatalf("safe audit header %q was removed: %#v", key, got)
		}
	}
}
