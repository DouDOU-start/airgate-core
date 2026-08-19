package pipeline

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	"github.com/DouDOU-start/airgate-core/internal/relay/cpa"
	"github.com/DouDOU-start/airgate-core/internal/relay/relayhook"
	"github.com/DouDOU-start/airgate-core/internal/requestaudit"
)

type scriptedAccountForwarder struct {
	mu       sync.Mutex
	calls    []int
	requests []cpa.ForwardRequest
	forward  func(cpa.ForwardRequest) cpa.ForwardResult
}

func (f *scriptedAccountForwarder) Forward(_ context.Context, _ *gin.Context, req cpa.ForwardRequest) cpa.ForwardResult {
	f.mu.Lock()
	f.calls = append(f.calls, req.Account.AccountID)
	f.requests = append(f.requests, req)
	f.mu.Unlock()
	return f.forward(req)
}

func (f *scriptedAccountForwarder) forwardedRequests() []cpa.ForwardRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]cpa.ForwardRequest(nil), f.requests...)
}

func TestCursorSessionKeyThroughRelayHookAccountRoute(t *testing.T) {
	env := newTestEnv(t)
	accounts := accountreg.New(&fakeAccountLoader{snaps: []accountreg.Snapshot{{
		ID: 1, Name: "cursor-1", Platform: "cursor", Type: "oauth",
		Priority: 100, Weight: 10, State: accountreg.StateActive,
		Models: map[string]struct{}{testModel: {}}, GroupIDs: map[int]struct{}{7: {}},
	}}}, nil)
	if err := accounts.Reload(context.Background()); err != nil {
		t.Fatalf("账号注册表加载失败: %v", err)
	}
	forwarder := &scriptedAccountForwarder{forward: func(req cpa.ForwardRequest) cpa.ForwardResult {
		return cpa.ForwardResult{
			StatusCode: http.StatusOK, ContentType: "application/json",
			Body: []byte(`{"id":"chat-account","model":"` + req.Model + `","choices":[{"message":{"role":"assistant","content":"ok"}}]}`),
		}
	}}
	env.pipe.accounts = accounts
	env.pipe.cpa = forwarder
	env.pipe.relayHook = &staticRelayHook{decision: relayhook.Decision{
		Version: relayhook.VersionV1,
		Route:   &relayhook.RoutePlan{AccountIDs: []int{1}, Fallback: relayhook.FallbackCore},
	}}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"`+testModel+`","messages":[{"role":"user","content":"hello"}]}`,
	))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Claude-Code-Session-Id", "claude-session-1")
	response := httptest.NewRecorder()
	env.engine.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	requests := forwarder.forwardedRequests()
	if len(requests) != 1 || requests[0].CursorSessionKey == "" ||
		!strings.Contains(requests[0].CursorSessionKey, "claude:claude-session-1") {
		t.Fatalf("Cursor session key 未贯通到 CPA: %+v", requests)
	}
}

func (f *scriptedAccountForwarder) accountCalls() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.calls...)
}

func TestRateLimitedPluginAccountsDoNotStarveCoreFallback(t *testing.T) {
	var channelHits atomic.Int32
	var lastBody atomic.Value
	upstream := newResponsesUpstream(t, &channelHits, &lastBody)
	defer upstream.Close()

	env := newTestEnv(t, testSnap(99, upstream.URL))
	accounts := newRateLimitProbeAccounts(t, 3)
	forwarder := &scriptedAccountForwarder{forward: func(cpa.ForwardRequest) cpa.ForwardResult {
		return cpa.ForwardResult{
			StatusCode:  http.StatusTooManyRequests,
			Headers:     http.Header{"Retry-After": []string{"1"}},
			Body:        []byte(`{"error":{"message":"still rate limited"}}`),
			ContentType: "application/json",
		}
	}}
	env.pipe.accounts = accounts
	env.pipe.cpa = forwarder
	env.pipe.relayHook = &staticRelayHook{decision: relayhook.Decision{
		Version: relayhook.VersionV1,
		Route: &relayhook.RoutePlan{
			AccountIDs:                 []int{1, 2, 3},
			AllowRateLimitedAccountIDs: []int{1, 2, 3},
			Fallback:                   relayhook.FallbackCore,
		},
	}}

	// 前三个请求各探测一个不同限流账号；第四个请求应直接走 Core fallback。
	for requestNumber := 1; requestNumber <= 4; requestNumber++ {
		response := env.doResponses(t, `{"model":"gpt-4o","input":"hi"}`)
		if response.Code != http.StatusOK {
			t.Fatalf("第 %d 个请求 status=%d body=%s", requestNumber, response.Code, response.Body.String())
		}
	}

	if got := fmt.Sprint(forwarder.accountCalls()); got != "[1 2 3]" {
		t.Fatalf("限流账号调用顺序=%s，期望每个账号只探测一次 [1 2 3]", got)
	}
	if got := channelHits.Load(); got != 4 {
		t.Fatalf("健康 Core fallback 命中=%d，期望 4", got)
	}
}

func TestSuccessfulRateLimitProbeRestoresAccountForFollowingRequests(t *testing.T) {
	env := newTestEnv(t)
	accounts := newRateLimitProbeAccounts(t, 1)
	forwarder := &scriptedAccountForwarder{forward: func(req cpa.ForwardRequest) cpa.ForwardResult {
		return cpa.ForwardResult{
			StatusCode:  http.StatusOK,
			Body:        []byte(`{"id":"resp-account","model":"` + req.Model + `","output":[]}`),
			ContentType: "application/json",
		}
	}}
	env.pipe.accounts = accounts
	env.pipe.cpa = forwarder
	env.pipe.relayHook = &staticRelayHook{decision: relayhook.Decision{
		Version: relayhook.VersionV1,
		Route: &relayhook.RoutePlan{
			AccountIDs:                 []int{1},
			AllowRateLimitedAccountIDs: []int{1},
			Fallback:                   relayhook.FallbackCore,
		},
	}}

	for requestNumber := 1; requestNumber <= 2; requestNumber++ {
		response := env.doResponses(t, `{"model":"gpt-4o","input":"hi"}`)
		if response.Code != http.StatusOK {
			t.Fatalf("第 %d 个请求 status=%d body=%s", requestNumber, response.Code, response.Body.String())
		}
	}

	if got := fmt.Sprint(forwarder.accountCalls()); got != "[1 1]" {
		t.Fatalf("账号调用=%s，期望恢复后继续调度 [1 1]", got)
	}
	current, ok := accounts.Snapshot(1)
	if !ok || current.State != accountreg.StateActive || current.StateUntil != nil {
		t.Fatalf("恢复探测成功后的账号状态异常: %+v", current)
	}
}

func TestCanceledRateLimitProbeEntersCooldown(t *testing.T) {
	env := newTestEnv(t)
	accounts := newRateLimitProbeAccounts(t, 1)
	requestContext, cancel := context.WithCancel(context.Background())
	forwarder := &scriptedAccountForwarder{forward: func(cpa.ForwardRequest) cpa.ForwardResult {
		cancel()
		return cpa.ForwardResult{NetErr: context.Canceled}
	}}
	env.pipe.accounts = accounts
	env.pipe.cpa = forwarder
	env.pipe.relayHook = &staticRelayHook{decision: relayhook.Decision{
		Version: relayhook.VersionV1,
		Route: &relayhook.RoutePlan{
			AccountIDs:                 []int{1},
			AllowRateLimitedAccountIDs: []int{1},
			Fallback:                   relayhook.FallbackCore,
		},
	}}

	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-4o","input":"hi"}`)).WithContext(requestContext)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	env.engine.ServeHTTP(response, request)

	if got := fmt.Sprint(forwarder.accountCalls()); got != "[1]" {
		t.Fatalf("取消前账号调用=%s，期望 [1]", got)
	}
	if got, _ := accounts.BeginRateLimitProbe(1); got != accountreg.RateLimitProbeBlocked {
		t.Fatalf("结果未知的取消探测后决策=%v，期望进入冷却", got)
	}
	current, ok := accounts.Snapshot(1)
	if !ok || current.StateUntil == nil || time.Until(*current.StateUntil) < 29*time.Second {
		t.Fatalf("取消探测后的冷却时间不足: %+v", current)
	}
}

