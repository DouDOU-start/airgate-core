package pipeline

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	"github.com/DouDOU-start/airgate-core/internal/relay/cpa"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
)

func TestAffinity慢账号迁移会更新主绑定(t *testing.T) {
	env := newTestEnv(t)
	env.pipe.accounts = newActiveAffinityAccounts(t)
	forwarder := successfulAffinityForwarder()
	env.pipe.cpa = forwarder
	for range accountAffinityRebindMinSamples {
		env.pipe.recordAccountFirstToken(1, testModel, 9_000)
		env.pipe.recordAccountFirstToken(2, testModel, 3_000)
	}

	sessionID := "slow-rebind"
	sessionKey := affinityKey(testKeyInfo().UserID, testKeyInfo().GroupID, testModel, registry.ProtocolOpenAI, "header:"+sessionID)
	env.pipe.sessionAffinity.bind(sessionKey, routeAccount, 1)
	response := doAffinityResponses(t, env, sessionID)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if got := fmt.Sprint(forwarder.accountCalls()); got != "[2]" {
		t.Fatalf("慢账号迁移后的实际调用=%s，期望 [2]", got)
	}
	kind, id, ok := env.pipe.sessionAffinity.lookup(sessionKey)
	if !ok || kind != routeAccount || id != 2 {
		t.Fatalf("慢账号迁移后主绑定=(%v,%d,%v)，期望账号 2", kind, id, ok)
	}
}

func TestAffinity并发临时分流不修改主绑定(t *testing.T) {
	env := newTestEnv(t)
	env.pipe.accounts = newActiveAffinityAccounts(t)
	forwarder := successfulAffinityForwarder()
	env.pipe.cpa = forwarder
	for range accountLatencyMinSamples {
		env.pipe.recordAccountFirstToken(1, testModel, 3_000)
		env.pipe.recordAccountFirstToken(2, testModel, 5_000)
	}
	releaseFirst := env.pipe.trackAccountAttempt(1)
	releaseSecond := env.pipe.trackAccountAttempt(1)
	defer releaseFirst()
	defer releaseSecond()

	sessionID := "parallel-spill"
	sessionKey := affinityKey(testKeyInfo().UserID, testKeyInfo().GroupID, testModel, registry.ProtocolOpenAI, "header:"+sessionID)
	env.pipe.sessionAffinity.bind(sessionKey, routeAccount, 1)
	response := doAffinityResponses(t, env, sessionID)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if got := fmt.Sprint(forwarder.accountCalls()); got != "[2]" {
		t.Fatalf("并发临时分流后的实际调用=%s，期望 [2]", got)
	}
	kind, id, ok := env.pipe.sessionAffinity.lookup(sessionKey)
	if !ok || kind != routeAccount || id != 1 {
		t.Fatalf("临时分流不应修改主绑定，实际=(%v,%d,%v)", kind, id, ok)
	}
}

func TestAffinity临时分流目标失败不删除主绑定(t *testing.T) {
	env := newTestEnv(t)
	env.pipe.accounts = newActiveAffinityAccounts(t)
	forwarder := affinityFailoverForwarder(2)
	env.pipe.cpa = forwarder
	for range accountLatencyMinSamples {
		env.pipe.recordAccountFirstToken(1, testModel, 3_000)
		env.pipe.recordAccountFirstToken(2, testModel, 5_000)
	}
	releaseFirst := env.pipe.trackAccountAttempt(1)
	releaseSecond := env.pipe.trackAccountAttempt(1)
	defer releaseFirst()
	defer releaseSecond()

	sessionID := "parallel-spill-failover"
	sessionKey := affinityKey(testKeyInfo().UserID, testKeyInfo().GroupID, testModel, registry.ProtocolOpenAI, "header:"+sessionID)
	env.pipe.sessionAffinity.bind(sessionKey, routeAccount, 1)
	response := doAffinityResponses(t, env, sessionID)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if got := fmt.Sprint(forwarder.accountCalls()); got != "[2 1]" {
		t.Fatalf("临时分流失败后的调用顺序=%s，期望 [2 1]", got)
	}
	kind, id, ok := env.pipe.sessionAffinity.lookup(sessionKey)
	if !ok || kind != routeAccount || id != 1 {
		t.Fatalf("临时分流失败不应删除主绑定，实际=(%v,%d,%v)", kind, id, ok)
	}
}

