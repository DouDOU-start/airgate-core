package pipeline

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestWriteUpstreamBodyCopiesSafeResponseHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Header("X-Middleware", "preserved")
	c.Header("Connection", "close")
	c.Header("Content-Length", "stale")

	result := attemptResult{
		statusCode:  201,
		contentType: "application/json",
		body:        []byte(`{"id":"resp_123"}`),
		headers: http.Header{
			"Content-Type":        {"application/json; charset=utf-8"},
			"ETag":                {`"abc"`},
			"X-Models-Etag":       {`"models-1"`},
			"X-Codex-Turn-State":  {"turn-state"},
			"Retry-After":         {"3"},
			"X-Request-Id":        {"upstream-req"},
			"Content-Encoding":    {"zstd"},
			"Connection":          {"keep-alive, X-Connection-Only"},
			"X-Connection-Only":   {"must-not-leak"},
			"Keep-Alive":          {"timeout=5"},
			"Proxy-Authenticate":  {"Basic realm=proxy"},
			"Proxy-Authorization": {"secret"},
			"TE":                  {"trailers"},
			"Trailer":             {"X-Trailer"},
			"Transfer-Encoding":   {"chunked"},
			"Upgrade":             {"h2c"},
			"Content-Length":      {"999"},
			"Set-Cookie":          {"session=secret"},
		},
	}

	writeUpstreamBody(c, result)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusCreated)
	}
	for _, name := range []string{
		"ETag", "X-Models-Etag", "X-Codex-Turn-State", "Retry-After", "X-Request-Id", "Content-Encoding",
	} {
		if got := w.Header().Get(name); got == "" {
			t.Errorf("header %s was not copied", name)
		}
	}
	if got := w.Header().Get("X-Middleware"); got != "preserved" {
		t.Errorf("middleware header = %q, want preserved", got)
	}
	for _, name := range []string{
		"Connection", "X-Connection-Only", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
		"TE", "Trailer", "Transfer-Encoding", "Upgrade", "Content-Length",
		"Set-Cookie",
	} {
		if got := w.Header().Get(name); got != "" {
			t.Errorf("hop-by-hop header %s leaked as %q", name, got)
		}
	}
	if got := w.Body.String(); got != `{"id":"resp_123"}` {
		t.Fatalf("body = %q", got)
	}
}

func TestCopySafeUpstreamResponseHeadersNilSafe(t *testing.T) {
	dst := make(http.Header)
	copySafeUpstreamResponseHeaders(dst, nil)
	copySafeUpstreamResponseHeaders(nil, http.Header{"ETag": {"x"}})
	if len(dst) != 0 {
		t.Fatalf("nil source copied headers: %#v", dst)
	}
}

func TestWritePassthroughUpstreamBodyPreservesRawResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name            string
		method          string
		result          attemptResult
		wantStatus      int
		wantType        string
		wantBody        string
		wantTypePresent bool
	}{
		{
			name:   "body without content type",
			method: http.MethodPost,
			result: attemptResult{
				statusCode: http.StatusOK,
				body:       []byte{0x00, 0x01, 0xfe, 0xff},
			},
			wantStatus: http.StatusOK,
			wantBody:   string([]byte{0x00, 0x01, 0xfe, 0xff}),
		},
		{
			name:   "SDP content type and body",
			method: http.MethodPost,
			result: attemptResult{
				statusCode:  http.StatusCreated,
				contentType: "application/sdp",
				headers: http.Header{
					"Content-Type": {"application/json"},
					"Location":     {"/v1/realtime/calls/call_123"},
				},
				body: []byte("v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\n"),
			},
			wantStatus:      http.StatusCreated,
			wantType:        "application/sdp",
			wantBody:        "v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\n",
			wantTypePresent: true,
		},
		{
			name:   "204 has no invented metadata or body",
			method: http.MethodPost,
			result: attemptResult{
				statusCode: http.StatusNoContent,
				body:       []byte("must not be written"),
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name:   "HEAD has no response body",
			method: http.MethodHead,
			result: attemptResult{
				statusCode:  http.StatusOK,
				contentType: "application/sdp",
				body:        []byte("must not be written"),
			},
			wantStatus:      http.StatusOK,
			wantType:        "application/sdp",
			wantTypePresent: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(test.method, "/v1/realtime/calls", nil)

			writePassthroughUpstreamBody(c, test.result)

			if w.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", w.Code, test.wantStatus)
			}
			if got := w.Header().Get("Content-Type"); got != test.wantType {
				t.Fatalf("Content-Type = %q, want %q", got, test.wantType)
			}
			hasType := len(w.Result().Header.Values("Content-Type")) > 0
			if hasType != test.wantTypePresent {
				t.Fatalf("Content-Type presence = %v, want %v", hasType, test.wantTypePresent)
			}
			if got := w.Body.String(); got != test.wantBody {
				t.Fatalf("body = %q, want %q", got, test.wantBody)
			}
			if test.name == "SDP content type and body" && w.Header().Get("Location") == "" {
				t.Fatal("Location response header was not preserved")
			}
		})
	}
}