func TestPanickedRateLimitProbeEntersCooldown(t *testing.T) {
	env := newTestEnv(t)
	accounts := newRateLimitProbeAccounts(t, 1)
	env.pipe.accounts = accounts
	env.pipe.cpa = &scriptedAccountForwarder{forward: func(cpa.ForwardRequest) cpa.ForwardResult {
		panic("测试探测异常")
	}}
	env.pipe.relayHook = singleRateLimitProbeHook(1)

	func() {
		defer func() {
			if recovered := recover(); recovered == nil {
				t.Fatal("期望转发异常继续向外传播")
			}
		}()
		env.doResponses(t, `{"model":"gpt-4o","input":"hi"}`)
	}()

	assertProbeCooling(t, accounts, 1, "执行异常")
}

func TestAuditFailedRateLimitProbeEntersCooldown(t *testing.T) {
	env := newTestEnv(t)
	accounts := newRateLimitProbeAccounts(t, 1)
	env.pipe.accounts = accounts
	env.pipe.cpa = &scriptedAccountForwarder{forward: func(cpa.ForwardRequest) cpa.ForwardResult {
		return cpa.ForwardResult{NetErr: requestaudit.ErrWrite}
	}}
	env.pipe.relayHook = singleRateLimitProbeHook(1)

	response := env.doResponses(t, `{"model":"gpt-4o","input":"hi"}`)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("审计失败 status=%d body=%s", response.Code, response.Body.String())
	}
	assertProbeCooling(t, accounts, 1, "审计失败")
}

