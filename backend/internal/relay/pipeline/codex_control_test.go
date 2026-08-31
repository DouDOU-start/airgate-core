package pipeline

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	providertransport "github.com/DouDOU-start/airgate-core/internal/relay/transport"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
)

func serveCodexControlNative(t *testing.T, p *Pipeline, path string, body []byte, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/*path", func(c *gin.Context) {
		c.Set(middleware.CtxKeyKeyInfo, &auth.APIKeyInfo{
			KeyID: 1, UserID: 2, GroupID: 7, UserBalance: 100,
		})
	}, p.HandleCodexControlPlane)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	// These helper requests model the official Codex backend client. Shared
	// aliases reject ordinary OpenAI callers before account selection.
	req.Header.Set("Originator", "codex_cli_rs")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, req)
	return response
}

func TestCodexRawControlSpecAllowlist(t *testing.T) {
	for _, spec := range codexRawControlSpecs {
		for _, prefix := range codexControlRoutePrefixes {
			got, ok := codexRawControlSpecForPath(prefix + spec.Path)
			if !ok || got.Endpoint != spec.Endpoint || got.Path != spec.Path {
				t.Fatalf("path %q did not resolve to %s/%s: %#v, %v", prefix+spec.Path, spec.Endpoint, spec.Path, got, ok)
			}
		}
	}
	for _, path := range []string{
		"/alpha/history/v2/list_windows/extra",
		"/alpha/notes/v2/read_file?path=secret",
		"/analytics-events/events/extra",
		"/unknown",
	} {
		if _, ok := codexRawControlSpecForPath(path); ok {
			t.Errorf("unexpectedly accepted non-allowlisted control path %q", path)
		}
	}
}

func TestCodexHistoryNotesControlSpecsRequireOAuth(t *testing.T) {
	for _, spec := range codexRawControlSpecs {
		switch spec.Endpoint {
		case adaptor.EndpointHistoryListWindows, adaptor.EndpointHistoryListItems,
			adaptor.EndpointHistoryReadItem, adaptor.EndpointHistorySearchContents,
			adaptor.EndpointNotesListFilesByPrefix, adaptor.EndpointNotesReadFile,
			adaptor.EndpointNotesSearchContents, adaptor.EndpointNotesAppendToFile,
			adaptor.EndpointNotesWriteFile, adaptor.EndpointNotesThreadHint:
			if !spec.OAuthOnly {
				t.Errorf("history/notes endpoint %q must be OAuth-only", spec.Endpoint)
			}
		}
	}
}

func TestHandleCodexControlPlaneForwardsRawHistoryAndSkipsCPA(t *testing.T) {
	body := []byte(`{"query":"needle","context":{"session_id":"client-value"},"unknown":{"nested":[1,2]}}`)
	native := &memoriesNativeTransport{result: providertransport.Result{
		StatusCode: http.StatusOK,
		Headers:    http.Header{"Content-Type": {"application/json"}, "X-Control": {"history"}},
		Body:       []byte(`{"ok":true}`),
	}}
	legacy := &memoriesCountingCPA{}
	p := newMemoriesNativePipeline(t, native, legacy)
	response := serveCodexControlNative(t, p, "/v1/alpha/history/v2/search_contents", body, "application/json; charset=utf-8")
	if response.Code != http.StatusOK || response.Body.String() != `{"ok":true}` {
		t.Fatalf("response = %d/%q", response.Code, response.Body.String())
	}
	if response.Header().Get("X-Control") != "history" {
		t.Fatalf("response headers = %#v", response.Header())
	}
	if legacy.callCount() != 0 {
		t.Fatalf("CPA calls = %d, want 0", legacy.callCount())
	}
	if calls, request := native.snapshot(); calls != 1 {
		t.Fatalf("native calls = %d, want 1", calls)
	} else {
		if request.Endpoint != adaptor.EndpointHistorySearchContents || request.Path != "/alpha/history/v2/search_contents" {
			t.Fatalf("native endpoint/path = %q/%q", request.Endpoint, request.Path)
		}
		if !bytes.Equal(request.Payload, body) {
			t.Fatalf("native body changed: got %q want %q", request.Payload, body)
		}
		if request.Headers.Get("Content-Type") != "application/json; charset=utf-8" {
			t.Fatalf("native content type = %q", request.Headers.Get("Content-Type"))
		}
		if request.Headers.Get("X-OpenAI-Encrypted-Tool-Arguments") != "true" {
			t.Fatalf("encrypted tool marker missing: %#v", request.Headers)
		}
	}
}

func TestHandleCodexControlPlaneForwardsAnalyticsWithoutModel(t *testing.T) {
	body := []byte(`{"events":[{"kind":"test","payload":{"ok":true}}]}`)
	native := &memoriesNativeTransport{result: providertransport.Result{
		StatusCode: http.StatusAccepted,
		Headers:    http.Header{"Content-Type": {"application/json"}},
		Body:       []byte(`{"accepted":true}`),
	}}
	legacy := &memoriesCountingCPA{}
	p := newMemoriesNativePipeline(t, native, legacy)
	response := serveCodexControlNative(t, p, "/codex/analytics-events/events", body, "application/json")
	if response.Code != http.StatusAccepted || response.Body.String() != `{"accepted":true}` {
		t.Fatalf("response = %d/%q", response.Code, response.Body.String())
	}
	if legacy.callCount() != 0 {
		t.Fatalf("CPA calls = %d, want 0", legacy.callCount())
	}
	if calls, request := native.snapshot(); calls != 1 {
		t.Fatalf("native calls = %d, want 1", calls)
	} else if request.Endpoint != adaptor.EndpointAnalyticsEvents || request.Path != "/analytics-events/events" {
		t.Fatalf("native endpoint/path = %q/%q", request.Endpoint, request.Path)
	}
}

func TestHandleCodexControlPlaneRejectsNonObjectJSON(t *testing.T) {
	native := &memoriesNativeTransport{}
	p := newMemoriesNativePipeline(t, native, &memoriesCountingCPA{})
	response := serveCodexControlNative(t, p, "/alpha/notes/v2/read_file", []byte(`[]`), "application/json")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", response.Code, response.Body.String())
	}
	if calls, _ := native.snapshot(); calls != 0 {
		t.Fatalf("native calls = %d, want 0", calls)
	}
}
