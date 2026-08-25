package server

import (
	"testing"

	"github.com/DouDOU-start/airgate-core/internal/config"
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

func TestConvertMarketEntries(t *testing.T) {
	if got := convertMarketEntries(nil); got != nil {
		t.Fatalf("空市场条目应返回 nil，得到 %#v", got)
	}

	got := convertMarketEntries([]config.MarketEntry{{
		Name:        "gateway-openai",
		Description: "OpenAI 网关",
		Author:      "AirGate",
		Type:        "gateway",
		GithubRepo:  "owner/repo",
	}})

	if len(got) != 1 {
		t.Fatalf("转换数量 = %d，期望 1", len(got))
	}
	if got[0].Name != "gateway-openai" || got[0].GithubRepo != "owner/repo" || got[0].Type != "gateway" {
		t.Fatalf("转换结果异常: %+v", got[0])
	}
}
