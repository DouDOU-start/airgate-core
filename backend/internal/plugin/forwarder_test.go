package plugin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/routing"
	sdk "github.com/DouDOU-start/airgate-sdk"
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

func TestParseBody_ReasoningEffort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "openai flat",
			body: `{"model":"gpt-5","reasoning_effort":"x-high"}`,
			want: "xhigh",
		},
		{
			name: "openai nested",
			body: `{"model":"gpt-5","reasoning":{"effort":"high"}}`,
			want: "high",
		},
		{
			name: "anthropic output effort",
			body: `{"model":"claude-opus-4-6","output_config":{"effort":"max"}}`,
			want: "max",
		},
		{
			name: "anthropic default",
			body: `{"model":"claude-opus-4-6","thinking":{"type":"enabled","budget_tokens":32768}}`,
			want: "high",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			parsed := parseBody([]byte(tt.body), "application/json")
			if parsed.ReasoningEffort != tt.want {
				t.Fatalf("ReasoningEffort = %q, want %q", parsed.ReasoningEffort, tt.want)
			}
		})
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

func TestSelectAllRoutesFailureResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		summary    allRoutesFailureSummary
		wantStatus int
		wantCode   string
	}{
		{
			name: "upstream rate limited",
			summary: allRoutesFailureSummary{
				rateLimitedSeen:       true,
				rateLimitedRetryAfter: 3 * time.Second,
				upstreamFailureSeen:   true,
			},
			wantStatus: http.StatusTooManyRequests,
			wantCode:   "all_routes_rate_limited",
		},
		{
			name: "local capacity exhausted",
			summary: allRoutesFailureSummary{
				localCapacitySeen:   true,
				upstreamFailureSeen: true,
			},
			wantStatus: http.StatusTooManyRequests,
			wantCode:   "all_routes_capacity_exhausted",
		},
		{
			name: "upstream timeout",
			summary: allRoutesFailureSummary{
				upstreamTimeoutSeen: true,
				upstreamFailureSeen: true,
			},
			wantStatus: http.StatusGatewayTimeout,
			wantCode:   "upstream_timeout",
		},
		{
			name: "upstream failure",
			summary: allRoutesFailureSummary{
				upstreamFailureSeen: true,
			},
			wantStatus: http.StatusBadGateway,
			wantCode:   "upstream_error",
		},
		{
			name: "no available account",
			summary: allRoutesFailureSummary{
				accountDeadSeen:    true,
				accountUnavailable: true,
			},
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "no_available_account",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := selectAllRoutesFailureResponse(tt.summary)
			if got.status != tt.wantStatus {
				t.Fatalf("status = %d, want %d", got.status, tt.wantStatus)
			}
			if got.code != tt.wantCode {
				t.Fatalf("code = %q, want %q", got.code, tt.wantCode)
			}
		})
	}
}

func TestAllRoutesFailureSummaryRecordsTimeout(t *testing.T) {
	t.Parallel()

	summary := allRoutesFailureSummary{}
	summary.recordExecution(forwardExecution{
		outcome: sdk.ForwardOutcome{Kind: sdk.OutcomeUpstreamTransient},
		err:     context.DeadlineExceeded,
	})

	if !summary.upstreamTimeoutSeen {
		t.Fatalf("upstreamTimeoutSeen = false, want true")
	}
	if summary.upstreamFailureSeen {
		t.Fatalf("upstreamFailureSeen = true, want false")
	}
}