func singleRateLimitProbeHook(accountID int) *staticRelayHook {
	return &staticRelayHook{decision: relayhook.Decision{
		Version: relayhook.VersionV1,
		Route: &relayhook.RoutePlan{
			AccountIDs:                 []int{accountID},
			AllowRateLimitedAccountIDs: []int{accountID},
			Fallback:                   relayhook.FallbackCore,
		},
	}}
}

func assertProbeCooling(t *testing.T, accounts *accountreg.Registry, accountID int, scenario string) {
	t.Helper()
	if got, _ := accounts.BeginRateLimitProbe(accountID); got != accountreg.RateLimitProbeBlocked {
		t.Fatalf("%s后探测决策=%v，期望进入冷却", scenario, got)
	}
	current, ok := accounts.Snapshot(accountID)
	if !ok || current.StateUntil == nil || time.Until(*current.StateUntil) < 29*time.Second {
		t.Fatalf("%s后的冷却时间不足: %+v", scenario, current)
	}
}

func newRateLimitProbeAccounts(t *testing.T, count int) *accountreg.Registry {
	t.Helper()
	until := time.Now().Add(time.Hour)
	snapshots := make([]accountreg.Snapshot, 0, count)
	for id := 1; id <= count; id++ {
		snapshots = append(snapshots, accountreg.Snapshot{
			ID: id, Name: fmt.Sprintf("codex-%d", id), Platform: "codex", Type: "oauth",
			Priority: 100, Weight: 10, State: accountreg.StateRateLimited, StateUntil: &until,
			Models: map[string]struct{}{testModel: {}}, GroupIDs: map[int]struct{}{7: {}},
		})
	}
	accounts := accountreg.New(&fakeAccountLoader{snaps: snapshots}, nil)
	if err := accounts.Reload(context.Background()); err != nil {
		t.Fatalf("账号注册表加载失败: %v", err)
	}
	return accounts
}
