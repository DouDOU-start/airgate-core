package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	"github.com/DouDOU-start/airgate-core/internal/relay/cpa"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
	"github.com/DouDOU-start/airgate-core/internal/relay/relayhook"
)

type staticRelayHook struct {
	mu       sync.Mutex
	request  relayhook.Request
	decision relayhook.Decision
	err      error
}

// codexInstructionRelayHook mirrors the plugin's pre-dispatch responsibility:
// instruction injection is scoped to the inbound Codex client, while account-
// dependent overage history remains outside this shared failover body.
type codexInstructionRelayHook struct {
	mu          sync.Mutex
	request     relayhook.Request
	instruction string
}

func (h *codexInstructionRelayHook) BeforeDispatch(_ context.Context, req relayhook.Request) (relayhook.Decision, error) {
	h.mu.Lock()
	h.request = req
	h.mu.Unlock()
	if req.Client != "codex" {
		return relayhook.Decision{}, nil
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return relayhook.Decision{}, err
	}
	var current string
	if raw := body["instructions"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &current); err != nil {
			return relayhook.Decision{}, err
		}
	}
	updated, err := json.Marshal(current + "\n\n" + h.instruction)
	if err != nil {
		return relayhook.Decision{}, err
	}
	body["instructions"] = updated
	rewritten, err := json.Marshal(body)
	if err != nil {
		return relayhook.Decision{}, err
	}
	return relayhook.Decision{Version: relayhook.VersionV1, RequestBody: rewritten}, nil
}

func (h *codexInstructionRelayHook) lastRequest() relayhook.Request {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.request
}

func (h *staticRelayHook) BeforeDispatch(_ context.Context, req relayhook.Request) (relayhook.Decision, error) {
	h.mu.Lock()
	h.request = req
	h.mu.Unlock()
	return h.decision, h.err
}

func (h *staticRelayHook) lastRequest() relayhook.Request {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.request
}

func TestRelayHookReplacesResponsesBodyBeforeForward(t *testing.T) {
	var hits atomic.Int32
	var lastBody atomic.Value
	upstream := newResponsesUpstream(t, &hits, &lastBody)
	defer upstream.Close()

	hook := &staticRelayHook{decision: relayhook.Decision{
		Version: relayhook.VersionV1,
		RequestBody: json.RawMessage(`{
			"model":"gpt-4o",
			"input":[
				{"type":"function_call","call_id":"call_test","name":"zz","arguments":"{}"},
				{"type":"function_call_output","call_id":"call_test","output":"{}"},
				{"role":"user","content":[{"type":"input_text","text":"hi"}]}
			]
		}`),
	}}
	env := newTestEnv(t, testSnap(1, upstream.URL))
	env.pipe.relayHook = hook

	httpReq := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-4o","input":"hi"}`))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", "codex/1.2.3")
	w := httptest.NewRecorder()
	env.engine.ServeHTTP(w, httpReq)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	sent, _ := lastBody.Load().(string)
	for _, want := range []string{`"type":"function_call"`, `"type":"function_call_output"`, `"call_id":"call_test"`} {
		if !strings.Contains(sent, want) {
			t.Errorf("上游请求体缺少注入片段 %s: %s", want, sent)
		}
	}
	called := hook.lastRequest()
	if called.Version != relayhook.VersionV1 || called.Endpoint != "responses" || called.GroupID != 7 || called.Client != "codex" {
		t.Errorf("Hook 请求元数据异常: %+v", called)
	}
}

