package pipeline

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
)

func TestCodexBackendEnvironmentRoutesUseExactShapesAndAliases(t *testing.T) {
	gin.SetMode(gin.TestMode)
	accepted := []struct {
		path     string
		endpoint string
		provider string
	}{
		{path: "/wham/environments", endpoint: adaptor.EndpointCodexEnvironments, provider: "/environments"},
		{path: "/api/codex/environments", endpoint: adaptor.EndpointCodexEnvironments, provider: "/environments"},
		{path: "/backend-api/wham/environments", endpoint: adaptor.EndpointCodexEnvironments, provider: "/environments"},
		{path: "/backend-api/v1/wham/environments", endpoint: adaptor.EndpointCodexEnvironments, provider: "/environments"},
		{path: "/api/codex/environments/by-repo/github/openai/codex", endpoint: adaptor.EndpointCodexEnvironmentsByRepo, provider: "/environments/by-repo/github/openai/codex"},
		{path: "/wham/environments/by-repo/github/open%2Fai/codex/%252Fmain", endpoint: adaptor.EndpointCodexEnvironmentsByRepo, provider: "/environments/by-repo/github/open%2Fai/codex/%252Fmain"},
	}
	for _, test := range accepted {
		t.Run(test.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, test.path, nil)
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = req
			route, providerPath, ok := codexBackendClientRouteForRequest(ctx)
			if !ok {
				t.Fatalf("route was rejected: URL=%#v path=%q raw=%q", req.URL, req.URL.Path, req.URL.RawPath)
			}
			if route.Endpoint != test.endpoint || route.Method != http.MethodGet {
				t.Fatalf("route = %#v, want endpoint=%q GET", route, test.endpoint)
			}
			if providerPath != test.provider || route.Path != test.provider {
				t.Fatalf("provider path=%q route path=%q, want %q", providerPath, route.Path, test.provider)
			}
		})
	}
}

func TestCodexBackendEnvironmentByRepoRejectsWrongSegmentShapes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{
		"/wham/environments/by-repo/github/openai",
		"/wham/environments/by-repo/github/openai/codex/ref/extra",
		"/wham/environments/by-repo/github/../codex",
		"/wham/environments/by-repo/github/open%5Cai/codex",
		"/wham/environments/by-repo/github/open%ZZ/codex",
		"/wham/environments/by-repo/github/%20/codex",
	} {
		t.Run(path, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, path, nil)
			if err != nil {
				req = &http.Request{Method: http.MethodGet, URL: &url.URL{Path: path, RawPath: path}}
			}
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = req
			if _, _, ok := codexBackendClientRouteForRequest(ctx); ok {
				t.Fatalf("unsafe or wrong-shape environment path accepted: URL=%#v", req.URL)
			}
		})
	}
}

func TestCodexBackendEnvironmentKnownPathReturns405Route(t *testing.T) {
	gin.SetMode(gin.TestMode)
	req := httptest.NewRequest(http.MethodPost, "/api/codex/environments", nil)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = req
	route, providerPath, ok := codexBackendClientRouteForRequest(ctx)
	if !ok || route.Endpoint != adaptor.EndpointCodexEnvironments || route.Method != http.MethodGet || providerPath != "/environments" {
		t.Fatalf("known wrong-method route = %#v, %q, %v", route, providerPath, ok)
	}
}
