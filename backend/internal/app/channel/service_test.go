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
	listAll             func(ctx context.Context) ([]Channel, error)
	findByID            func(ctx context.Context, id int) (Channel, error)
	findKeyByID         func(ctx context.Context, keyID int) (ChannelKey, error)
	updateKeyState      func(ctx context.Context, keyID int, status string, errMsg string) error
	updateKeyTestResult func(ctx context.Context, keyID int, responseTimeMs int, testedAt time.Time) error
	updateKeyBalance    func(ctx context.Context, keyID int, balance float64, updatedAt time.Time) error
}

func (s *stubRepo) List(context.Context, ListFilter) ([]Channel, int64, error) { return nil, 0, nil }
func (s *stubRepo) ListAll(ctx context.Context) ([]Channel, error) {
	if s.listAll == nil {
		return nil, nil
	}
	return s.listAll(ctx)
}
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
func (s *stubRepo) ListKeys(context.Context, KeyListFilter) ([]ChannelKey, int64, error) {
	return nil, 0, nil
}
func (s *stubRepo) FindKeyByID(ctx context.Context, keyID int) (ChannelKey, error) {
	if s.findKeyByID == nil {
		return ChannelKey{}, nil
	}
	return s.findKeyByID(ctx, keyID)
}
func (s *stubRepo) CreateKey(context.Context, int, KeyInput) (ChannelKey, error) {
	return ChannelKey{}, nil
}
func (s *stubRepo) UpdateKey(context.Context, int, KeyInput) (ChannelKey, error) {
	return ChannelKey{}, nil
}
func (s *stubRepo) DeleteKey(context.Context, int) error { return nil }
func (s *stubRepo) UpdateKeyState(ctx context.Context, keyID int, status string, errMsg string) error {
	if s.updateKeyState == nil {
		return nil
	}
	return s.updateKeyState(ctx, keyID, status, errMsg)
}
func (s *stubRepo) UpdateCredentialState(context.Context, int, string, string) error {
	return nil
}
func (s *stubRepo) UpdateKeyTestResult(ctx context.Context, keyID int, responseTimeMs int, testedAt time.Time) error {
	if s.updateKeyTestResult == nil {
		return nil
	}
	return s.updateKeyTestResult(ctx, keyID, responseTimeMs, testedAt)
}
func (s *stubRepo) UpdateKeyBalance(ctx context.Context, keyID int, balance float64, updatedAt time.Time) error {
	if s.updateKeyBalance == nil {
		return nil
	}
	return s.updateKeyBalance(ctx, keyID, balance, updatedAt)
}
func (s *stubRepo) UpdateKeyHealthState(_ context.Context, _ int, _ string, _, _ int) error {
	return nil
}
func (s *stubRepo) UpdateKeyProbeTime(_ context.Context, _ int, _ time.Time) error {
	return nil
}
func (s *stubRepo) ListProbeEnabledKeys(context.Context) ([]KeyHealthSnapshot, error) {
	return nil, nil
}
func (s *stubRepo) GetChannelKeyMoneyStats(_ context.Context, _ []int, _ time.Time) (map[int]MoneyStats, error) {
	return nil, nil
}
func (s *stubRepo) ListBalanceSyncTargets(_ context.Context, _ time.Time) ([]int, error) {
	return nil, nil
}
func (s *stubRepo) ListUpstreamRateTargets(context.Context) ([]UpstreamRateTarget, error) {
	return nil, nil
}
func (s *stubRepo) UpdateUpstreamRate(_ context.Context, _ int, _ float64, _ time.Time) error {
	return nil
}

// stubTester 恒成功的密钥端点测试器。
type stubTester struct{ latency int }

