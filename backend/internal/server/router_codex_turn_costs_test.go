package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRegisterCodexControlPlaneRoutesIncludesTurnCosts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	handler := func(c *gin.Context) { c.Status(http.StatusNoContent) }
	registerCodexControlPlaneRoutes(engine.Group("/v1"), engine.Group(""), engine.Group("/codex"), handler)

	for _, path := range []string{
		"/v1/analytics/codex/turn-costs",
		"/analytics/codex/turn-costs",
		"/codex/analytics/codex/turn-costs",
		"/codex/v1/analytics/codex/turn-costs",
	} {
		resp := httptest.NewRecorder()
		engine.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, path, nil))
		if resp.Code != http.StatusNoContent {
			t.Errorf("POST %s status = %d, want 204", path, resp.Code)
		}
	}
}