func TestAffinity迁移目标失败后恢复健康绑定(t *testing.T) {
	env := newTestEnv(t)
	env.pipe.accounts = newActiveAffinityAccounts(t)
	forwarder := affinityFailoverForwarder(2)
	env.pipe.cpa = forwarder
	for range accountAffinityRebindMinSamples {
		env.pipe.recordAccountFirstToken(1, testModel, 9_000)
		env.pipe.recordAccountFirstToken(2, testModel, 3_000)
	}

	sessionID := "slow-rebind-failover"
	sessionKey := affinityKey(testKeyInfo().UserID, testKeyInfo().GroupID, testModel, registry.ProtocolOpenAI, "header:"+sessionID)
	env.pipe.sessionAffinity.bind(sessionKey, routeAccount, 1)
	response := doAffinityResponses(t, env, sessionID)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if got := fmt.Sprint(forwarder.accountCalls()); got != "[2 1]" {
		t.Fatalf("迁移目标失败后的调用顺序=%s，期望 [2 1]", got)
	}
	kind, id, ok := env.pipe.sessionAffinity.lookup(sessionKey)
	if !ok || kind != routeAccount || id != 1 {
		t.Fatalf("迁移目标失败后应恢复健康绑定，实际=(%v,%d,%v)", kind, id, ok)
	}
}

func newActiveAffinityAccounts(t *testing.T) *accountreg.Registry {
	t.Helper()
	accounts := accountreg.New(&fakeAccountLoader{snaps: []accountreg.Snapshot{
		{ID: 1, Name: "codex-1", Platform: "codex", Type: "oauth", Priority: 50, Weight: 10,
			State: accountreg.StateActive, Models: map[string]struct{}{testModel: {}}, GroupIDs: map[int]struct{}{7: {}}},
		{ID: 2, Name: "codex-2", Platform: "codex", Type: "oauth", Priority: 50, Weight: 10,
			State: accountreg.StateActive, Models: map[string]struct{}{testModel: {}}, GroupIDs: map[int]struct{}{7: {}}},
	}}, nil)
	if err := accounts.Reload(context.Background()); err != nil {
		t.Fatalf("账号注册表加载失败: %v", err)
	}
	return accounts
}

func successfulAffinityForwarder() *scriptedAccountForwarder {
	return &scriptedAccountForwarder{forward: func(req cpa.ForwardRequest) cpa.ForwardResult {
		return successfulAffinityResult(req)
	}}
}

func affinityFailoverForwarder(failingAccountID int) *scriptedAccountForwarder {
	return &scriptedAccountForwarder{forward: func(req cpa.ForwardRequest) cpa.ForwardResult {
		if req.Account.AccountID == failingAccountID {
			return cpa.ForwardResult{
				StatusCode:  http.StatusTooManyRequests,
				Headers:     http.Header{"Retry-After": []string{"1"}},
				Body:        []byte(`{"error":{"message":"rate limited"}}`),
				ContentType: "application/json",
			}
		}
		return successfulAffinityResult(req)
	}}
}

func successfulAffinityResult(req cpa.ForwardRequest) cpa.ForwardResult {
	return cpa.ForwardResult{
		StatusCode: http.StatusOK,
		Body: []byte(`{"id":"resp-affinity","model":"` + req.Model +
			`","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`),
		ContentType: "application/json",
		Usage:       &dto.Usage{PromptTokens: 1, CompletionTokens: 1},
	}
}

func doAffinityResponses(t *testing.T, env *testEnv, sessionID string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-4o","input":"hi"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Session-Id", sessionID)
	response := httptest.NewRecorder()
	env.engine.ServeHTTP(response, request)
	return response
}