func TestCodexClientInstructionRewriteReachesOpenAIAndXAICPAAccounts(t *testing.T) {
	tests := []struct {
		platform   string
		originator string
	}{
		{platform: "openai", originator: "codex_cli_rs"},
		{platform: "openai", originator: "codex_sdk_ts"},
		{platform: "xai", originator: "codex_cli_rs"},
		{platform: "xai", originator: "codex_sdk_ts"},
	}
	for _, test := range tests {
		t.Run(test.platform+"/"+test.originator, func(t *testing.T) {
			env := newTestEnv(t)
			accounts := accountreg.New(hookAccountLoader{snaps: []accountreg.Snapshot{{
				ID: 101, Name: test.platform + "-cpa", Platform: test.platform, Type: "api_key",
				Priority: 100, Weight: 10, State: accountreg.StateActive,
				Models: map[string]struct{}{"gpt-4o": {}}, GroupIDs: map[int]struct{}{7: {}},
				Credentials: map[string]string{"api_key": "test-token"},
			}}}, nil)
			if err := accounts.Reload(context.Background()); err != nil {
				t.Fatalf("load %s account registry: %v", test.platform, err)
			}

			hook := &codexInstructionRelayHook{instruction: "gateway policy"}
			forwarder := &scriptedAccountForwarder{forward: func(req cpa.ForwardRequest) cpa.ForwardResult {
				return cpa.ForwardResult{
					StatusCode: http.StatusOK, ContentType: "application/json",
					Body:  []byte(`{"id":"resp-cpa","object":"response","status":"completed","model":"` + req.Model + `","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`),
					Usage: &dto.Usage{PromptTokens: 1, CompletionTokens: 1},
				}
			}}
			env.pipe.accounts = accounts
			env.pipe.cpa = forwarder
			env.pipe.codexTransportPolicy = staticCodexPolicy("native_only")
			env.pipe.relayHook = hook

			httpReq := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(
				`{"model":"gpt-4o","instructions":"base","input":"hi","metadata":{"keep":"yes"}}`,
			))
			httpReq.Header.Set("Content-Type", "application/json")
			httpReq.Header.Set("Originator", test.originator)
			response := httptest.NewRecorder()
			env.engine.ServeHTTP(response, httpReq)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}

			requests := forwarder.forwardedRequests()
			if len(requests) != 1 {
				t.Fatalf("CPA requests=%d, want 1", len(requests))
			}
			forwarded := requests[0]
			if forwarded.Account.Platform != test.platform || forwarded.Endpoint != "responses" {
				t.Fatalf("CPA target=%+v endpoint=%q", forwarded.Account, forwarded.Endpoint)
			}
			var payload struct {
				Model        string            `json:"model"`
				Instructions string            `json:"instructions"`
				Input        string            `json:"input"`
				Metadata     map[string]string `json:"metadata"`
			}
			if err := json.Unmarshal(forwarded.Payload, &payload); err != nil {
				t.Fatalf("decode CPA payload: %v; body=%s", err, forwarded.Payload)
			}
			if payload.Instructions != "base\n\ngateway policy" {
				t.Fatalf("CPA instructions=%q", payload.Instructions)
			}
			if payload.Model != "gpt-4o" || payload.Input != "hi" || payload.Metadata["keep"] != "yes" {
				t.Fatalf("instruction-only rewrite changed other fields: %+v", payload)
			}
			called := hook.lastRequest()
			if called.Client != "codex" || called.Endpoint != "responses" {
				t.Fatalf("hook request identity=%+v", called)
			}
		})
	}
}

