package cpa

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
)

func TestBridge运行时注册无APIKey平台执行器(t *testing.T) {
	bridge := NewBridge(nil)
	defer func() { _ = bridge.Close(context.Background()) }()
	seedPaths := append([]string(nil), bridge.executorSeedPaths...)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		antigravity, hasAntigravity := bridge.Executor("antigravity")
		kimi, hasKimi := bridge.Executor("kimi")
		if hasAntigravity && antigravity != nil && hasKimi && kimi != nil && bridge.Ready() {
			for _, path := range seedPaths {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("executor 注册完成后占位凭证仍存在: %s", path)
				}
			}
			// 等待 watcher 消费占位文件删除事件，确认 executor 不会随临时 auth 一起消失。
			time.Sleep(500 * time.Millisecond)
			if executor, ok := bridge.Executor("antigravity"); !ok || executor == nil {
				t.Fatal("删除占位凭证后 antigravity executor 消失")
			}
			if executor, ok := bridge.Executor("kimi"); !ok || executor == nil {
				t.Fatal("删除占位凭证后 kimi executor 消失")
			}

			//nolint:staticcheck // CPA 通过固定字符串上下文键注入测试 RoundTripper。
			ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", http.RoundTripper(
				roundTripperFunc(func(request *http.Request) (*http.Response, error) {
					body := `{}`
					switch request.URL.Path {
					case "/token":
						body = `{"access_token":"真实执行器换出的访问令牌","expires_in":3599,"token_type":"Bearer"}`
					case "/v1internal:loadCodeAssist":
						body = `{"cloudaicompanionProject":"真实执行器发现的项目"}`
					case "/v1internal:generateContent":
						body = `{
							"response":{
								"candidates":[{"content":{"role":"model","parts":[{"text":"连接成功"}]}}],
								"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}
							}
						}`
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": []string{"application/json"}},
						Body:       io.NopCloser(strings.NewReader(body)),
						Request:    request,
					}, nil
				}),
			))
			credentials, err := bridge.ImportOAuthCredentials(
				ctx,
				"antigravity",
				"oauth",
				map[string]string{"refresh_token": "测试刷新令牌"},
				"",
			)
			if err != nil {
				t.Fatalf("真实 Antigravity executor RT 导入失败: %v", err)
			}
			if credentials["access_token"] != "真实执行器换出的访问令牌" || credentials["project_id"] != "真实执行器发现的项目" {
				t.Fatalf("真实 Antigravity executor 返回凭证不完整: %#v", credentials)
			}

			result := bridge.ForwardAccountTest(ctx, ForwardRequest{
				Account: AccountAuthInput{
					AccountID:   33,
					Name:        "Antigravity 测试账号",
					Platform:    "antigravity",
					Type:        "oauth",
					Credentials: credentials,
				},
				Model:         "gemini-2.5-flash",
				Endpoint:      adaptor.EndpointChatCompletions,
				EntryProtocol: registry.ProtocolOpenAI,
				Payload:       []byte(`{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"}],"stream":false}`),
			})
			if result.BuildErr != nil || result.NetErr != nil || result.StreamErr != nil || result.StatusCode != http.StatusOK {
				t.Fatalf("真实 Antigravity executor 连通性转发失败: status=%d build=%v net=%v stream=%v body=%s",
					result.StatusCode, result.BuildErr, result.NetErr, result.StreamErr, result.Body)
			}
			var response struct {
				Choices []struct {
					Message struct {
						Content string `json:"content"`
					} `json:"message"`
				} `json:"choices"`
			}
			if err := json.Unmarshal(result.Body, &response); err != nil {
				t.Fatalf("解析真实 Antigravity executor 转换结果失败: %v，响应: %s", err, result.Body)
			}
			if len(response.Choices) != 1 || response.Choices[0].Message.Content != "连接成功" {
				t.Fatalf("真实 Antigravity executor 未转换为 OpenAI 响应: %s", result.Body)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	providers := SupportedProviders()
	registered := make([]string, 0, len(providers))
	for _, provider := range providers {
		if executor, ok := bridge.Executor(provider); ok && executor != nil {
			registered = append(registered, provider)
		}
	}
	t.Fatalf("OAuth executor 未完整注册，当前已有: %v，初始化错误: %v", registered, bridge.InitError())
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
