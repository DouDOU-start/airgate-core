package channel

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/auth"
)

// stubRepo 渠道仓储替身：按需覆盖各方法，未覆盖返回零值。
type stubRepo struct {
	findByID         func(ctx context.Context, id int) (Channel, error)
	updateState      func(ctx context.Context, id int, status string, errMsg string) error
	updateTestResult func(ctx context.Context, id int, responseTimeMs int, testedAt time.Time) error
	updateBalance    func(ctx context.Context, id int, balance float64, updatedAt time.Time) error
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
func (s *stubRepo) UpdateState(ctx context.Context, id int, status string, errMsg string) error {
	if s.updateState == nil {
		return nil
	}
	return s.updateState(ctx, id, status, errMsg)
}
func (s *stubRepo) UpdateTestResult(ctx context.Context, id int, responseTimeMs int, testedAt time.Time) error {
	if s.updateTestResult == nil {
		return nil
	}
	return s.updateTestResult(ctx, id, responseTimeMs, testedAt)
}
func (s *stubRepo) UpdateBalance(ctx context.Context, id int, balance float64, updatedAt time.Time) error {
	if s.updateBalance == nil {
		return nil
	}
	return s.updateBalance(ctx, id, balance, updatedAt)
}

// stubTester 恒成功的渠道测试器。
type stubTester struct{ latency int }

func (s stubTester) Test(context.Context, Channel, string, string) (int, error) {
	return s.latency, nil
}

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
				updateState: func(_ context.Context, _ int, status string, _ string) error {
					if status != StatusEnabled {
						t.Errorf("恢复状态 = %q, want enabled", status)
					}
					recovered = true
					return nil
				},
			}
			svc := NewService(repo, "test-secret-test-secret-test-32b")
			svc.SetTester(stubTester{latency: 12})

			latency, err := svc.Test(context.Background(), 1, "gpt-4o", "")
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
	gotBaseURL  string
	gotAPIKey   string
	models      []string
	balance     float64
	balanceErr  error
	balanceKeys []string // 每次 FetchBalance 收到的 key（验证多 key 求和）
}

func (s *stubFetcher) FetchModels(_ context.Context, _, baseURL, apiKey string) ([]string, error) {
	s.gotBaseURL = baseURL
	s.gotAPIKey = apiKey
	return s.models, nil
}

func (s *stubFetcher) FetchBalance(_ context.Context, _, _, apiKey string) (float64, error) {
	s.balanceKeys = append(s.balanceKeys, apiKey)
	if s.balanceErr != nil {
		return 0, s.balanceErr
	}
	return s.balance, nil
}

// TestFetchModelsDelegatesToFetcher fetch-models 解密渠道首个 API Key 后委托拉取器。
func TestFetchModelsDelegatesToFetcher(t *testing.T) {
	// secret 须为 hex 且解码后 ≥32 字节（AES-256）。
	const secret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	encrypted, err := auth.EncryptAPIKey("sk-upstream", secret)
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}

	repo := &stubRepo{
		findByID: func(_ context.Context, id int) (Channel, error) {
			return Channel{
				ID:      id,
				Type:    "openai_compatible",
				BaseURL: "https://api.example.com",
				APIKeys: []string{encrypted},
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
	if fetcher.gotBaseURL != "https://api.example.com" {
		t.Errorf("baseURL = %q, want %q", fetcher.gotBaseURL, "https://api.example.com")
	}
	if fetcher.gotAPIKey != "sk-upstream" {
		t.Errorf("apiKey = %q, want 解密后的明文", fetcher.gotAPIKey)
	}
}

// TestRefreshBalanceSumsAcrossKeys 多 key 渠道余额求和并落库。
func TestRefreshBalanceSumsAcrossKeys(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	k1, _ := auth.EncryptAPIKey("sk-1", secret)
	k2, _ := auth.EncryptAPIKey("sk-2", secret)

	var persisted float64
	repo := &stubRepo{
		findByID: func(_ context.Context, id int) (Channel, error) {
			return Channel{ID: id, Type: "openai_compatible", BaseURL: "https://x", APIKeys: []string{k1, k2}}, nil
		},
		updateBalance: func(_ context.Context, _ int, balance float64, _ time.Time) error {
			persisted = balance
			return nil
		},
	}
	svc := NewService(repo, secret)
	fetcher := &stubFetcher{balance: 30} // 每 key $30
	svc.fetcher = fetcher

	bal, updatedAt, err := svc.RefreshBalance(context.Background(), 1)
	if err != nil {
		t.Fatalf("RefreshBalance err = %v", err)
	}
	if bal != 60 {
		t.Errorf("balance = %v, want 60 (2 keys × 30)", bal)
	}
	if len(fetcher.balanceKeys) != 2 {
		t.Errorf("queried %d keys, want 2", len(fetcher.balanceKeys))
	}
	if persisted != 60 {
		t.Errorf("persisted = %v, want 60", persisted)
	}
	if updatedAt == nil {
		t.Error("updatedAt nil")
	}
}

// TestRefreshBalanceUnsupportedShortCircuits 不支持类型直接透传 ErrBalanceUnsupported。
func TestRefreshBalanceUnsupportedShortCircuits(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	k1, _ := auth.EncryptAPIKey("sk-1", secret)
	repo := &stubRepo{
		findByID: func(_ context.Context, id int) (Channel, error) {
			return Channel{ID: id, Type: "anthropic", BaseURL: "https://x", APIKeys: []string{k1}}, nil
		},
	}
	svc := NewService(repo, secret)
	svc.fetcher = &stubFetcher{balanceErr: ErrBalanceUnsupported}

	if _, _, err := svc.RefreshBalance(context.Background(), 1); !errors.Is(err, ErrBalanceUnsupported) {
		t.Errorf("err = %v, want ErrBalanceUnsupported", err)
	}
}
