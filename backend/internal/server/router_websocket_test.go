package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRegisterCodexResponsesWebSocketFallbackRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	relayGroup := engine.Group("/v1")
	noPrefixGroup := engine.Group("")
	codexGroup := engine.Group("/codex")
	handler := func(c *gin.Context) {
		c.Status(http.StatusUpgradeRequired)
	}

	registerCodexResponsesWebSocketFallbackRoutes(relayGroup, noPrefixGroup, codexGroup, handler)

	paths := []string{
		"/v1/responses",
		"/responses",
		"/codex/v1/responses",
		"/codex/responses",
	}
	for _, path := range paths {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		resp := httptest.NewRecorder()
		engine.ServeHTTP(resp, req)
		if resp.Code != http.StatusUpgradeRequired {
			t.Errorf("GET %s status = %d, want %d", path, resp.Code, http.StatusUpgradeRequired)
		}
	}
}

func TestRegisterCodexGuardianUnsupportedRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	relayGroup := engine.Group("/v1")
	noPrefixGroup := engine.Group("")
	codexGroup := engine.Group("/codex")
	handler := func(c *gin.Context) {
		c.JSON(http.StatusNotImplemented, gin.H{"error": gin.H{"code": "unsupported_endpoint"}})
	}

	registerCodexGuardianUnsupportedRoutes(relayGroup, noPrefixGroup, codexGroup, handler)

	paths := []string{
		"/v1/guardian",
		"/v1/guardian-classifier",
		"/guardian",
		"/guardian-classifier",
		"/codex/v1/guardian",
		"/codex/v1/guardian-classifier",
		"/codex/guardian",
		"/codex/guardian-classifier",
	}
	for _, path := range paths {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			req := httptest.NewRequest(method, path, nil)
			resp := httptest.NewRecorder()
			engine.ServeHTTP(resp, req)
			if resp.Code != http.StatusNotImplemented {
				t.Errorf("%s %s status = %d, want %d", method, path, resp.Code, http.StatusNotImplemented)
			}
			if got := resp.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
				t.Errorf("%s %s content type = %q, want JSON", method, path, got)
			}
		}
	}
}

func TestRegisterCodexGuardianNativeRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	relayGroup := engine.Group("/v1")
	noPrefixGroup := engine.Group("")
	codexGroup := engine.Group("/codex")
	guardian := func(c *gin.Context) { c.Status(http.StatusNoContent) }
	classifier := func(c *gin.Context) { c.Status(http.StatusAccepted) }
	unsupported := func(c *gin.Context) { c.Status(http.StatusNotImplemented) }
	registerCodexGuardianNativeRoutes(relayGroup, noPrefixGroup, codexGroup, guardian, classifier, unsupported)

	guardianPaths := []string{
		"/v1/guardian", "/guardian", "/codex/v1/guardian", "/codex/guardian",
	}
	classifierPaths := []string{
		"/v1/guardian-classifier", "/guardian-classifier", "/codex/v1/guardian-classifier", "/codex/guardian-classifier",
	}
	for _, path := range guardianPaths {
		resp := httptest.NewRecorder()
		engine.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, path, nil))
		if resp.Code != http.StatusNoContent {
			t.Errorf("POST %s status = %d, want %d", path, resp.Code, http.StatusNoContent)
		}
		resp = httptest.NewRecorder()
		engine.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, path, nil))
		if resp.Code != http.StatusNotImplemented {
			t.Errorf("GET %s status = %d, want %d", path, resp.Code, http.StatusNotImplemented)
		}
	}
	for _, path := range classifierPaths {
		resp := httptest.NewRecorder()
		engine.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, path, nil))
		if resp.Code != http.StatusAccepted {
			t.Errorf("POST %s status = %d, want %d", path, resp.Code, http.StatusAccepted)
		}
	}
}