func (s stubTester) Test(context.Context, ChannelKey, string, string) (int, error) {
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
				findKeyByID: func(_ context.Context, keyID int) (ChannelKey, error) {
					findCalls++
					status := tc.statusAtStart
					if findCalls > 1 { // 恢复前的重读拿到最新状态
						status = tc.statusAtEnd
					}
					return ChannelKey{ID: keyID, Status: status, TestModel: "gpt-4o"}, nil
				},
				updateKeyState: func(_ context.Context, _ int, status string, _ string) error {
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

// TestFetchModelsDelegatesToFetcher fetch-models 解密指定 key 后委托拉取器。
func TestFetchModelsDelegatesToFetcher(t *testing.T) {
	// secret 须为 hex 且解码后 ≥32 字节（AES-256）。
	const secret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	encrypted, err := auth.EncryptAPIKey("sk-upstream", secret)
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}

	repo := &stubRepo{
		findKeyByID: func(_ context.Context, keyID int) (ChannelKey, error) {
			return ChannelKey{
				ID:      keyID,
				Type:    "openai_compatible",
				BaseURL: "https://api.example.com",
				APIKey:  encrypted,
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

// TestRefreshBalancePersistsForKey 刷新单把 key 的上游余额并落库。
func TestRefreshBalancePersistsForKey(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	k1, _ := auth.EncryptAPIKey("sk-1", secret)

	var persisted float64
	repo := &stubRepo{
		findKeyByID: func(_ context.Context, keyID int) (ChannelKey, error) {
			return ChannelKey{ID: keyID, Type: "openai_compatible", BaseURL: "https://x", APIKey: k1}, nil
		},
		updateKeyBalance: func(_ context.Context, _ int, balance float64, _ time.Time) error {
			persisted = balance
			return nil
		},
	}
	svc := NewService(repo, secret)
	fetcher := &stubFetcher{balance: 30}
	svc.fetcher = fetcher

	bal, updatedAt, err := svc.RefreshBalance(context.Background(), 1)
	if err != nil {
		t.Fatalf("RefreshBalance err = %v", err)
	}
	if bal != 30 {
		t.Errorf("balance = %v, want 30", bal)
	}
	if len(fetcher.balanceKeys) != 1 || fetcher.balanceKeys[0] != "sk-1" {
		t.Errorf("queried keys = %v, want [sk-1]", fetcher.balanceKeys)
	}
	if persisted != 30 {
		t.Errorf("persisted = %v, want 30", persisted)
	}
	if updatedAt == nil {
		t.Error("updatedAt nil")
	}
}

// TestLoadAllForRegistryRequiresBothRateSwitches 验证关闭探测后，历史探测值不会继续覆盖成本倍率。
func TestLoadAllForRegistryRequiresBothRateSwitches(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	encrypted, err := auth.EncryptAPIKey("sk-upstream", secret)
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}

	repo := &stubRepo{listAll: func(context.Context) ([]Channel, error) {
		return []Channel{{
			ID: 1,
			Keys: []ChannelKey{
				{ID: 1, APIKey: encrypted, UpstreamRateEnabled: true, UseUpstreamRateForCost: true},
				{ID: 2, APIKey: encrypted, UpstreamRateEnabled: false, UseUpstreamRateForCost: true},
			},
		}}, nil
	}}

	snaps, err := NewService(repo, secret).LoadAllForRegistry(context.Background())
	if err != nil {
		t.Fatalf("LoadAllForRegistry error: %v", err)
	}
	if len(snaps) != 2 {
		t.Fatalf("快照数量 = %d，期望 2", len(snaps))
	}
	if !snaps[0].UseUpstreamRateForCost {
		t.Fatal("两个开关均开启时应允许探测倍率计入成本")
	}
	if snaps[1].UseUpstreamRateForCost {
		t.Fatal("探测关闭后不应允许历史探测倍率计入成本")
	}
}

// TestAddKeyAllowsEmptyGroups 新增 key 允许不绑定分组（未绑定分组的 key 不会被任何分组调度到，非法状态）。
func TestAddKeyAllowsEmptyGroups(t *testing.T) {
	svc := NewService(&stubRepo{}, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")

	if _, err := svc.AddKey(context.Background(), 1, KeyInput{Type: "openai_compatible", APIKey: "sk-1", GroupIDs: nil}); err != nil {
		t.Errorf("GroupIDs=nil: err = %v, want nil", err)
	}
	if _, err := svc.AddKey(context.Background(), 1, KeyInput{Type: "openai_compatible", APIKey: "sk-1", GroupIDs: []int{}}); err != nil {
		t.Errorf("GroupIDs=[]: err = %v, want nil", err)
	}
	if _, err := svc.AddKey(context.Background(), 1, KeyInput{Type: "openai_compatible", APIKey: "sk-1", GroupIDs: []int{1}}); err != nil {
		t.Errorf("GroupIDs=[1]: err = %v, want nil", err)
	}
}

// TestAddKeyRequiresProtocol 确保绕过 HTTP 层调用服务时也不会创建无协议端点的孤儿凭证。
func TestAddKeyRequiresProtocol(t *testing.T) {
	svc := NewService(&stubRepo{}, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")

	if _, err := svc.AddKey(context.Background(), 1, KeyInput{APIKey: "sk-1"}); !errors.Is(err, ErrInvalidProtocol) {
		t.Fatalf("err = %v，期望 ErrInvalidProtocol", err)
	}
}

// TestAddKeyRejectsRemovedCustomProtocol 确保已移除的 custom 类型不能通过服务层旧接口继续写入。
func TestAddKeyRejectsRemovedCustomProtocol(t *testing.T) {
	svc := NewService(&stubRepo{}, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")

	if _, err := svc.AddKey(context.Background(), 1, KeyInput{Type: "custom", APIKey: "sk-1"}); !errors.Is(err, ErrInvalidProtocol) {
		t.Fatalf("err = %v，期望 ErrInvalidProtocol", err)
	}
}

// TestUpdateKeyAllowsEmptyGroups 更新 key 时：GroupIDs 显式传空（解绑全部分组）与 nil（不改动分组）均放行。
func TestUpdateKeyAllowsEmptyGroups(t *testing.T) {
	svc := NewService(&stubRepo{}, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")

	if _, err := svc.UpdateKey(context.Background(), 1, KeyInput{GroupIDs: []int{}}); err != nil {
		t.Errorf("GroupIDs=[]: err = %v, want nil", err)
	}
	if _, err := svc.UpdateKey(context.Background(), 1, KeyInput{GroupIDs: nil}); err != nil {
		t.Errorf("GroupIDs=nil（不改动分组）: err = %v, want nil", err)
	}
	if _, err := svc.UpdateKey(context.Background(), 1, KeyInput{GroupIDs: []int{2}}); err != nil {
		t.Errorf("GroupIDs=[2]: err = %v, want nil", err)
	}
}
