package pipeline

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestCodexOnlyHandlersRejectOrdinarySharedAliases(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name    string
		path    string
		method  string
		body    string
		handler gin.HandlerFunc
	}{
		{
			name: "backend client on v1",
			path: "/v1/usage", method: http.MethodGet,
			handler: (&Pipeline{}).HandleCodexBackendClient,
		},
		{
			name: "backend client on wham",
			path: "/wham/tasks", method: http.MethodPost, body: `{}`,
			handler: (&Pipeline{}).HandleCodexBackendClient,
		},
		{
			name: "backend client on backend api",
			path: "/backend-api/v1/wham/usage", method: http.MethodGet,
			handler: (&Pipeline{}).HandleCodexBackendClient,
		},
		{
			name: "control plane on backend api",
			path: "/backend-api/alpha/history/v2/list_windows", method: http.MethodPost, body: `{}`,
			handler: (&Pipeline{}).HandleCodexControlPlane,
		},
		{
			name: "files on v1",
			path: "/v1/files", method: http.MethodPost,
			body:    `{"file_name":"a.txt","file_size":1,"use_case":"codex"}`,
			handler: (&Pipeline{}).HandleCodexFilesCreate,
		},
		{
			name: "memories on v1",
			path: "/v1/memories/trace_summarize", method: http.MethodPost, body: `{}`,
			handler: (&Pipeline{}).HandleMemoriesTraceSummarize,
		},
		{
			name: "compact on v1",
			path: "/v1/responses/compact", method: http.MethodPost, body: `{"model":"gpt-5-codex"}`,
			handler: (&Pipeline{}).HandleCompact,
		},
		{
			name: "alpha search on v1",
			path: "/v1/alpha/search", method: http.MethodPost, body: `{"model":"gpt-5-codex","query":"hello"}`,
			handler: (&Pipeline{}).HandleAlphaSearch,
		},
		{
			name: "guardian on v1",
			path: "/v1/guardian", method: http.MethodPost, body: `{}`,
			handler: (&Pipeline{}).HandleCodexGuardian,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := strings.NewReader(tt.body)
			req := httptest.NewRequest(tt.method, tt.path, body)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = req
			tt.handler(c)

			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d, body = %s; want 404", w.Code, w.Body.String())
			}
			var envelope struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("response is not JSON: %v; body = %s", err, w.Body.String())
			}
			if envelope.Error.Code != "unsupported_endpoint" {
				t.Fatalf("error code = %q, want unsupported_endpoint; body = %s", envelope.Error.Code, w.Body.String())
			}
		})
	}
}

func TestCodexOnlyHandlerAcceptsDedicatedCodexAliasBeforeAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	req := httptest.NewRequest(http.MethodGet, "/codex/usage", nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	(&Pipeline{}).HandleCodexBackendClient(c)
	if w.Code == http.StatusNotFound {
		t.Fatalf("dedicated Codex alias was rejected as ordinary: %s", w.Body.String())
	}
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("dedicated alias status = %d, body = %s; want auth failure", w.Code, w.Body.String())
	}
}

func TestCodexResponsesWebSocketRejectsOrdinarySharedAlias(t *testing.T) {
	gin.SetMode(gin.TestMode)
	req := httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req

	(&Pipeline{}).HandleResponsesWebSocket(c)
	if w.Code != http.StatusUpgradeRequired {
		t.Fatalf("status = %d, body = %s; want 426", w.Code, w.Body.String())
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("response is not JSON: %v; body = %s", err, w.Body.String())
	}
	if envelope.Error.Code != "websocket_not_available" {
		t.Fatalf("error code = %q, want websocket_not_available; body = %s", envelope.Error.Code, w.Body.String())
	}
}