func TestRegisterCodexGuardianNativeRoutesWebSocketHandlers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	relayGroup := engine.Group("/v1")
	noPrefixGroup := engine.Group("")
	codexGroup := engine.Group("/codex")
	guardian := func(c *gin.Context) { c.Status(http.StatusNoContent) }
	classifier := func(c *gin.Context) { c.Status(http.StatusAccepted) }
	unsupported := func(c *gin.Context) { c.Status(http.StatusNotImplemented) }
	guardianWS := func(c *gin.Context) { c.Status(http.StatusSwitchingProtocols) }
	classifierWS := func(c *gin.Context) { c.Status(http.StatusUpgradeRequired) }
	registerCodexGuardianNativeRoutes(relayGroup, noPrefixGroup, codexGroup, guardian, classifier, unsupported, guardianWS, classifierWS)

	for _, path := range []string{"/v1/guardian", "/guardian", "/codex/v1/guardian", "/codex/guardian"} {
		resp := httptest.NewRecorder()
		engine.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, path, nil))
		if resp.Code != http.StatusSwitchingProtocols {
			t.Errorf("GET %s status = %d, want %d", path, resp.Code, http.StatusSwitchingProtocols)
		}
	}
	for _, path := range []string{"/v1/guardian-classifier", "/guardian-classifier", "/codex/v1/guardian-classifier", "/codex/guardian-classifier"} {
		resp := httptest.NewRecorder()
		engine.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, path, nil))
		if resp.Code != http.StatusUpgradeRequired {
			t.Errorf("GET %s status = %d, want %d", path, resp.Code, http.StatusUpgradeRequired)
		}
	}
}

func TestRegisterCodexNativeHTTPRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	relayGroup := engine.Group("/v1")
	noPrefixGroup := engine.Group("")
	codexGroup := engine.Group("/codex")
	handler := func(c *gin.Context) { c.Status(http.StatusNoContent) }
	registerCodexNativeHTTPRoutes(relayGroup, noPrefixGroup, codexGroup, handler, handler, handler)

	for _, path := range []string{
		"/v1/realtime/calls", "/realtime/calls", "/codex/v1/realtime/calls", "/codex/realtime/calls",
		"/v1/live", "/live", "/codex/v1/live", "/codex/live",
		"/v1/memories/trace_summarize", "/memories/trace_summarize",
		"/codex/v1/memories/trace_summarize", "/codex/memories/trace_summarize",
	} {
		resp := httptest.NewRecorder()
		engine.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, path, nil))
		if resp.Code != http.StatusNoContent {
			t.Errorf("POST %s status = %d, want %d", path, resp.Code, http.StatusNoContent)
		}
	}
}

func TestRegisterCodexRealtimeSidebandRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	relayGroup := engine.Group("/v1")
	noPrefixGroup := engine.Group("")
	codexGroup := engine.Group("/codex")
	handler := func(c *gin.Context) { c.Status(http.StatusUpgradeRequired) }
	registerCodexRealtimeSidebandRoutes(relayGroup, noPrefixGroup, codexGroup, handler)

	for _, path := range []string{
		"/v1/realtime?call_id=rtc_test", "/realtime?call_id=rtc_test",
		"/codex/v1/realtime?call_id=rtc_test", "/codex/realtime?call_id=rtc_test",
		"/v1/live/rtc_test", "/live/rtc_test", "/codex/v1/live/rtc_test", "/codex/live/rtc_test",
	} {
		resp := httptest.NewRecorder()
		engine.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, path, nil))
		if resp.Code != http.StatusUpgradeRequired {
			t.Errorf("GET %s status = %d, want %d", path, resp.Code, http.StatusUpgradeRequired)
		}
	}
}

func TestRegisterCodexBackendAliasRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("/backend-api/codex")
	statusHandler := func(status int) gin.HandlerFunc {
		return func(c *gin.Context) { c.Status(status) }
	}
	registerCodexBackendAliasRoutes(group, codexBackendAliasHandlers{
		responses:                   statusHandler(201),
		responsesWebSocket:          statusHandler(202),
		compact:                     statusHandler(203),
		alphaSearch:                 statusHandler(204),
		imagesGenerations:           statusHandler(215),
		imagesEdits:                 statusHandler(216),
		models:                      statusHandler(205),
		realtimeCalls:               statusHandler(206),
		realtimeLive:                statusHandler(207),
		memories:                    statusHandler(208),
		realtimeSidebandWebSocket:   statusHandler(209),
		guardian:                    statusHandler(210),
		guardianClassifier:          statusHandler(211),
		guardianWebSocket:           statusHandler(212),
		guardianClassifierWebSocket: statusHandler(213),
	})

	tests := []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodPost, "/backend-api/codex/responses", 201},
		{http.MethodGet, "/backend-api/codex/responses", 202},
		{http.MethodPost, "/backend-api/codex/v1/responses", 201},
		{http.MethodGet, "/backend-api/codex/v1/responses", 202},
		{http.MethodPost, "/backend-api/codex/responses/compact", 203},
		{http.MethodPost, "/backend-api/codex/v1/responses/compact", 203},
		{http.MethodPost, "/backend-api/codex/alpha/search", 204},
		{http.MethodPost, "/backend-api/codex/v1/alpha/search", 204},
		{http.MethodPost, "/backend-api/codex/images/generations", 215},
		{http.MethodPost, "/backend-api/codex/v1/images/generations", 215},
		{http.MethodPost, "/backend-api/codex/images/edits", 216},
		{http.MethodPost, "/backend-api/codex/v1/images/edits", 216},
		{http.MethodGet, "/backend-api/codex/models", 205},
		{http.MethodGet, "/backend-api/codex/v1/models", 205},
		{http.MethodPost, "/backend-api/codex/realtime/calls", 206},
		{http.MethodPost, "/backend-api/codex/v1/realtime/calls", 206},
		{http.MethodPost, "/backend-api/codex/live", 207},
		{http.MethodPost, "/backend-api/codex/v1/live", 207},
		{http.MethodPost, "/backend-api/codex/memories/trace_summarize", 208},
		{http.MethodPost, "/backend-api/codex/v1/memories/trace_summarize", 208},
		{http.MethodGet, "/backend-api/codex/realtime?call_id=rtc_test", 209},
		{http.MethodGet, "/backend-api/codex/v1/realtime?call_id=rtc_test", 209},
		{http.MethodGet, "/backend-api/codex/live/rtc_test", 209},
		{http.MethodGet, "/backend-api/codex/v1/live/rtc_test", 209},
		{http.MethodPost, "/backend-api/codex/guardian", 210},
		{http.MethodPost, "/backend-api/codex/v1/guardian", 210},
		{http.MethodGet, "/backend-api/codex/guardian", 212},
		{http.MethodGet, "/backend-api/codex/v1/guardian", 212},
		{http.MethodPost, "/backend-api/codex/guardian-classifier", 211},
		{http.MethodPost, "/backend-api/codex/v1/guardian-classifier", 211},
		{http.MethodGet, "/backend-api/codex/guardian-classifier", 213},
		{http.MethodGet, "/backend-api/codex/v1/guardian-classifier", 213},
	}
	for _, tt := range tests {
		resp := httptest.NewRecorder()
		engine.ServeHTTP(resp, httptest.NewRequest(tt.method, tt.path, nil))
		if resp.Code != tt.want {
			t.Errorf("%s %s status = %d, want %d", tt.method, tt.path, resp.Code, tt.want)
		}
	}
}

func TestRegisterCodexBackendAliasRoutesSupportsPublicAPICodexBase(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("/api/codex")
	statusHandler := func(status int) gin.HandlerFunc {
		return func(c *gin.Context) { c.Status(status) }
	}
	registerCodexBackendAliasRoutes(group, codexBackendAliasHandlers{
		responses:          statusHandler(201),
		responsesWebSocket: statusHandler(202),
		compact:            statusHandler(203),
		alphaSearch:        statusHandler(204),
		imagesGenerations:  statusHandler(205),
		imagesEdits:        statusHandler(206),
		models:             statusHandler(207),
		filesCreate:        statusHandler(208),
		filesFinalize:      statusHandler(209),
	})

	tests := []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodPost, "/api/codex/responses", 201},
		{http.MethodGet, "/api/codex/responses", 202},
		{http.MethodPost, "/api/codex/v1/responses", 201},
		{http.MethodPost, "/api/codex/responses/compact", 203},
		{http.MethodPost, "/api/codex/alpha/search", 204},
		{http.MethodPost, "/api/codex/images/generations", 205},
		{http.MethodPost, "/api/codex/v1/images/edits", 206},
		{http.MethodGet, "/api/codex/models", 207},
		{http.MethodPost, "/api/codex/files", 208},
		{http.MethodPost, "/api/codex/v1/files/file-1/uploaded", 209},
	}
	for _, tt := range tests {
		resp := httptest.NewRecorder()
		engine.ServeHTTP(resp, httptest.NewRequest(tt.method, tt.path, nil))
		if resp.Code != tt.want {
			t.Errorf("%s %s status = %d, want %d", tt.method, tt.path, resp.Code, tt.want)
		}
	}
}
