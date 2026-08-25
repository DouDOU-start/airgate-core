package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/plugin/hookv2"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"
)

type fakeRelayHookV2Client struct {
	handle func(context.Context, hookv2.Request) (hookv2.Response, error)
	calls  atomic.Int32
}

func (f *fakeRelayHookV2Client) Info() hookv2.PluginInfo { return hookv2.PluginInfo{} }
func (f *fakeRelayHookV2Client) Init(context.Context, map[string]string) error {
	return nil
}
func (f *fakeRelayHookV2Client) Start(context.Context) error { return nil }
func (f *fakeRelayHookV2Client) Stop(context.Context) error  { return nil }
func (f *fakeRelayHookV2Client) Handle(ctx context.Context, request hookv2.Request) (hookv2.Response, error) {
	f.calls.Add(1)
	if f.handle == nil {
		return hookv2.Response{StatusCode: http.StatusNoContent}, nil
	}
	return f.handle(ctx, request)
}

func TestRelayHookV2AppliesSafeBodyMutationAndIgnoresRouting(t *testing.T) {
	replacement := []byte(`{"model":"gpt-5.4","stream":false,"metadata":{"user_id":"session-new"},"reasoning":{"effort":"high"},"tools":[{"type":"image_generation"}],"tool_choice":"required","input":"rewritten"}`)
	client := &fakeRelayHookV2Client{handle: func(_ context.Context, request hookv2.Request) (hookv2.Response, error) {
		if request.Method != http.MethodPost || request.Path != relayHookV2BeforeDispatchPath {
			t.Fatalf("hook request = %s %s", request.Method, request.Path)
		}
		var payload relayHookV2Request
		if err := json.Unmarshal(request.Body, &payload); err != nil {
			t.Fatalf("decode hook request: %v", err)
		}
		if payload.RequestID != "req-123" || payload.UserID != 11 || payload.APIKeyID != 22 || payload.GroupID != 33 {
			t.Fatalf("hook identity = %+v", payload)
		}
		if payload.Endpoint != "responses" || payload.Protocol != "openai" || payload.Client != "codex-cli/test" {
			t.Fatalf("hook client fields = %+v", payload)
		}
		return relayHookV2Response(t, map[string]any{
			"version":      relayHookV2Version,
			"request_body": json.RawMessage(replacement),
			"route": map[string]any{
				"account_ids":                        []int{999},
				"allow_rate_limited_account_ids":     []int{999},
				"fallback":                           "plugin",
				"unrecognized_future_routing_option": true,
			},
		}), nil
	}}
	forwarder, c, state := newRelayHookV2TestHarness(t, []relayHookV2TestPlugin{{name: "safe", priority: 10, client: client}})

	forwarder.applyRelayHookV2(c, state)

	if !bytes.Equal(state.body, replacement) {
		t.Fatalf("body = %s, want %s", state.body, replacement)
	}
	if state.model != "gpt-5.4" || state.stream {
		t.Fatalf("model/stream changed: model=%q stream=%v", state.model, state.stream)
	}
	if state.sessionID != "session-new" || state.reasoningEffort != "high" {
		t.Fatalf("derived fields: session=%q effort=%q", state.sessionID, state.reasoningEffort)
	}
	if state.accountReq.Workload != scheduler.WorkloadImage || !state.imageToolPayloadValid {
		t.Fatalf("derived image requirements not refreshed: req=%+v cache=%v", state.accountReq, state.imageToolPayloadValid)
	}
	if state.account != nil || state.selectedRoute.GroupID != 0 {
		t.Fatalf("routing decision leaked into state: account=%v route=%+v", state.account, state.selectedRoute)
	}
}

