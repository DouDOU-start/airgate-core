package pipeline

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	"github.com/DouDOU-start/airgate-core/internal/relay/cpa"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
)

type diagnosticAccountLoader struct {
	accounts []accountreg.Snapshot
}

func (l diagnosticAccountLoader) LoadAllForAccountRegistry(context.Context) ([]accountreg.Snapshot, error) {
	return l.accounts, nil
}

func TestLogCodexRateLimitedAccountSuccessRecordsCompleteRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	registry := accountreg.New(diagnosticAccountLoader{accounts: []accountreg.Snapshot{{
		ID: 17, Name: "codex-oauth", Platform: "codex", Type: "oauth",
		State: accountreg.StateActive,
	}}}, nil)
	if err := registry.Reload(context.Background()); err != nil {
		t.Fatalf("加载账号注册表失败: %v", err)
	}
	selected, ok := registry.Snapshot(17)
	if !ok {
		t.Fatal("未找到测试账号")
	}
	registry.MarkRateLimited(17, time.Now().Add(2*time.Minute), "并发请求返回 429")

	originalBody := []byte(`{ "model": "gpt-5", "input": "原始敏感提示词" }`)
	forwardBody := []byte(`{"input":"改写后的敏感提示词","model":"gpt-5"}`)
	request := httptest.NewRequest(http.MethodPost, "https://gateway.example/v1/responses?trace=full", bytes.NewReader(originalBody))
	request.Host = "gateway.example"
	request.RemoteAddr = "203.0.113.10:4567"
	request.Header.Set("Authorization", "Bearer sk-client-secret")
	request.Header.Set("Cookie", "session=raw-cookie")
	request.Header.Add("X-Diagnostic", "first")
	request.Header.Add("X-Diagnostic", "second")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = request
	c.Set(ctxKeyRelayInboundRequestBody, originalBody)
	c.Set(middleware.CtxKeyRequestID, "req-sensitive-1")

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)

	pipe := &Pipeline{accounts: registry}
	pipe.logCodexRateLimitedAccountSuccess(
		c,
		selected,
		"gpt-5",
		"responses",
		false,
		forwardBody,
		http.Header{"Content-Type": []string{"application/json"}},
		cpa.ForwardResult{
			StatusCode: http.StatusOK,
			Headers:    http.Header{"X-Request-Id": []string{"upstream-request-1"}},
		},
	)

	output := logs.String()
	for _, expected := range []string{
		"relay_codex_rate_limited_account_request_succeeded",
		"request_id=req-sensitive-1",
		"account_id=17",
		"selected_account_state=active",
		"current_account_state=rate_limited",
		"sk-client-secret",
		"session=raw-cookie",
		"first second",
		"原始敏感提示词",
		"改写后的敏感提示词",
		"upstream-request-1",
		"request_body_rewritten=true",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("诊断日志缺少 %q:\n%s", expected, output)
		}
	}
}

func TestLogCodexRateLimitedAccountSuccessRejectsOtherCases(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		account        accountreg.Snapshot
		currentState   string
		result         cpa.ForwardResult
		wantDiagnostic bool
	}{
		{
			name:         "当前账号仍活跃",
			account:      accountreg.Snapshot{ID: 1, Platform: "codex", Type: "oauth", State: accountreg.StateActive},
			currentState: accountreg.StateActive,
			result:       cpa.ForwardResult{StatusCode: http.StatusOK},
		},
		{
			name:         "非 Codex 账号",
			account:      accountreg.Snapshot{ID: 1, Platform: "xai", Type: "oauth", State: accountreg.StateActive},
			currentState: accountreg.StateRateLimited,
			result:       cpa.ForwardResult{StatusCode: http.StatusOK},
		},
		{
			name:         "Codex API Key 账号",
			account:      accountreg.Snapshot{ID: 1, Platform: "codex", Type: "api_key", State: accountreg.StateActive},
			currentState: accountreg.StateRateLimited,
			result:       cpa.ForwardResult{StatusCode: http.StatusOK},
		},
		{
			name:         "上游返回限流",
			account:      accountreg.Snapshot{ID: 1, Platform: "codex", Type: "oauth", State: accountreg.StateActive},
			currentState: accountreg.StateRateLimited,
			result:       cpa.ForwardResult{StatusCode: http.StatusTooManyRequests},
		},
		{
			name:         "流式响应未完整结束",
			account:      accountreg.Snapshot{ID: 1, Platform: "codex", Type: "oauth", State: accountreg.StateActive},
			currentState: accountreg.StateRateLimited,
			result:       cpa.ForwardResult{StatusCode: http.StatusOK, Written: true, Done: false},
		},
		{
			name:         "流式响应发生错误",
			account:      accountreg.Snapshot{ID: 1, Platform: "codex", Type: "oauth", State: accountreg.StateActive},
			currentState: accountreg.StateRateLimited,
			result:       cpa.ForwardResult{StatusCode: http.StatusOK, Written: true, Done: true, StreamErr: errors.New("断流")},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := test.account
			current.State = test.currentState
			registry := accountreg.New(diagnosticAccountLoader{accounts: []accountreg.Snapshot{current}}, nil)
			if err := registry.Reload(context.Background()); err != nil {
				t.Fatalf("加载账号注册表失败: %v", err)
			}

			request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5"}`))
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = request

			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			defer slog.SetDefault(previous)

			pipe := &Pipeline{accounts: registry}
			pipe.logCodexRateLimitedAccountSuccess(c, &test.account, "gpt-5", "responses", test.result.Written, []byte(`{"model":"gpt-5"}`), nil, test.result)

			gotDiagnostic := strings.Contains(logs.String(), "relay_codex_rate_limited_account_request_succeeded")
			if gotDiagnostic != test.wantDiagnostic {
				t.Fatalf("是否输出诊断日志 = %v，期望 %v；日志=%s", gotDiagnostic, test.wantDiagnostic, logs.String())
			}
		})
	}
}

func TestIsSuccessfulCPAResult(t *testing.T) {
	if !isSuccessfulCPAResult(cpa.ForwardResult{StatusCode: http.StatusOK}) {
		t.Error("非流式 200 应判定成功")
	}
	if !isSuccessfulCPAResult(cpa.ForwardResult{StatusCode: http.StatusOK, Written: true, Done: true}) {
		t.Error("完整流式 200 应判定成功")
	}
}
