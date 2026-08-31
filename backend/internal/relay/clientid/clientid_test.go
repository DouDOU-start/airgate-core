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
		{"bare codex-cli fallback ua", "codex-cli", nil, Codex},
		{"official codex ua", "codex_cli_rs/0.144.0 (Windows; x86_64) rust", nil, Codex},
		{"embedded official codex ua", "codex_app_server_daemon/1.2.3 codex_cli_rs/1.2.3", nil, Codex},
		{"official originator", "curl/8.0", map[string]string{"Originator": "codex_cli_rs"}, Codex},
		{"official originator normalized", "curl/8.0", map[string]string{"Originator": " CODEX_CLI_RS "}, Codex},
		{"official tui originator", "curl/8.0", map[string]string{"Originator": "codex-tui"}, Codex},
		{"official vscode originator", "curl/8.0", map[string]string{"Originator": "codex_vscode"}, Codex},
		{"official atlas originator", "curl/8.0", map[string]string{"Originator": "codex_atlas"}, Codex},
		{"official desktop originator", "curl/8.0", map[string]string{"Originator": "codex_chatgpt_desktop"}, Codex},
		{"official work desktop originator", "curl/8.0", map[string]string{"Originator": "codex_work_desktop"}, Codex},
		{"official work web originator", "curl/8.0", map[string]string{"Originator": "codex_work_web"}, Codex},
		{"official work mobile originator", "curl/8.0", map[string]string{"Originator": "codex_work_mobile"}, Codex},
		{"official work cca originator", "curl/8.0", map[string]string{"Originator": "codex_work_cca"}, Codex},
		{"official chatgpt cca originator", "curl/8.0", map[string]string{"Originator": "chatgpt_cca"}, Codex},
		{"official named product originator", "curl/8.0", map[string]string{"Originator": "Codex Desktop"}, Codex},
		{"official exec originator", "curl/8.0", map[string]string{"Originator": "codex_exec"}, Codex},
		{"official TypeScript SDK originator", "curl/8.0", map[string]string{"Originator": "codex_sdk_ts"}, Codex},
		{"official TypeScript SDK originator normalized", "curl/8.0", map[string]string{"Originator": " CODEX_SDK_TS "}, Codex},
		{"official vscode ua", "codex_vscode/0.144.0 (Windows; x86_64) rust", nil, Codex},
		{"official exec ua", "codex_exec/0.144.0 (Linux; x86_64) rust", nil, Codex},
		{"official work desktop ua", "codex_work_desktop/0.144.0 (Windows; x86_64) rust", nil, Codex},
		{"official work web ua", "codex_work_web/0.144.0 (Linux; x86_64) rust", nil, Codex},
		{"official work mobile ua", "codex_work_mobile/0.144.0 (iOS; arm64) rust", nil, Codex},
		{"official work cca ua", "codex_work_cca/0.144.0 (Linux; x86_64) rust", nil, Codex},
		{"official chatgpt cca ua", "chatgpt_cca/0.144.0 (Linux; x86_64) rust", nil, Codex},
		{"openai client user agent", "curl/8.0", map[string]string{"X-OpenAI-Client-User-Agent": "codex_cli_rs/0.144.0"}, Codex},
		{"legacy openai codex cli ua", "openai-codex-cli/1.0", nil, Codex},
		{"codex fingerprint header", "python-requests/2.31", map[string]string{"X-Codex-Window-Id": "abc"}, Codex},
		{"codex installation header", "python-requests/2.31", map[string]string{"X-Codex-Installation-Id": "install-1"}, Codex},
		{"unknown codex header is not identity", "python-requests/2.31", map[string]string{"X-Codex-Fake": "abc"}, ""},
		{"empty codex fingerprint is not enough", "curl/8.0", map[string]string{"X-Codex-Window-Id": "  "}, ""},
		{"unrelated originator", "curl/8.0", map[string]string{"Originator": "other_client"}, ""},
		{"named product prefix is case sensitive", "curl/8.0", map[string]string{"Originator": "codex desktop"}, ""},
		{"app server daemon alone is not identity", "codex_app_server_daemon/1.2.3", nil, ""},
		{"codex backend alone is not identity", "codex-backend/1.2.3", nil, ""},
		{"app server daemon originator is not identity", "curl/8.0", map[string]string{"Originator": "codex_app_server_daemon"}, ""},
		{"codex backend originator is not identity", "curl/8.0", map[string]string{"Originator": "codex-backend"}, ""},
		{"bare fallback ua must be exact", "codex-cli-wrapper", nil, ""},
		{"embedded lookalike ua is not identity", "notcodex_cli_rs/0.144.0", nil, ""},
		{"embedded lookalike client ua is not identity", "curl/8.0", map[string]string{"X-OpenAI-Client-User-Agent": "notcodex_cli_rs/0.144.0"}, ""},
		{"wrapped client ua is not identity", "curl/8.0", map[string]string{"X-OpenAI-Client-User-Agent": "openai-codex-cli-wrapper"}, ""},
		{"underscored client ua is not identity", "curl/8.0", map[string]string{"X-OpenAI-Client-User-Agent": "codex_cli_wrapper"}, ""},
		{"unrelated openai client user agent", "curl/8.0", map[string]string{"X-OpenAI-Client-User-Agent": "openai-go/2.0"}, ""},
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

func TestClassifyRequestPreservesClaudePrecedence(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/codex/v1/responses", nil)
	req.Header.Set("User-Agent", "claude-cli/2.1.0")
	req.Header.Set("Originator", "codex_cli_rs")
	if got := ClassifyRequest(req); got != ClaudeCode {
		t.Fatalf("ClassifyRequest() = %q, want %q", got, ClaudeCode)
	}

	req.Header.Set("User-Agent", "codex_cli_rs/0.144.0")
	if got := ClassifyRequest(req); got != Codex {
		t.Fatalf("ClassifyRequest() = %q, want %q", got, Codex)
	}
}

func TestIsCodexRequestNil(t *testing.T) {
	if IsCodexRequest(nil) {
		t.Fatal("nil request must not be classified as Codex")
	}
	if got := Get(nil); got != "" {
		t.Fatalf("Get(nil) = %q, want empty", got)
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