func TestRelayHookV2RejectsModelOrStreamMutationFailOpen(t *testing.T) {
	tests := []struct {
		name        string
		replacement json.RawMessage
	}{
		{name: "model", replacement: json.RawMessage(`{"model":"other","stream":false,"input":"changed"}`)},
		{name: "stream", replacement: json.RawMessage(`{"model":"gpt-5.4","stream":true,"input":"changed"}`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeRelayHookV2Client{handle: func(context.Context, hookv2.Request) (hookv2.Response, error) {
				return relayHookV2Response(t, relayHookV2Decision{Version: relayHookV2Version, RequestBody: tt.replacement}), nil
			}}
			forwarder, c, state := newRelayHookV2TestHarness(t, []relayHookV2TestPlugin{{name: tt.name, client: client}})
			original := bytes.Clone(state.body)

			forwarder.applyRelayHookV2(c, state)

			if !bytes.Equal(state.body, original) {
				t.Fatalf("invalid mutation changed body: %s", state.body)
			}
		})
	}
}

func TestRelayHookV2ErrorsPanicsAndTimeoutsFailOpen(t *testing.T) {
	tests := []struct {
		name    string
		timeout time.Duration
		handle  func(context.Context, hookv2.Request) (hookv2.Response, error)
	}{
		{name: "rpc error", handle: func(context.Context, hookv2.Request) (hookv2.Response, error) {
			return hookv2.Response{}, errors.New("plugin unavailable")
		}},
		{name: "panic", handle: func(context.Context, hookv2.Request) (hookv2.Response, error) {
			panic("boom")
		}},
		{name: "timeout", timeout: 5 * time.Millisecond, handle: func(ctx context.Context, _ hookv2.Request) (hookv2.Response, error) {
			<-ctx.Done()
			return hookv2.Response{}, ctx.Err()
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeRelayHookV2Client{handle: tt.handle}
			forwarder, c, state := newRelayHookV2TestHarness(t, []relayHookV2TestPlugin{{name: tt.name, client: client}})
			if tt.timeout > 0 {
				forwarder.manager.relayHookV2Timeout = tt.timeout
			}
			original := bytes.Clone(state.body)

			forwarder.applyRelayHookV2(c, state)

			if !bytes.Equal(state.body, original) {
				t.Fatalf("failed hook changed body: %s", state.body)
			}
			if client.calls.Load() != 1 {
				t.Fatalf("calls = %d, want 1", client.calls.Load())
			}
		})
	}
}

func TestRelayHookV2ChainKeepsEarlierMutationWhenLaterHookFails(t *testing.T) {
	firstBody := json.RawMessage(`{"model":"gpt-5.4","stream":false,"metadata":{"user_id":"first"},"input":"first"}`)
	first := &fakeRelayHookV2Client{handle: func(context.Context, hookv2.Request) (hookv2.Response, error) {
		return relayHookV2Response(t, relayHookV2Decision{Version: relayHookV2Version, RequestBody: firstBody}), nil
	}}
	second := &fakeRelayHookV2Client{handle: func(context.Context, hookv2.Request) (hookv2.Response, error) {
		return hookv2.Response{}, errors.New("second failed")
	}}
	forwarder, c, state := newRelayHookV2TestHarness(t, []relayHookV2TestPlugin{
		{name: "second", priority: 20, client: second},
		{name: "first", priority: 10, client: first},
	})

	forwarder.applyRelayHookV2(c, state)

	if !bytes.Equal(state.body, firstBody) {
		t.Fatalf("body = %s, want first successful mutation", state.body)
	}
	if state.sessionID != "first" {
		t.Fatalf("sessionID = %q, want first", state.sessionID)
	}
}

func TestNormalizeRelayHookV2BodyRejectsOversizedReplacement(t *testing.T) {
	prefix := []byte(`{"model":"gpt-5.4","stream":false,"padding":"`)
	replacement := make([]byte, 0, maxRelayHookV2BodyBytes+1)
	replacement = append(replacement, prefix...)
	replacement = append(replacement, bytes.Repeat([]byte{'x'}, maxRelayHookV2BodyBytes-len(prefix))...)
	replacement = append(replacement, '"', '}')
	state := &forwardState{model: "gpt-5.4"}

	if _, changed, err := normalizeRelayHookV2Body(state, relayHookV2Decision{
		Version:     relayHookV2Version,
		RequestBody: replacement,
	}); err == nil || changed {
		t.Fatalf("oversized replacement: changed=%v err=%v", changed, err)
	}
}

