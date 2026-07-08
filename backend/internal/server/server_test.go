package server

import (
	"testing"
)

func TestContentTypeFromExt(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"index.html", "text/html; charset=utf-8"},
		{"style.css", "text/css; charset=utf-8"},
		{"app.js", "application/javascript; charset=utf-8"},
		{"app.mjs", "application/javascript; charset=utf-8"},
		{"data.json", "application/json"},
		{"logo.svg", "image/svg+xml"},
		{"image.png", "image/png"},
		{"font.woff2", "font/woff2"},
		{"file.bin", "application/octet-stream"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := contentTypeFromExt(tt.name); got != tt.want {
				t.Fatalf("Content-Type = %q，期望 %q", got, tt.want)
			}
		})
	}
}