func TestOrdinaryClientDoesNotReceiveCodexInstructionRewriteOnOpenAIAndXAICPAAccounts(t *testing.T) {
	for _, platform := range []string{"openai", "xai"} {
		t.Run(platform, func(t *testing.T) {
			env := newTestEnv(t)
			accounts := accountreg.New(hookAccountLoader{snaps: []accountreg.Snapshot{{
				ID: 101, Name: platform + "-cpa", Platform: platform, Type: "api_key",
				Priority: 100, Weight: 10, State: accountreg.StateActive,
				Models: map[string]struct{}{"gpt-4o": {}}, GroupIDs: map[int]struct{}{7: {}},
				Credentials: map[string]string{"api_key": "test-token"},
			}}}, nil)
			if err := accounts.Reload(context.Background()); err != nil {
				t.Fatalf("load %s account registry: %v", platform, err)
			}

			hook := &codexInstructionRelayHook{instruction: "gateway policy"}
			forwarder := &scriptedAccountForwarder{forward: func(req cpa.ForwardRequest) cpa.ForwardResult {
				return cpa.ForwardResult{
					StatusCode: http.StatusOK, ContentType: "application/json",
					Body:  []byte(`{"id":"resp-cpa","object":"response","status":"completed","model":"` + req.Model + `","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`),
					Usage: &dto.Usage{PromptTokens: 1, CompletionTokens: 1},
				}
			}}
			env.pipe.accounts = accounts
			env.pipe.cpa = forwarder
			// A plugin-wide native_only policy is deliberately present here: it
			// must not affect a non-Codex account selected for an ordinary client.
			env.pipe.codexTransportPolicy = staticCodexPolicy("native_only")
			env.pipe.relayHook = hook

			httpReq := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(
				`{"model":"gpt-4o","instructions":"base","input":"hi","metadata":{"keep":"yes"}}`,
			))
			httpReq.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			env.engine.ServeHTTP(response, httpReq)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}

			requests := forwarder.forwardedRequests()
			if len(requests) != 1 {
				t.Fatalf("CPA requests=%d, want 1", len(requests))
			}
			forwarded := requests[0]
			if forwarded.Account.Platform != platform || forwarded.Endpoint != "responses" {
				t.Fatalf("CPA target=%+v endpoint=%q", forwarded.Account, forwarded.Endpoint)
			}
			var payload struct {
				Model        string            `json:"model"`
				Instructions string            `json:"instructions"`
				Input        string            `json:"input"`
				Metadata     map[string]string `json:"metadata"`
			}
			if err := json.Unmarshal(forwarded.Payload, &payload); err != nil {
				t.Fatalf("decode CPA payload: %v; body=%s", err, forwarded.Payload)
			}
			if payload.Instructions != "base" {
				t.Fatalf("ordinary client instructions=%q, want original value", payload.Instructions)
			}
			if payload.Model != "gpt-4o" || payload.Input != "hi" || payload.Metadata["keep"] != "yes" {
				t.Fatalf("ordinary request changed unexpectedly: %+v", payload)
			}
			called := hook.lastRequest()
			if called.Client != "" || called.Endpoint != "responses" {
				t.Fatalf("hook request identity=%+v", called)
			}
		})
	}
}

func TestValidateRelayHookDecisionFiltersAccountsAndPreservesModelStream(t *testing.T) {
	original, err := parseHookTestRequest(`{"model":"gpt-4o","input":"hi","stream":true}`)
	if err != nil {
		t.Fatal(err)
	}
	candidates := []relayhook.Candidate{
		{Kind: "account", ID: 20},
		{Kind: "channel", ID: 20},
	}
	next, plan, err := validateRelayHookDecision(original, candidates, relayhook.Decision{
		Version: relayhook.VersionV1,
		RequestBody: json.RawMessage(
			`{"model":"gpt-4o","input":[],"stream":true}`,
		),
		Route: &relayhook.RoutePlan{AccountIDs: []int{999, 20, 20}, Fallback: relayhook.FallbackCore},
	})
	if err != nil {
		t.Fatalf("决策校验失败: %v", err)
	}
	if next == original {
		t.Fatal("有效替换体未生效")
	}
	if plan == nil || len(plan.AccountIDs) != 1 || plan.AccountIDs[0] != 20 {
		t.Fatalf("账号计划过滤异常: %+v", plan)
	}

	_, _, err = validateRelayHookDecision(original, candidates, relayhook.Decision{
		Version:     relayhook.VersionV1,
		RequestBody: json.RawMessage(`{"model":"other","input":[],"stream":true}`),
	})
	if err == nil {
		t.Fatal("修改 model 的决策应被拒绝")
	}
	_, _, err = validateRelayHookDecision(original, candidates, relayhook.Decision{
		Version: relayhook.VersionV1,
		Route:   &relayhook.RoutePlan{AccountIDs: []int{20}, Fallback: "none"},
	})
	if err == nil {
		t.Fatal("未知 fallback 应被拒绝")
	}
}