func TestNormalizeRelayHookV2BodyRejectsInvalidVersionWithoutBody(t *testing.T) {
	state := &forwardState{model: "gpt-5.4"}

	if _, changed, err := normalizeRelayHookV2Body(state, relayHookV2Decision{
		Version: "unsupported",
	}); err == nil || changed || relayHookV2ErrorCode(err) != "invalid_version" {
		t.Fatalf("invalid version without body: changed=%v err=%v", changed, err)
	}
}

func TestRelayHookV2CircuitBreakerAllowsSingleHalfOpenProbe(t *testing.T) {
	client := &fakeRelayHookV2Client{handle: func(context.Context, hookv2.Request) (hookv2.Response, error) {
		return hookv2.Response{}, errors.New("down")
	}}
	hook := newRelayHookV2Plugin("circuit", client)
	for range relayHookV2FailureLimit {
		_, called, err := hook.invoke(context.Background(), hookv2.Request{})
		if !called || err == nil {
			t.Fatalf("failure call: called=%v err=%v", called, err)
		}
		hook.recordFailure(time.Now())
	}
	if _, called, _ := hook.invoke(context.Background(), hookv2.Request{}); called {
		t.Fatal("open circuit unexpectedly called plugin")
	}

	hook.mu.Lock()
	hook.circuitUntil = time.Now().Add(-time.Millisecond)
	hook.mu.Unlock()
	_, called, err := hook.invoke(context.Background(), hookv2.Request{})
	if !called || err == nil {
		t.Fatalf("half-open probe: called=%v err=%v", called, err)
	}
	if _, secondCalled, _ := hook.invoke(context.Background(), hookv2.Request{}); secondCalled {
		t.Fatal("second half-open probe unexpectedly called plugin")
	}
	hook.recordFailure(time.Now())
}

type relayHookV2TestPlugin struct {
	name     string
	priority int32
	client   *fakeRelayHookV2Client
}

func newRelayHookV2TestHarness(t *testing.T, plugins []relayHookV2TestPlugin) (*Forwarder, *gin.Context, *forwardState) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	manager := NewManager(t.TempDir(), "debug", "", nil)
	for _, item := range plugins {
		name := item.name
		if name == "" {
			name = "test-hook"
		}
		manager.instances[name] = &PluginInstance{
			Name:         name,
			Priority:     item.priority,
			Capabilities: []string{hookv2.CapabilityRelayHookV1},
			RelayHookV2:  newRelayHookV2Plugin(name, item.client),
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(`{"model":"gpt-5.4","stream":false,"metadata":{"user_id":"session-old"},"reasoning_effort":"low","input":"original"}`))
	request.Header.Set("User-Agent", "codex-cli/test")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = request
	c.Set("request_id", "req-123")
	body := []byte(`{"model":"gpt-5.4","stream":false,"metadata":{"user_id":"session-old"},"reasoning_effort":"low","input":"original"}`)
	parsed := parseBody(body, "application/json")
	state := &forwardState{
		requestPath:           "/v1/responses",
		body:                  body,
		model:                 parsed.Model,
		schedulingModels:      []string{parsed.Model},
		schedulingModel:       parsed.Model,
		stream:                parsed.Stream,
		realtime:              parsed.Stream,
		sessionID:             parsed.SessionID,
		reasoningEffort:       parsed.ReasoningEffort,
		accountReq:            accountRequirementsForRequestCached(manager, "/v1/responses", parsed.Model, &parsed),
		imageToolPayloadValid: parsed.imageToolPayloadValid,
		imageToolPayload:      parsed.imageToolPayload,
		requestedPlatform:     "openai",
		keyInfo: &auth.APIKeyInfo{
			KeyID:         22,
			UserID:        11,
			GroupID:       33,
			GroupPlatform: "openai",
		},
		plugin: &PluginInstance{Name: "gateway-openai"},
	}
	return &Forwarder{manager: manager}, c, state
}

func relayHookV2Response(t *testing.T, value any) hookv2.Response {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal hook response: %v", err)
	}
	return hookv2.Response{StatusCode: http.StatusOK, Body: body}
}
