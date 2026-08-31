package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
	"github.com/DouDOU-start/airgate-core/internal/relay/relayhook"
)

type capturingAttemptTransformer struct {
	calls  int
	mutate bool
	seen   relayhook.ProviderAttemptRequest
}

func (*capturingAttemptTransformer) BeforeDispatch(context.Context, relayhook.Request) (relayhook.Decision, error) {
	return relayhook.Decision{}, nil
}

func (h *capturingAttemptTransformer) TransformProviderAttempt(_ context.Context, request relayhook.ProviderAttemptRequest) (relayhook.ProviderAttemptDecision, error) {
	h.calls++
	h.seen = request
	if h.mutate && len(request.Body) > 0 {
		request.Body[0] = 'x'
	}
	return relayhook.ProviderAttemptDecision{
		Version:     relayhook.VersionV1,
		RequestBody: json.RawMessage(`{"model":"gpt-test","stream":false,"input":"base","overage":true}`),
	}, nil
}

func codexAttemptContext(t *testing.T, codexClient bool) *gin.Context {
	t.Helper()
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	if codexClient {
		context.Request.Header.Set("Originator", "codex_cli_rs")
	}
	return context
}

func TestTransformSelectedCodexAttemptUsesAttemptLocalBodyCopy(t *testing.T) {
	base := []byte(`{"model":"gpt-test","stream":false,"input":"base"}`)
	wantBase := append([]byte(nil), base...)
	hook := &capturingAttemptTransformer{mutate: true}
	pipe := &Pipeline{relayHook: hook}
	request := &dto.ChatRequest{Model: "gpt-test", Stream: false}
	account := &accountreg.Snapshot{ID: 7, Platform: "codex", Type: "oauth", State: accountreg.StateActive}

	transformed := pipe.transformSelectedCodexAttempt(
		context.Background(), codexAttemptContext(t, true), account, request,
		adaptor.EndpointResponses, "openai", base, forwardOptions{}, "request-1",
	)

	if hook.calls != 1 {
		t.Fatalf("transform calls = %d, want 1", hook.calls)
	}
	if !bytes.Equal(base, wantBase) {
		t.Fatalf("shared failover body was mutated: got %q want %q", base, wantBase)
	}
	if !bytes.Contains(transformed, []byte(`"overage":true`)) {
		t.Fatalf("attempt-local replacement was not returned: %s", transformed)
	}
	if hook.seen.Client != "codex" || hook.seen.Account.ID != account.ID {
		t.Fatalf("attempt metadata = %+v", hook.seen)
	}
	transformed[0] = 'x'
	if !bytes.Equal(base, wantBase) {
		t.Fatalf("returned replacement aliases shared failover body: %q", base)
	}
}

func TestTransformSelectedCodexAttemptGate(t *testing.T) {
	tests := []struct {
		name        string
		platform    string
		accountType string
		endpoint    string
		codexClient bool
		rawBody     []byte
		wantCalls   int
	}{
		{name: "Codex OAuth from Codex CLI", platform: "codex", accountType: "oauth", endpoint: adaptor.EndpointResponses, codexClient: true, wantCalls: 1},
		{name: "Codex API key from Codex CLI", platform: "codex", accountType: "api_key", endpoint: adaptor.EndpointResponses, codexClient: true, wantCalls: 1},
		{name: "XAI account from Codex CLI", platform: "xai", accountType: "oauth", endpoint: adaptor.EndpointResponses, codexClient: true, wantCalls: 1},
		{name: "OpenAI account from Codex CLI", platform: "openai", accountType: "oauth", endpoint: adaptor.EndpointResponses, codexClient: true, wantCalls: 1},
		{name: "OpenAI Codex spelling remains ordinary account metadata", platform: "openai-codex", accountType: "oauth", endpoint: adaptor.EndpointResponses, codexClient: true, wantCalls: 1},
		{name: "ordinary client", platform: "codex", accountType: "oauth", endpoint: adaptor.EndpointResponses},
		{name: "non Responses endpoint", platform: "codex", accountType: "oauth", endpoint: adaptor.EndpointChatCompletions, codexClient: true},
		{name: "raw provider contract", platform: "codex", accountType: "oauth", endpoint: adaptor.EndpointResponses, codexClient: true, rawBody: []byte(`{}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base := []byte(`{"model":"gpt-test","stream":false,"input":"base"}`)
			hook := &capturingAttemptTransformer{}
			pipe := &Pipeline{relayHook: hook}
			result := pipe.transformSelectedCodexAttempt(
				context.Background(), codexAttemptContext(t, test.codexClient),
				&accountreg.Snapshot{ID: 7, Platform: test.platform, Type: test.accountType},
				&dto.ChatRequest{Model: "gpt-test", Stream: false}, test.endpoint, "openai", base,
				forwardOptions{rawBody: test.rawBody}, "request-1",
			)
			if hook.calls != test.wantCalls {
				t.Fatalf("transform calls = %d, want %d", hook.calls, test.wantCalls)
			}
			if test.wantCalls == 0 && !bytes.Equal(result, base) {
				t.Fatalf("gated request body changed: got %s want %s", result, base)
			}
		})
	}
}
