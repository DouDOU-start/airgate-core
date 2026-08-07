package account

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/cpa"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
)

func TestAntigravity连通性测试通过CPA转发(t *testing.T) {
	repo := &antigravityTestRepo{item: Account{
		ID:       21,
		Name:     "Antigravity OAuth",
		Platform: "antigravity",
		Type:     TypeOAuth,
		Credentials: map[string]string{
			"access_token":  "测试访问令牌",
			"refresh_token": "测试刷新令牌",
			"project_id":    "测试项目",
		},
	}}
	forwarder := &antigravityTestForwarder{
		result: cpa.ForwardResult{
			StatusCode: http.StatusOK,
			Body: []byte(`{
				"choices":[{"message":{"role":"assistant","content":"连接成功"}}],
				"usage":{"prompt_tokens":3,"completion_tokens":2}
			}`),
		},
	}
	service := NewService(repo, "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	service.SetOAuthCredentialRefresher(forwarder)

	var events []TestEvent
	err := service.TestConnection(
		context.Background(),
		repo.item.ID,
		"gemini-2.5-flash",
		"请只回复连接成功",
		TestOptions{},
		func(event TestEvent) { events = append(events, event) },
	)
	if err != nil {
		t.Fatalf("Antigravity 连通性测试失败: %v", err)
	}
	if forwarder.calls != 1 {
		t.Fatalf("CPA 转发调用次数 = %d，期望 1", forwarder.calls)
	}
	if forwarder.refreshCalls != 0 {
		t.Fatalf("未过期凭证不应提前刷新，实际刷新次数 = %d", forwarder.refreshCalls)
	}
	request := forwarder.request
	if request.Account.Platform != "antigravity" || request.Account.AccountID != repo.item.ID {
		t.Fatalf("转发账号信息不正确: %+v", request.Account)
	}
	if request.Model != "gemini-2.5-flash" || request.Endpoint != adaptor.EndpointChatCompletions {
		t.Fatalf("转发模型或端点不正确: model=%q endpoint=%q", request.Model, request.Endpoint)
	}
	if request.EntryProtocol != registry.ProtocolOpenAI || request.Stream {
		t.Fatalf("转发协议或流式标记不正确: protocol=%q stream=%v", request.EntryProtocol, request.Stream)
	}
	var payload struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Stream bool `json:"stream"`
	}
	if err := json.Unmarshal(request.Payload, &payload); err != nil {
		t.Fatalf("解析转发请求体失败: %v", err)
	}
	if payload.Model != "gemini-2.5-flash" || payload.Stream || len(payload.Messages) != 1 {
		t.Fatalf("转发请求体不正确: %+v", payload)
	}
	if payload.Messages[0].Role != "user" || payload.Messages[0].Content != "请只回复连接成功" {
		t.Fatalf("转发消息不正确: %+v", payload.Messages[0])
	}

	if len(events) != 3 {
		t.Fatalf("事件数量 = %d，期望 3，事件: %+v", len(events), events)
	}
	if events[0].Type != "test_start" || events[0].Model != "gemini-2.5-flash" {
		t.Fatalf("开始事件不正确: %+v", events[0])
	}
	if events[1].Type != "content" || events[1].Text != "连接成功" {
		t.Fatalf("内容事件不正确: %+v", events[1])
	}
	if events[2].Type != "test_complete" || !events[2].Success {
		t.Fatalf("完成事件不正确: %+v", events[2])
	}
}

func TestAntigravity连通性测试透传上游错误(t *testing.T) {
	repo := &antigravityTestRepo{item: Account{
		ID:       22,
		Name:     "Antigravity OAuth",
		Platform: "antigravity",
		Type:     TypeOAuth,
		Credentials: map[string]string{
			"access_token": "测试访问令牌",
		},
	}}
	forwarder := &antigravityTestForwarder{
		result: cpa.ForwardResult{
			StatusCode: http.StatusForbidden,
			Body:       []byte(`{"error":{"code":403,"message":"权限不足"}}`),
		},
	}
	service := NewService(repo, "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	service.SetOAuthCredentialRefresher(forwarder)

	var events []TestEvent
	err := service.TestConnection(
		context.Background(),
		repo.item.ID,
		"gemini-2.5-flash",
		"hi",
		TestOptions{},
		func(event TestEvent) { events = append(events, event) },
	)
	if err == nil {
		t.Fatal("上游返回 403 时测试应失败")
	}
	if !strings.Contains(err.Error(), "上游 HTTP 403") || !strings.Contains(err.Error(), "权限不足") {
		t.Fatalf("错误未包含上游状态和原文: %v", err)
	}
	if len(events) != 2 || events[0].Type != "test_start" || events[1].Type != "error" {
		t.Fatalf("错误事件序列不正确: %+v", events)
	}
	if !strings.Contains(events[1].Error, "权限不足") {
		t.Fatalf("错误事件未保留上游原文: %+v", events[1])
	}
}

type antigravityTestRepo struct {
	stubAccountRepo
	item Account
}

func (r *antigravityTestRepo) FindByID(context.Context, int, LoadOptions) (Account, error) {
	return r.item, nil
}

type antigravityTestForwarder struct {
	calls        int
	refreshCalls int
	request      cpa.ForwardRequest
	result       cpa.ForwardResult
}

func (f *antigravityTestForwarder) RefreshAccountCredentials(
	context.Context,
	int,
	string,
	string,
	string,
	map[string]string,
	string,
) (map[string]string, error) {
	f.refreshCalls++
	return nil, nil
}

func (f *antigravityTestForwarder) ForwardAccountTest(_ context.Context, request cpa.ForwardRequest) cpa.ForwardResult {
	f.calls++
	f.request = request
	return f.result
}
