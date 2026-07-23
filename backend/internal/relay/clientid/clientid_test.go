package clientid

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestDetect(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name    string
		ua      string
		headers map[string]string
		want    string
	}{
		{"plain client", "curl/8.0", nil, ""},
		{"claude code", "claude-cli/2.1.22", nil, ClaudeCode},
		{"claude code case insensitive", "Claude-CLI/1.0.0", nil, ClaudeCode},
		{"codex ua", "codex/0.1.2025", nil, Codex},
		{"codex-cli ua", "openai-codex-cli/1.0", nil, Codex},
		{"codex fingerprint header", "python-requests/2.31", map[string]string{"X-Codex-Window-Id": "abc"}, Codex},
		{"claude code takes priority over codex header", "claude-cli/2.0.0", map[string]string{"X-Codex-Window-Id": "abc"}, ClaudeCode},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			req, _ := http.NewRequest("POST", "/v1/messages", nil)
			req.Header.Set("User-Agent", tt.ua)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			c.Request = req

			Detect(c)
			got := Get(c)
			if got != tt.want {
				t.Errorf("Detect() got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMatches(t *testing.T) {
	tests := []struct {
		name       string
		clientType string
		allowed    []string
		want       bool
	}{
		{"empty allowed = no restriction", "", nil, true},
		{"empty allowed with client", ClaudeCode, nil, true},
		{"match claude_code", ClaudeCode, []string{ClaudeCode}, true},
		{"match codex", Codex, []string{Codex}, true},
		{"match multi", ClaudeCode, []string{Codex, ClaudeCode}, true},
		{"no match", "", []string{ClaudeCode}, false},
		{"wrong type", Codex, []string{ClaudeCode}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Matches(tt.clientType, tt.allowed); got != tt.want {
				t.Errorf("Matches(%q, %v) = %v, want %v", tt.clientType, tt.allowed, got, tt.want)
			}
		})
	}
}
