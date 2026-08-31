package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestCodexCuratedPluginsExportProxyForwardsOnlySafeHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/backend-api/plugins/export/curated" || r.URL.RawQuery != "" {
			t.Errorf("upstream request = %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		if got := r.Header.Get("Originator"); got != "codex_cli_rs/1.2" {
			t.Errorf("Originator = %q", got)
		}
		if got := r.Header.Get("User-Agent"); got != "codex_cli_rs/9.9" {
			t.Errorf("User-Agent = %q", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q", got)
		}
		for _, name := range []string{"Authorization", "Cookie", "X-Api-Key", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"} {
			if got := r.Header.Get(name); got != "" {
				t.Errorf("sensitive header %s leaked as %q", name, got)
			}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("ETag", "curated-etag")
		w.Header().Set("Set-Cookie", "must-not-forward=1")
		w.Header().Set("Connection", "close")
		_, _ = io.WriteString(w, `{"download_url":"https://storage.example.test/curated-plugins.zip?sig=signed"}`)
	}))
	defer upstream.Close()

	proxy := newCodexCuratedPluginsExportProxyForTest(upstream.Client(), upstream.URL+"/backend-api/plugins/export/curated")
	engine := gin.New()
	registerCodexCuratedPluginsExportRoute(engine, proxy.Handle)
	request := httptest.NewRequest(http.MethodGet, "/backend-api/plugins/export/curated", nil)
	request.Header.Set("Authorization", "Bearer caller-secret")
	request.Header.Set("Cookie", "session=caller-secret")
	request.Header.Set("X-Api-Key", "caller-secret")
	request.Header.Set("X-Forwarded-For", "198.51.100.7")
	request.Header.Set("X-Forwarded-Host", "attacker.example")
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("Originator", "codex_cli_rs/1.2")
	request.Header.Set("User-Agent", "codex_cli_rs/9.9")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if got := response.Body.String(); got != `{"download_url":"https://storage.example.test/curated-plugins.zip?sig=signed"}` {
		t.Fatalf("body = %q", got)
	}
	if response.Header().Get("ETag") != "curated-etag" {
		t.Fatalf("ETag was not preserved: %#v", response.Header())
	}
	if response.Header().Get("Set-Cookie") != "" || response.Header().Get("Connection") != "" {
		t.Fatalf("stateful response headers leaked: %#v", response.Header())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("default Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d, want 1", calls.Load())
	}
}

func TestCodexCuratedPluginsExportProxyRejectsAuthLikeInputsBeforeUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	defer upstream.Close()
	proxy := newCodexCuratedPluginsExportProxyForTest(upstream.Client(), upstream.URL+"/backend-api/plugins/export/curated")
	engine := gin.New()
	registerCodexCuratedPluginsExportRoute(engine, proxy.Handle)

	tests := []struct {
		name   string
		method string
		target string
		body   string
		status int
	}{
		{name: "post", method: http.MethodPost, target: "/backend-api/plugins/export/curated", status: http.StatusMethodNotAllowed},
		{name: "query", method: http.MethodGet, target: "/backend-api/plugins/export/curated?next=1", status: http.StatusBadRequest},
		{name: "body", method: http.MethodGet, target: "/backend-api/plugins/export/curated", body: "unexpected", status: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(tt.method, tt.target, strings.NewReader(tt.body))
			engine.ServeHTTP(response, request)
			if response.Code != tt.status {
				t.Fatalf("status = %d, body = %s; want %d", response.Code, response.Body.String(), tt.status)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid public inputs reached upstream %d times", calls.Load())
	}
}

func TestCodexCuratedPluginsExportProxyRejectsInvalidMetadataAndOversize(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name       string
		body       string
		maxBody    int64
		wantStatus int
	}{
		{name: "invalid json", body: "not-json", maxBody: 1 << 20, wantStatus: http.StatusBadGateway},
		{name: "missing url", body: `{"ok":true}`, maxBody: 1 << 20, wantStatus: http.StatusBadGateway},
		{name: "insecure url", body: `{"download_url":"http://storage.example.test/a.zip"}`, maxBody: 1 << 20, wantStatus: http.StatusBadGateway},
		{name: "oversize", body: `{"download_url":"https://storage.example.test/` + strings.Repeat("x", 128) + `"}`, maxBody: 32, wantStatus: http.StatusBadGateway},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tt.body)
			}))
			defer upstream.Close()
			proxy := newCodexCuratedPluginsExportProxyForTest(upstream.Client(), upstream.URL+"/backend-api/plugins/export/curated")
			proxy.maxBody = tt.maxBody
			engine := gin.New()
			registerCodexCuratedPluginsExportRoute(engine, proxy.Handle)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/backend-api/plugins/export/curated", nil))
			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, body = %s; want %d", response.Code, response.Body.String(), tt.wantStatus)
			}
		})
	}
}

func TestCodexCuratedPluginsExportURLValidationIsFixedAndHTTPSOnly(t *testing.T) {
	valid := []string{codexCuratedPluginsExportUpstreamURL, "https://chatgpt.com/backend-api/plugins/export/curated/"}
	for _, raw := range valid {
		if err := validateCodexCuratedPluginsUpstreamURL(raw, false); err != nil {
			t.Errorf("valid URL %q rejected: %v", raw, err)
		}
	}
	for _, raw := range []string{
		"http://chatgpt.com/backend-api/plugins/export/curated",
		"https://attacker.example/backend-api/plugins/export/curated",
		"https://chatgpt.com/backend-api/plugins/export/curated?url=https://attacker.example",
		"https://chatgpt.com/backend-api/plugins/export/other",
		"https://chatgpt.com/backend-api/plugins/export/curated#fragment",
	} {
		if err := validateCodexCuratedPluginsUpstreamURL(raw, false); err == nil {
			t.Errorf("unsafe URL %q was accepted", raw)
		}
	}
}
