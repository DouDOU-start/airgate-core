package account

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/cpa"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
)

func TestAntigravity连通性测试通过CPA转发(t *testing.T) {
	server := newAntigravityModelsServer(t, []string{"gemini-3.6-flash-high"})
	defer server.Close()
	repo := &antigravityTestRepo{item: Account{
		ID:       21,
		Name:     "Antigravity OAuth",
		Platform: "antigravity",
		Type:     TypeOAuth,
		Credentials: map[string]string{
			"access_token":  "测试访问令牌",
			"refresh_token": "测试刷新令牌",
			"project_id":    "测试项目",
			"base_url":      server.URL,
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
		"gemini-3.6-flash-high",
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
	if request.Model != "gemini-3.6-flash-high" || request.Endpoint != adaptor.EndpointChatCompletions {
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
	if payload.Model != "gemini-3.6-flash-high" || payload.Stream || len(payload.Messages) != 1 {
		t.Fatalf("转发请求体不正确: %+v", payload)
	}
	if payload.Messages[0].Role != "user" || payload.Messages[0].Content != "请只回复连接成功" {
		t.Fatalf("转发消息不正确: %+v", payload.Messages[0])
	}

	if len(events) != 3 {
		t.Fatalf("事件数量 = %d，期望 3，事件: %+v", len(events), events)
	}
	if events[0].Type != "test_start" || events[0].Model != "gemini-3.6-flash-high" {
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
	server := newAntigravityModelsServer(t, []string{"gemini-3.6-flash-high"})
	defer server.Close()
	repo := &antigravityTestRepo{item: Account{
		ID:       22,
		Name:     "Antigravity OAuth",
		Platform: "antigravity",
		Type:     TypeOAuth,
		Credentials: map[string]string{
			"access_token": "测试访问令牌",
			"base_url":     server.URL,
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
		"gemini-3.6-flash-high",
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

func TestAntigravity可测模型优先使用账号实时目录(t *testing.T) {
	server := newAntigravityModelsServer(t, []string{"gemini-3.6-flash-high", "claude-sonnet-4-6", "chat_20706"})
	defer server.Close()
	repo := &antigravityTestRepo{item: Account{
		ID:       23,
		Platform: "antigravity",
		Type:     TypeOAuth,
		Credentials: map[string]string{
			"access_token": "测试访问令牌",
			"project_id":   "测试项目",
			"base_url":     server.URL,
		},
	}}
	service := NewService(repo, "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")

	models, err := service.AvailableTestModels(context.Background(), repo.item.ID)
	if err != nil {
		t.Fatalf("查询 Antigravity 实时模型失败: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("实时模型数量 = %d，模型: %+v", len(models), models)
	}
	for _, model := range models {
		if model.ID == "gemini-3.7-flash-high" {
			t.Fatalf("账号未开放的 3.7 模型不应出现在测试列表: %+v", models)
		}
		if model.ID == "chat_20706" {
			t.Fatalf("Antigravity 内部模型不应出现在测试列表: %+v", models)
		}
	}
}

func TestAntigravity实时目录过滤全部内部模型(t *testing.T) {
	server := newAntigravityModelsServer(t, []string{
		"chat_20706",
		"chat_23310",
		"tab_flash_lite_preview",
		"tab_jump_flash_lite_preview",
		"gemini-2.5-flash-thinking",
		"gemini-2.5-pro",
		"gemini-3.6-flash-high",
	})
	defer server.Close()

	models, err := fetchAntigravityAvailableModels(context.Background(), map[string]string{
		"access_token": "测试访问令牌",
		"project_id":   "测试项目",
		"base_url":     server.URL,
	}, "")
	if err != nil {
		t.Fatalf("查询 Antigravity 实时模型失败: %v", err)
	}
	if len(models) != 1 || models[0].ID != "gemini-3.6-flash-high" {
		t.Fatalf("内部模型过滤结果不正确: %+v", models)
	}
}

func TestAntigravity拒绝测试账号未开放模型(t *testing.T) {
	server := newAntigravityModelsServer(t, []string{"gemini-3.6-flash-high"})
	defer server.Close()
	repo := &antigravityTestRepo{item: Account{
		ID:       24,
		Platform: "antigravity",
		Type:     TypeOAuth,
		Credentials: map[string]string{
			"access_token": "测试访问令牌",
			"project_id":   "测试项目",
			"base_url":     server.URL,
		},
	}}
	forwarder := &antigravityTestForwarder{}
	service := NewService(repo, "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	service.SetOAuthCredentialRefresher(forwarder)

	err := service.TestConnection(
		context.Background(),
		repo.item.ID,
		"gemini-3.7-flash-high",
		"hi",
		TestOptions{},
		func(TestEvent) {},
	)
	if err == nil || !strings.Contains(err.Error(), "当前 Antigravity 账号未开放模型 gemini-3.7-flash-high") {
		t.Fatalf("未返回明确的模型不可用错误: %v", err)
	}
	if forwarder.calls != 0 {
		t.Fatalf("模型未开放时不应调用 CPA，实际调用 %d 次", forwarder.calls)
	}
}

func TestAntigravity未指定模型时从实时目录选择默认模型(t *testing.T) {
	server := newAntigravityModelsServer(t, []string{"claude-sonnet-4-6", "gemini-3.7-flash-high"})
	defer server.Close()
	repo := &antigravityTestRepo{item: Account{
		ID:       25,
		Platform: "antigravity",
		Type:     TypeOAuth,
		Credentials: map[string]string{
			"access_token": "测试访问令牌",
			"project_id":   "测试项目",
			"base_url":     server.URL,
		},
	}}
	forwarder := &antigravityTestForwarder{result: cpa.ForwardResult{
		StatusCode: http.StatusOK,
		Body:       []byte(`{"choices":[{"message":{"content":"连接成功"}}]}`),
	}}
	service := NewService(repo, "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	service.SetOAuthCredentialRefresher(forwarder)

	err := service.TestConnection(context.Background(), repo.item.ID, "", "hi", TestOptions{}, func(TestEvent) {})
	if err != nil {
		t.Fatalf("未指定模型时测试失败: %v", err)
	}
	if forwarder.request.Model != "gemini-3.7-flash-high" {
		t.Fatalf("默认模型 = %q，期望使用实时目录中的 Flash 模型", forwarder.request.Model)
	}
}

func TestAntigravity测试模型过滤Tiered并保留标准37模型(t *testing.T) {
	server := newAntigravityModelsServer(t, []string{"gemini-3.7-flash-tiered", "gemini-3.7-flash-high"})
	defer server.Close()
	repo := &antigravityTestRepo{item: Account{
		ID:       26,
		Platform: "antigravity",
		Type:     TypeOAuth,
		Credentials: map[string]string{
			"access_token": "测试访问令牌",
			"base_url":     server.URL,
		},
	}}
	service := NewService(repo, "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")

	models, err := service.AvailableTestModels(context.Background(), repo.item.ID)
	if err != nil {
		t.Fatalf("查询 Antigravity 测试模型失败: %v", err)
	}
	if len(models) != 1 || models[0].ID != "gemini-3.7-flash-high" {
		t.Fatalf("测试模型未按 CPA 规范过滤: %+v", models)
	}
}

func TestAntigravity测试连接拒绝Tiered模型(t *testing.T) {
	server := newAntigravityModelsServer(t, []string{"gemini-3.7-flash-tiered"})
	defer server.Close()
	repo := &antigravityTestRepo{item: Account{
		ID:       27,
		Platform: "antigravity",
		Type:     TypeOAuth,
		Credentials: map[string]string{
			"access_token": "测试访问令牌",
			"base_url":     server.URL,
		},
	}}
	forwarder := &antigravityTestForwarder{}
	service := NewService(repo, "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	service.SetOAuthCredentialRefresher(forwarder)

	err := service.TestConnection(context.Background(), repo.item.ID, "gemini-3.7-flash-tiered", "hi", TestOptions{}, func(TestEvent) {})
	if err == nil || !strings.Contains(err.Error(), "不在 Antigravity CPA 标准模型目录中") {
		t.Fatalf("tiered 模型未被拒绝: %v", err)
	}
	if forwarder.calls != 0 {
		t.Fatalf("tiered 模型不应调用 CPA，实际调用 %d 次", forwarder.calls)
	}
}

func TestAntigravity实时目录失败时白名单仍过滤Tiered(t *testing.T) {
	repo := &antigravityTestRepo{item: Account{
		ID:       28,
		Platform: "antigravity",
		Type:     TypeOAuth,
		Extra: map[string]any{
			"models": []any{"gemini-3.7-flash-tiered", "gemini-3.7-flash-high"},
		},
		Credentials: map[string]string{
			"access_token": "测试访问令牌",
			"base_url":     "http://127.0.0.1:1",
		},
	}}
	service := NewService(repo, "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")

	models, err := service.AvailableTestModels(context.Background(), repo.item.ID)
	if err != nil {
		t.Fatalf("查询回退测试模型失败: %v", err)
	}
	if len(models) != 1 || models[0].ID != "gemini-3.7-flash-high" {
		t.Fatalf("实时目录失败时仍透传 tiered: %+v", models)
	}
}

func TestAntigravity上游实体不存在时返回明确诊断(t *testing.T) {
	message := accountTestForwardError(cpa.ForwardResult{
		StatusCode: http.StatusNotFound,
		Body:       []byte(`{"error":{"code":404,"message":"Requested entity was not found.","status":"NOT_FOUND"}}`),
	})
	if !strings.Contains(message, "未开放所选模型") || !strings.Contains(message, "project_id 已失效") {
		t.Fatalf("404 诊断信息不明确: %q", message)
	}
}

func newAntigravityModelsServer(t *testing.T, modelIDs []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != antigravityModelsPath {
			t.Errorf("模型目录请求路径 = %q", r.URL.Path)
			http.Error(w, "请求路径错误", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer 测试访问令牌" {
			t.Errorf("模型目录请求缺少正确认证头")
			http.Error(w, "认证头错误", http.StatusUnauthorized)
			return
		}
		models := make(map[string]map[string]any, len(modelIDs))
		for _, id := range modelIDs {
			models[id] = map[string]any{"displayName": id}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"models": models})
	}))
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