func parseHookTestRequest(body string) (*dto.ChatRequest, error) {
	return dto.ParseChatRequest([]byte(body))
}

type hookAccountLoader struct {
	snaps []accountreg.Snapshot
}

func (l hookAccountLoader) LoadAllForAccountRegistry(context.Context) ([]accountreg.Snapshot, error) {
	return l.snaps, nil
}

func TestPickRouteUsesPluginAccountOrderThenCoreFallback(t *testing.T) {
	until := time.Now().Add(time.Hour)
	accounts := accountreg.New(hookAccountLoader{snaps: []accountreg.Snapshot{
		{ID: 1, Name: "账号一", Platform: "codex", Type: "oauth", Priority: 100, Weight: 10, State: accountreg.StateActive, Models: map[string]struct{}{"gpt-4o": {}}, GroupIDs: map[int]struct{}{7: {}}},
		{ID: 2, Name: "账号二", Platform: "codex", Type: "oauth", Priority: 1, Weight: 10, State: accountreg.StateRateLimited, StateUntil: &until, Models: map[string]struct{}{"gpt-4o": {}}, GroupIDs: map[int]struct{}{7: {}}},
	}}, nil)
	if err := accounts.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	pipe := &Pipeline{accounts: accounts}
	plan := &relayhook.RoutePlan{AccountIDs: []int{2, 1}, Fallback: relayhook.FallbackCore}

	target, ok := pipe.pickRoute(7, "gpt-4o", "openai", nil, nil, plan)
	if !ok || target.account == nil || target.account.ID != 1 {
		t.Fatalf("未授权限流账号时选择 = %+v，期望账号 1", target)
	}
	plan.AllowRateLimitedAccountIDs = []int{2}
	target, ok = pipe.pickRoute(7, "gpt-4o", "openai", nil, nil, plan)
	if !ok || target.account == nil || target.account.ID != 2 {
		t.Fatalf("显式授权后的首选账号 = %+v，期望账号 2", target)
	}
	target, ok = pipe.pickRoute(7, "gpt-4o", "openai", nil, []int{2}, plan)
	if !ok || target.account == nil || target.account.ID != 1 {
		t.Fatalf("账号 2 排除后选择 = %+v，期望账号 1", target)
	}

	plan = &relayhook.RoutePlan{AccountIDs: []int{999}, Fallback: relayhook.FallbackCore}
	target, ok = pipe.pickRoute(7, "gpt-4o", "openai", nil, nil, plan)
	if !ok || target.account == nil || target.account.ID != 1 {
		t.Fatalf("插件账号均不可用时未回到 Core 调度: %+v", target)
	}
}

func TestRelayHookErrorFailsOpen(t *testing.T) {
	var hits atomic.Int32
	var lastBody atomic.Value
	upstream := newResponsesUpstream(t, &hits, &lastBody)
	defer upstream.Close()

	hook := &staticRelayHook{err: errors.New("插件不可用")}
	env := newTestEnv(t, testSnap(1, upstream.URL))
	env.pipe.relayHook = hook
	w := env.doResponses(t, `{"model":"gpt-4o","input":"hi"}`)
	if w.Code != http.StatusOK || hits.Load() != 1 {
		t.Fatalf("插件错误不应阻断原转发: status=%d hits=%d body=%s", w.Code, hits.Load(), w.Body.String())
	}
	sent, _ := lastBody.Load().(string)
	if !strings.Contains(sent, `"input":"hi"`) {
		t.Fatalf("fail-open 未沿用原请求体: %s", sent)
	}
}
