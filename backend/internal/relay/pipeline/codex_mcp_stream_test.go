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

func TestCodexBackendClientMCPStreamNegotiation(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		accept   string
		endpoint string
		want     bool
	}{
		{name: "GET is an SSE subscription", method: http.MethodGet, accept: "application/json", endpoint: adaptor.EndpointCodexPSMCP, want: true},
		{name: "POST negotiates SSE", method: http.MethodPost, accept: "application/json, text/event-stream", endpoint: adaptor.EndpointCodexPSMCP, want: true},
		{name: "POST JSON remains unary", method: http.MethodPost, accept: "application/json", endpoint: adaptor.EndpointCodexPSMCP, want: false},
		{name: "DELETE may negotiate SSE", method: http.MethodDelete, accept: "text/event-stream", endpoint: adaptor.EndpointCodexPSMCP, want: true},
		{name: "other endpoint is unary", method: http.MethodGet, accept: "text/event-stream", endpoint: "codex_usage", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(tt.method, "/backend-api/ps/mcp", nil)
			request.Header.Set("Accept", tt.accept)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = request
			if got := codexBackendClientShouldStream(c, tt.endpoint); got != tt.want {
				t.Fatalf("stream = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHandleCodexBackendClientMCPPropagatesStreamFlag(t *testing.T) {
	native := &memoriesNativeTransport{result: providertransport.Result{
		StatusCode: http.StatusOK,
		Headers:    http.Header{"Content-Type": {"application/json"}},
		Body:       []byte(`{"jsonrpc":"2.0","result":{}}`),
	}}
	p := newMemoriesNativePipeline(t, native, &memoriesCountingCPA{})
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/backend-api/ps/mcp", func(c *gin.Context) {
		c.Set(middleware.CtxKeyKeyInfo, &auth.APIKeyInfo{KeyID: 1, UserID: 2, GroupID: 7, UserBalance: 100})
	}, p.HandleCodexBackendClient)
	engine.GET("/backend-api/ps/mcp", func(c *gin.Context) {
		c.Set(middleware.CtxKeyKeyInfo, &auth.APIKeyInfo{KeyID: 1, UserID: 2, GroupID: 7, UserBalance: 100})
	}, p.HandleCodexBackendClient)

	for _, tt := range []struct {
		name   string
		method string
		accept string
		want   bool
	}{
		{name: "get", method: http.MethodGet, accept: "application/json", want: true},
		{name: "post-sse", method: http.MethodPost, accept: "application/json, text/event-stream", want: true},
		{name: "post-json", method: http.MethodPost, accept: "application/json", want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := bytes.NewBufferString(`{"jsonrpc":"2.0","method":"initialize"}`)
			request := httptest.NewRequest(tt.method, "/backend-api/ps/mcp", body)
			request.Header.Set("Accept", tt.accept)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Originator", "codex_cli_rs")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			_, providerRequest := native.snapshot()
			if providerRequest.Endpoint != adaptor.EndpointCodexPSMCP || providerRequest.Stream != tt.want {
				t.Fatalf("provider request = endpoint:%q stream:%v, want endpoint:%q stream:%v", providerRequest.Endpoint, providerRequest.Stream, adaptor.EndpointCodexPSMCP, tt.want)
			}
		})
	}
}
