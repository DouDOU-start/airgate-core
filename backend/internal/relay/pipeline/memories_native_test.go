package pipeline

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/cpa"
	providertransport "github.com/DouDOU-start/airgate-core/internal/relay/transport"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
)

type memoriesAccountLoader struct {
	accounts []accountreg.Snapshot
}

func (l memoriesAccountLoader) LoadAllForAccountRegistry(context.Context) ([]accountreg.Snapshot, error) {
	return l.accounts, nil
}

type memoriesNativeTransport struct {
	mu      sync.Mutex
	calls   int
	request providertransport.Request
	result  providertransport.Result
}

func (f *memoriesNativeTransport) Execute(_ context.Context, req providertransport.Request) providertransport.Result {
	f.mu.Lock()
	f.calls++
	f.request = req
	result := f.result
	f.mu.Unlock()
	return result
}

func (f *memoriesNativeTransport) SupportsAccount(account providertransport.Account) bool {
	return providertransport.IsCodexPlatform(account.Platform)
}

func (f *memoriesNativeTransport) snapshot() (int, providertransport.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, f.request
}

type memoriesCountingCPA struct {
	mu     sync.Mutex
	calls  int
	result cpa.ForwardResult
}

func (f *memoriesCountingCPA) Forward(context.Context, *gin.Context, cpa.ForwardRequest) cpa.ForwardResult {
	f.mu.Lock()
	f.calls++
	result := f.result
	f.mu.Unlock()
	return result
}

func (f *memoriesCountingCPA) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func newMemoriesNativePipeline(t *testing.T, native providertransport.ProviderTransport, legacy providertransport.LegacyForwarder) *Pipeline {
	t.Helper()
	accounts := accountreg.New(memoriesAccountLoader{accounts: []accountreg.Snapshot{{
		ID: 901, Platform: "codex", Type: "oauth", State: accountreg.StateActive,
		Priority: 1, Weight: 1,
		Credentials: map[string]string{"access_token": "native-token"},
		Models:      map[string]struct{}{"gpt-memory": {}},
		GroupIDs:    map[int]struct{}{7: {}},
	}}}, nil)
	if err := accounts.Reload(context.Background()); err != nil {
		t.Fatalf("load native account registry: %v", err)
	}
	return New(Options{
		Accounts:          accounts,
		Concurrency:       scheduler.NewConcurrencyManager(nil),
		RPM:               scheduler.NewRPMCounter(nil),
		ProviderTransport: native,
		CPA:               legacy,
	})
}

func serveMemoriesNative(t *testing.T, p *Pipeline, body, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/v1/memories/trace_summarize", func(c *gin.Context) {
		c.Set(middleware.CtxKeyKeyInfo, &auth.APIKeyInfo{
			KeyID: 1, UserID: 2, GroupID: 7, UserBalance: 100,
		})
	}, p.HandleMemoriesTraceSummarize)

	req := httptest.NewRequest(http.MethodPost, "/v1/memories/trace_summarize", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Originator", "codex_cli_rs")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, req)
	return response
}

func TestHandleMemoriesTraceSummarizeRejectsMissingModel(t *testing.T) {
	response := serveMemoriesNative(t, &Pipeline{}, `{"trace_id":"trace-1"}`, "application/json")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", response.Code, response.Body.String())
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"code":"missing_model"`)) {
		t.Fatalf("body = %s, want missing_model error", response.Body.String())
	}
}

func TestHandleMemoriesTraceSummarizePreservesNativeJSONWireAndSkipsCPA(t *testing.T) {
	body := "{\n  \"model\": \"gpt-memory\",\n  \"trace_id\": \"trace-1\",\n  \"unknown\": {\"nested\": [1, 2, 3]}\n}"
	native := &memoriesNativeTransport{result: providertransport.Result{
		StatusCode:  http.StatusOK,
		Headers:     http.Header{"Content-Type": {"application/json"}, "X-Native": {"memories"}},
		ContentType: "application/json",
		Body:        []byte(`{"id":"memory-1","object":"memory"}`),
	}}
	legacy := &memoriesCountingCPA{}
	p := newMemoriesNativePipeline(t, native, legacy)

	response := serveMemoriesNative(t, p, body, "application/json; charset=utf-8")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", response.Code, response.Body.String())
	}
	if got := response.Body.String(); got != `{"id":"memory-1","object":"memory"}` {
		t.Fatalf("response body = %q, want native JSON", got)
	}
	if response.Header().Get("X-Native") != "memories" {
		t.Fatalf("native response header missing: %#v", response.Header())
	}
	if got := legacy.callCount(); got != 0 {
		t.Fatalf("CPA calls = %d, want 0", got)
	}

	calls, request := native.snapshot()
	if calls != 1 {
		t.Fatalf("native calls = %d, want 1", calls)
	}
	if request.Endpoint != adaptor.EndpointMemoriesTraceSummarize {
		t.Fatalf("native endpoint = %q, want %q", request.Endpoint, adaptor.EndpointMemoriesTraceSummarize)
	}
	if request.Path != "/memories/trace_summarize" {
		t.Fatalf("native path = %q, want /memories/trace_summarize", request.Path)
	}
	if request.EntryProtocol != "openai" || request.Transport != "http" || request.Stream {
		t.Fatalf("native protocol/transport/stream = %q/%q/%v", request.EntryProtocol, request.Transport, request.Stream)
	}
	if string(request.Payload) != body {
		t.Fatalf("native payload changed:\ngot  %q\nwant %q", request.Payload, body)
	}
	if got := request.Headers.Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("native Content-Type = %q, want original media type", got)
	}
}

func TestHandleMemoriesTraceSummarizePassesThroughNativeClientError(t *testing.T) {
	body := `{"error":{"code":"trace_invalid","message":"invalid trace"}}`
	native := &memoriesNativeTransport{result: providertransport.Result{
		StatusCode:  http.StatusUnprocessableEntity,
		Headers:     http.Header{"Content-Type": {"application/vnd.codex+json"}, "X-Native-Error": {"trace-1"}},
		ContentType: "application/vnd.codex+json",
		Body:        []byte(body),
	}}
	legacy := &memoriesCountingCPA{}
	p := newMemoriesNativePipeline(t, native, legacy)

	response := serveMemoriesNative(t, p, `{"model":"gpt-memory","trace_id":"trace-1"}`, "application/json")
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", response.Code, response.Body.String())
	}
	if got := response.Body.String(); got != body {
		t.Fatalf("response body = %q, want exact native body %q", got, body)
	}
	if got := response.Header().Get("Content-Type"); got != "application/vnd.codex+json" {
		t.Fatalf("Content-Type = %q, want native content type", got)
	}
	if got := response.Header().Get("X-Native-Error"); got != "trace-1" {
		t.Fatalf("native error header = %q, want trace-1", got)
	}
	if got := legacy.callCount(); got != 0 {
		t.Fatalf("CPA calls = %d, want 0", got)
	}
	if calls, _ := native.snapshot(); calls != 1 {
		t.Fatalf("native calls = %d, want 1", calls)
	}
}
