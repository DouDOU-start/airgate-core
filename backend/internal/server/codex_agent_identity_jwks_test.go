package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

type codexAgentIdentityJWKSRoundTripFunc func(*http.Request) (*http.Response, error)

func (f codexAgentIdentityJWKSRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestCodexAgentIdentityJWKSRejectsUnknownOrEncodedRequestBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstreamCalls := 0
	client := &http.Client{Transport: codexAgentIdentityJWKSRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		upstreamCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"keys":[]}`)),
			Request:    r,
		}, nil
	})}
	proxy := newCodexAgentIdentityJWKSProxyForTest(client, "http://127.0.0.1/agent-identities/jwks")
	engine := gin.New()
	engine.GET("/agent-identities/jwks", proxy.Handle)

	cases := []struct {
		name             string
		contentLength    int64
		transferEncoding []string
	}{
		{name: "declared body", contentLength: 1},
		{name: "unknown length", contentLength: -1},
		{name: "chunked body", contentLength: 0, transferEncoding: []string{"chunked"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/agent-identities/jwks", strings.NewReader("payload"))
			req.ContentLength = tc.contentLength
			req.TransferEncoding = tc.transferEncoding
			resp := httptest.NewRecorder()
			engine.ServeHTTP(resp, req)
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body = %s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if !strings.Contains(resp.Body.String(), "unexpected_body") {
				t.Fatalf("body = %q, want unexpected_body", resp.Body.String())
			}
		})
	}
	if upstreamCalls != 0 {
		t.Fatalf("upstream calls = %d, want 0 for rejected request bodies", upstreamCalls)
	}
}
