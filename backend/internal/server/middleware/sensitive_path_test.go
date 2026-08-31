package middleware

import "testing"

func TestRedactSensitiveRequestPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{path: "/_airgate/codex/files/upload/opaque-secret", want: "/_airgate/codex/files/upload/[REDACTED]"},
		{path: "/_airgate/codex/plugins/upload/opaque-secret", want: "/_airgate/codex/plugins/upload/[REDACTED]"},
		{path: "/_airgate/codex/files/upload/opaque-secret/more", want: "/_airgate/codex/files/upload/[REDACTED]"},
		{path: "/_airgate/codex/files/upload/", want: "/_airgate/codex/files/upload/"},
		{path: "/v1/files", want: "/v1/files"},
	}
	for _, tt := range tests {
		if got := RedactSensitiveRequestPath(tt.path); got != tt.want {
			t.Errorf("RedactSensitiveRequestPath(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}
