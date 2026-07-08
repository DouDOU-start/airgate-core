package channel

import (
	"context"
	"testing"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/auth"
)

// stubRepo 渠道仓储替身：按需覆盖各方法，未覆盖返回零值。
type stubRepo struct {
	findByID         func(ctx context.Context, id int) (Channel, error)
	updateState      func(ctx context.Context, id int, status string, until *time.Time, errMsg string) error
	updateTestResult func(ctx context.Context, id int, responseTimeMs int, testedAt time.Time) error
}

func (s *stubRepo) List(context.Context, ListFilter) ([]Channel, int64, error) { return nil, 0, nil }
func (s *stubRepo) ListAll(context.Context) ([]Channel, error)                 { return nil, nil }
func (s *stubRepo) FindByID(ctx context.Context, id int) (Channel, error) {
	if s.findByID == nil {
		return Channel{}, nil
	}
	return s.findByID(ctx, id)
}
func (s *stubRepo) Create(context.Context, CreateInput) (Channel, error)      { return Channel{}, nil }
func (s *stubRepo) Update(context.Context, int, UpdateInput) (Channel, error) { return Channel{}, nil }
func (s *stubRepo) Delete(context.Context, int) error                         { return nil }
func (s *stubRepo) BulkUpdate(context.Context, BulkUpdateInput) (int, error)  { return 0, nil }
func (s *stubRepo) UpdateState(ctx context.Context, id int, status string, until *time.Time, errMsg string) error {
	if s.updateState == nil {
		return nil
	}
	return s.updateState(ctx, id, status, until, errMsg)
}
func (s *stubRepo) UpdateTestResult(ctx context.Context, id int, responseTimeMs int, testedAt time.Time) error {
	if s.updateTestResult == nil {
		return nil
	}
	return s.updateTestResult(ctx, id, responseTimeMs, testedAt)
}

// stubTester 恒成功的渠道测试器。
type stubTester struct{ latency int }

func (s stubTester) Test(context.Context, Channel, string) (int, error) { return s.latency, nil }

// TestTestRecoverRereadsCurrentStatus 测试成功恢复前重读当前状态：
// 30s 测试窗口内管理员改为手动禁用时，不得凭测前快照恢复覆盖手动操作。
func TestTestRecoverRereadsCurrentStatus(t *testing.T) {
	cases := []struct {
		name          string
		statusAtStart string
		statusAtEnd   string
		wantRecover   bool
	}{
		{
			name:          "测试期间被改为手动禁用：不恢复",
			statusAtStart: StatusDisabledAuto,
			statusAtEnd:   StatusDisabledManual,
			wantRecover:   false,
		},
		{
			name:          "仍为自动禁用：恢复 enabled",
			statusAtStart: StatusDisabledAuto,
			statusAtEnd:   StatusDisabledAuto,
			wantRecover:   true,
		},
		{
			name:          "测前即手动禁用：不触发恢复分支",
			statusAtStart: StatusDisabledManual,
			statusAtEnd:   StatusDisabledManual,
			wantRecover:   false,
		},
		{
			name:          "已启用：不触发恢复分支",
			statusAtStart: StatusEnabled,
			statusAtEnd:   StatusEnabled,
			wantRecover:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findCalls := 0
			recovered := false
			repo := &stubRepo{
				findByID: func(_ context.Context, id int) (Channel, error) {
					findCalls++
					status := tc.statusAtStart
					if findCalls > 1 { // 恢复前的重读拿到最新状态
						status = tc.statusAtEnd
					}
					return Channel{ID: id, Status: status, TestModel: "gpt-4o"}, nil
				},
				updateState: func(_ context.Context, _ int, status string, _ *time.Time, _ string) error {
					if status != StatusEnabled {
						t.Errorf("恢复状态 = %q, want enabled", status)
					}
					recovered = true
					return nil
				},
			}
			svc := NewService(repo, "test-secret-test-secret-test-32b")
			svc.SetTester(stubTester{latency: 12})

			latency, err := svc.Test(context.Background(), 1, "gpt-4o")
			if err != nil {
				t.Fatalf("Test err = %v", err)
			}
			if latency != 12 {
				t.Errorf("latency = %d, want 12", latency)
			}
			if recovered != tc.wantRecover {
				t.Errorf("recovered = %v, want %v", recovered, tc.wantRecover)
			}
		})
	}
}

// stubFetcher 记录收到的参数。
type stubFetcher struct {
	gotProxyURL string
	gotBaseURL  string
	models      []string
}

func (s *stubFetcher) FetchModels(_ context.Context, _, baseURL, _, proxyURL string) ([]string, error) {
	s.gotBaseURL = baseURL
	s.gotProxyURL = proxyURL
	return s.models, nil
}

// TestFetchModelsUsesChannelProxy fetch-models 走渠道绑定的出口代理（与转发链路一致）。
func TestFetchModelsUsesChannelProxy(t *testing.T) {
	// secret 须为 hex 且解码后 ≥32 字节（AES-256）。
	const secret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	encrypted, err := auth.EncryptAPIKey("sk-upstream", secret)
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}

	cases := []struct {
		name         string
		proxy        *ProxyInfo
		wantProxyURL string
	}{
		{
			name:         "绑定 socks5 代理",
			proxy:        &ProxyInfo{Protocol: "socks5", Address: "10.0.0.1", Port: 1080, Username: "u", Password: "p"},
			wantProxyURL: "socks5://u:p@10.0.0.1:1080",
		},
		{
			name:         "未绑定代理直连",
			proxy:        nil,
			wantProxyURL: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &stubRepo{
				findByID: func(_ context.Context, id int) (Channel, error) {
					return Channel{
						ID:      id,
						Type:    "openai_compatible",
						BaseURL: "https://api.example.com",
						APIKeys: []string{encrypted},
						Proxy:   tc.proxy,
					}, nil
				},
			}
			svc := NewService(repo, secret)
			fetcher := &stubFetcher{models: []string{"gpt-4o"}}
			svc.fetcher = fetcher

			models, err := svc.FetchModels(context.Background(), 1)
			if err != nil {
				t.Fatalf("FetchModels err = %v", err)
			}
			if len(models) != 1 || models[0] != "gpt-4o" {
				t.Errorf("models = %v", models)
			}
			if fetcher.gotProxyURL != tc.wantProxyURL {
				t.Errorf("proxyURL = %q, want %q", fetcher.gotProxyURL, tc.wantProxyURL)
			}
		})
	}
}
