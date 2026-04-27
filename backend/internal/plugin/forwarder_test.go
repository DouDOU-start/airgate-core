package plugin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/routing"
)

func TestParseBody(t *testing.T) {
	t.Parallel()

	body := []byte(`{"model":"gpt-4.1","stream":false,"metadata":{"user_id":"session-123"}}`)

	parsed := parseBody(body, "application/json")
	if parsed.Model != "gpt-4.1" {
		t.Fatalf("Model = %q, want %q", parsed.Model, "gpt-4.1")
	}
	if parsed.SessionID != "session-123" {
		t.Fatalf("SessionID = %q, want %q", parsed.SessionID, "session-123")
	}
	if parsed.Stream {
		t.Fatalf("Stream = true, want false")
	}
}

func TestParseBody_StreamTrue(t *testing.T) {
	t.Parallel()

	body := []byte(`{"model":"gpt-4.1","stream":true,"metadata":{"user_id":"sess-1"}}`)

	parsed := parseBody(body, "application/json")
	if parsed.Model != "gpt-4.1" {
		t.Fatalf("Model = %q, want %q", parsed.Model, "gpt-4.1")
	}
	if !parsed.Stream {
		t.Fatalf("Stream = false, want true")
	}
	if parsed.SessionID != "sess-1" {
		t.Fatalf("SessionID = %q, want %q", parsed.SessionID, "sess-1")
	}
}

func TestBuildPluginRequestUsesWriterForStreamRequest(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	state := &forwardState{
		requestPath: "/v1/images/generations",
		stream:      true,
		realtime:    true,
		keyInfo:     &auth.APIKeyInfo{},
		account:     &ent.Account{},
	}

	req := buildPluginRequest(c, state)
	if !req.Stream {
		t.Fatalf("Stream = false, want true")
	}
	if req.Writer == nil {
		t.Fatalf("Writer = nil, want stream writer")
	}
}

func TestBuildPluginRequestOmitsWriterForPlainNonStreamRequest(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	state := &forwardState{
		requestPath: "/v1/chat/completions",
		stream:      false,
		realtime:    false,
		keyInfo:     &auth.APIKeyInfo{},
		account:     &ent.Account{},
	}

	req := buildPluginRequest(c, state)
	if req.Writer != nil {
		t.Fatalf("Writer = %T, want nil", req.Writer)
	}
}

func TestBuildPluginRequestOmitsWriterForNonStreamImagesRequest(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	state := &forwardState{
		requestPath: "/v1/images/generations",
		stream:      false,
		realtime:    false,
		keyInfo:     &auth.APIKeyInfo{},
		account:     &ent.Account{},
	}

	req := buildPluginRequest(c, state)
	if req.Stream {
		t.Fatalf("Stream = true, want false")
	}
	if req.Writer != nil {
		t.Fatalf("Writer = %T, want nil", req.Writer)
	}
}

func TestRoutesForAPIKeyUsesBoundGroupOnly(t *testing.T) {
	t.Parallel()

	settings := map[string]map[string]string{"openai": {"image_enabled": "true"}}
	state := &forwardState{keyInfo: &auth.APIKeyInfo{
		GroupID:                42,
		GroupPlatform:          "openai",
		GroupRateMultiplier:    1.5,
		UserGroupRates:         map[int64]float64{42: 0.7, 99: 0.1},
		GroupPluginSettings:    settings,
		GroupServiceTier:       "priority",
		GroupForceInstructions: "stay concise",
	}}

	routes := routesForAPIKey(state, routing.Requirements{NeedsImage: true})
	if len(routes) != 1 {
		t.Fatalf("len(routes) = %d, want 1", len(routes))
	}
	route := routes[0]
	if route.GroupID != 42 {
		t.Fatalf("GroupID = %d, want 42", route.GroupID)
	}
	if route.EffectiveRate != 0.7 {
		t.Fatalf("EffectiveRate = %v, want 0.7", route.EffectiveRate)
	}
	if route.GroupPluginSettings["openai"]["image_enabled"] != "true" {
		t.Fatalf("image_enabled not preserved")
	}

	settings["openai"]["image_enabled"] = "false"
	if route.GroupPluginSettings["openai"]["image_enabled"] != "true" {
		t.Fatalf("route plugin settings should be cloned")
	}
}

func TestRoutesForAPIKeyRejectsImageWhenBoundGroupDisabled(t *testing.T) {
	t.Parallel()

	state := &forwardState{keyInfo: &auth.APIKeyInfo{
		GroupID:             42,
		GroupPlatform:       "openai",
		GroupPluginSettings: map[string]map[string]string{"openai": {"image_enabled": "false"}},
	}}

	routes := routesForAPIKey(state, routing.Requirements{NeedsImage: true})
	if len(routes) != 0 {
		t.Fatalf("len(routes) = %d, want 0", len(routes))
	}
}
