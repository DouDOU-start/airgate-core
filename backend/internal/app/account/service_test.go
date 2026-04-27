package account

import (
	"context"
	"testing"
	"time"
)

func TestImportIgnoresEnvironmentScopedIDs(t *testing.T) {
	service := NewService(stubRepository{
		create: func(_ context.Context, input CreateInput) (Account, error) {
			if len(input.GroupIDs) != 0 {
				t.Fatalf("expected import to clear group IDs, got %v", input.GroupIDs)
			}
			if input.ProxyID != nil {
				t.Fatalf("expected import to clear proxy ID, got %v", *input.ProxyID)
			}
			return Account{ID: 1, Name: input.Name}, nil
		},
	}, nil, nil, nil)

	proxyID := int64(99)
	summary := service.Import(t.Context(), []CreateInput{{
		Name:           "demo",
		Platform:       "openai",
		Type:           "apikey",
		Credentials:    map[string]string{"api_key": "secret"},
		Priority:       3,
		MaxConcurrency: 5,
		RateMultiplier: 1.2,
		GroupIDs:       []int64{2, 1},
		ProxyID:        &proxyID,
	}})

	if summary.Imported != 1 || summary.Failed != 0 {
		t.Fatalf("unexpected import summary: %+v", summary)
	}
}

type stubRepository struct {
	create func(context.Context, CreateInput) (Account, error)
}

func (s stubRepository) List(context.Context, ListFilter) ([]Account, int64, error) {
	return nil, 0, nil
}

func (s stubRepository) ListAll(context.Context, ListFilter) ([]Account, error) {
	return nil, nil
}

func (s stubRepository) Create(ctx context.Context, input CreateInput) (Account, error) {
	if s.create == nil {
		return Account{}, nil
	}
	return s.create(ctx, input)
}

func (s stubRepository) Update(context.Context, int, UpdateInput) (Account, error) {
	return Account{}, nil
}

func (s stubRepository) Delete(context.Context, int) error { return nil }

func (s stubRepository) FindByID(context.Context, int, LoadOptions) (Account, error) {
	return Account{}, nil
}

func (s stubRepository) ListByPlatform(context.Context, string) ([]Account, error) {
	return nil, nil
}

func (s stubRepository) FindUsageLogs(context.Context, int, time.Time, time.Time) ([]UsageLog, error) {
	return nil, nil
}

func (s stubRepository) BatchWindowStats(context.Context, []int, time.Time) (map[int]AccountWindowStats, error) {
	return nil, nil
}

func (s stubRepository) BatchImageStats(context.Context, []int, time.Time) (map[int]AccountImageStats, error) {
	return nil, nil
}

func (s stubRepository) SaveCredentials(context.Context, int, map[string]string) error { return nil }

// stubStateWriter 捕获 StateWriter 调用。
type stubStateWriter struct {
	rateLimited map[int]*time.Time
	cleared     map[int]bool
	disabled    map[int]string
}

func newStubStateWriter() *stubStateWriter {
	return &stubStateWriter{
		rateLimited: map[int]*time.Time{},
		cleared:     map[int]bool{},
		disabled:    map[int]string{},
	}
}

func (s *stubStateWriter) MarkRateLimited(_ context.Context, accountID int, until time.Time, _ string) {
	cp := until
	s.rateLimited[accountID] = &cp
}

func (s *stubStateWriter) ClearRateLimited(_ context.Context, accountID int) {
	s.cleared[accountID] = true
}

func (s *stubStateWriter) MarkDisabled(_ context.Context, accountID int, reason string) {
	s.disabled[accountID] = reason
}

type windowStatsStub struct {
	stubRepository
	captured [][]int
	byStart  map[int64]map[int]AccountWindowStats
}

func (s *windowStatsStub) BatchWindowStats(_ context.Context, ids []int, startTime time.Time) (map[int]AccountWindowStats, error) {
	cp := append([]int(nil), ids...)
	s.captured = append(s.captured, cp)
	if s.byStart == nil {
		return nil, nil
	}
	return s.byStart[startTime.Unix()], nil
}

func TestEnrichTodayStats_AttachesAccountLevelStats(t *testing.T) {
	// 2026-04-14 15:30 本地时间 → 今日 00:00 = 2026-04-14 00:00
	now := time.Date(2026, 4, 14, 15, 30, 0, 0, time.Local)
	todayStart := time.Date(2026, 4, 14, 0, 0, 0, 0, time.Local).Unix()

	repo := &windowStatsStub{
		byStart: map[int64]map[int]AccountWindowStats{
			todayStart: {
				42: {Requests: 9, Tokens: 242_500, AccountCost: 0.22, UserCost: 0.13},
			},
		},
	}
	svc := NewService(repo, nil, nil, nil)
	svc.now = func() time.Time { return now }

	// 上游 quota 窗口不影响 today_stats，今日统计是账号级的
	merged := map[string]any{
		"42": map[string]any{
			"windows": []any{
				map[string]any{"key": "5h", "label": "5h", "used_percent": 19.0},
				map[string]any{"key": "7d", "label": "7d", "used_percent": 100.0},
				map[string]any{"key": "5h_spark", "label": "5h Spark", "used_percent": 0.0},
				map[string]any{"key": "7d_spark", "label": "7d Spark", "used_percent": 14.0},
			},
		},
	}
	svc.enrichTodayStats(t.Context(), merged)

	acct := merged["42"].(map[string]any)
	stats, ok := acct["today_stats"].(map[string]any)
	if !ok {
		t.Fatalf("account should have today_stats attached at top level")
	}
	if stats["requests"].(int64) != 9 {
		t.Errorf("requests = %v, want 9", stats["requests"])
	}
	if stats["tokens"].(int64) != 242_500 {
		t.Errorf("tokens = %v, want 242500", stats["tokens"])
	}
	if stats["account_cost"].(float64) != 0.22 {
		t.Errorf("account_cost = %v, want 0.22", stats["account_cost"])
	}
	if stats["user_cost"].(float64) != 0.13 {
		t.Errorf("user_cost = %v, want 0.13", stats["user_cost"])
	}

	// windows 不应该被打上 stats 字段
	windows := acct["windows"].([]any)
	for i, wAny := range windows {
		w := wAny.(map[string]any)
		if _, hasStats := w["stats"]; hasStats {
			t.Errorf("window %d should NOT have stats attached (today_stats lives at account level)", i)
		}
	}
}

func TestEnrichTodayStats_ApikeyPlaceholderGetsStats(t *testing.T) {
	// 回归：apikey 账号在 merged 里只有一个空 map 占位（getUpstreamUsage 里 seed 的），
	// enrichTodayStats 应该能给它填上 today_stats——不能因为没有 windows 就跳过
	now := time.Date(2026, 4, 14, 15, 30, 0, 0, time.Local)
	todayStart := time.Date(2026, 4, 14, 0, 0, 0, 0, time.Local).Unix()

	repo := &windowStatsStub{
		byStart: map[int64]map[int]AccountWindowStats{
			todayStart: {
				55: {Requests: 3, Tokens: 1200, AccountCost: 0.05, UserCost: 0.02},
			},
		},
	}
	svc := NewService(repo, nil, nil, nil)
	svc.now = func() time.Time { return now }

	// 模拟 getUpstreamUsage seed 之后的状态：apikey 账号只有一个空 map
	merged := map[string]any{
		"55": map[string]any{}, // apikey 占位
	}
	svc.enrichTodayStats(t.Context(), merged)

	acct := merged["55"].(map[string]any)
	stats, ok := acct["today_stats"].(map[string]any)
	if !ok {
		t.Fatalf("apikey placeholder account should get today_stats attached")
	}
	if stats["requests"].(int64) != 3 {
		t.Errorf("requests = %v, want 3", stats["requests"])
	}
	if stats["user_cost"].(float64) != 0.02 {
		t.Errorf("user_cost = %v, want 0.02", stats["user_cost"])
	}
}

func TestEnrichTodayStats_ZeroWhenNoRecords(t *testing.T) {
	// 账号今天完全没有请求 → 仍然注入 0 值，前端据此稳定展示
	now := time.Date(2026, 4, 14, 15, 30, 0, 0, time.Local)

	repo := &windowStatsStub{byStart: map[int64]map[int]AccountWindowStats{}}
	svc := NewService(repo, nil, nil, nil)
	svc.now = func() time.Time { return now }

	merged := map[string]any{
		"99": map[string]any{
			"windows": []any{
				map[string]any{"key": "5h", "label": "5h", "used_percent": 0.0},
			},
		},
	}
	svc.enrichTodayStats(t.Context(), merged)

	stats := merged["99"].(map[string]any)["today_stats"].(map[string]any)
	if stats["requests"].(int64) != 0 {
		t.Errorf("requests = %v, want 0", stats["requests"])
	}
	if stats["account_cost"].(float64) != 0 {
		t.Errorf("account_cost = %v, want 0", stats["account_cost"])
	}
}

func TestCloneMergedShallow_IsolatesCachedEntry(t *testing.T) {
	// 回归测试：克隆体写 today_stats 不能污染缓存里的原始 map
	cached := map[string]any{
		"42": map[string]any{
			"windows": []any{map[string]any{"key": "5h"}},
		},
	}
	clone := cloneMergedShallow(cached)
	cloneAcct := clone["42"].(map[string]any)
	cloneAcct["today_stats"] = map[string]any{"requests": int64(99)}

	// 缓存里的 account map 不应该出现 today_stats
	origAcct := cached["42"].(map[string]any)
	if _, leaked := origAcct["today_stats"]; leaked {
		t.Fatalf("today_stats leaked into cached map — cloneMergedShallow is not deep enough")
	}
}

func TestEnrichTodayStats_BatchesAllAccountsInOneQuery(t *testing.T) {
	// 多个账号应该在一次 BatchWindowStats 调用里一起查
	now := time.Date(2026, 4, 14, 15, 30, 0, 0, time.Local)
	todayStart := time.Date(2026, 4, 14, 0, 0, 0, 0, time.Local).Unix()

	repo := &windowStatsStub{
		byStart: map[int64]map[int]AccountWindowStats{
			todayStart: {
				1: {Requests: 3},
				2: {Requests: 5},
			},
		},
	}
	svc := NewService(repo, nil, nil, nil)
	svc.now = func() time.Time { return now }

	merged := map[string]any{
		"1": map[string]any{"windows": []any{}},
		"2": map[string]any{"windows": []any{}},
		"3": map[string]any{"windows": []any{}},
	}
	svc.enrichTodayStats(t.Context(), merged)

	if len(repo.captured) != 1 {
		t.Fatalf("expected exactly 1 BatchWindowStats call, got %d", len(repo.captured))
	}
	if len(repo.captured[0]) != 3 {
		t.Errorf("expected all 3 account IDs in one call, got %v", repo.captured[0])
	}
}

func TestExtractBodyError(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "Anthropic standard nested error",
			body: `{"error":{"type":"authentication_error","message":"Invalid x-api-key"}}`,
			want: "authentication_error: Invalid x-api-key",
		},
		{
			name: "nested error with only message",
			body: `{"error":{"message":"rate limited"}}`,
			want: "rate limited",
		},
		{
			name: "nested error with only type",
			body: `{"error":{"type":"overloaded"}}`,
			want: "overloaded",
		},
		{
			name: "error as plain string",
			body: `{"error":"upstream gone"}`,
			want: "upstream gone",
		},
		{
			name: "top-level code + message (pool format)",
			body: `{"code":"INVALID_API_KEY","message":"Invalid API key"}`,
			want: "INVALID_API_KEY: Invalid API key",
		},
		{
			name: "top-level only message",
			body: `{"message":"something broke"}`,
			want: "something broke",
		},
		{
			name: "top-level only code",
			body: `{"code":"BAD_REQUEST"}`,
			want: "BAD_REQUEST",
		},
		{
			name: "empty body",
			body: ``,
			want: "",
		},
		{
			name: "non-JSON body",
			body: `<html>500 Internal Server Error</html>`,
			want: "",
		},
		{
			name: "unrelated JSON",
			body: `{"foo":"bar"}`,
			want: "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractBodyError([]byte(c.body))
			if got != c.want {
				t.Errorf("extractBodyError(%q) = %q, want %q", c.body, got, c.want)
			}
		})
	}
}

func TestPersistRateLimitFromWindows(t *testing.T) {
	writer := newStubStateWriter()
	svc := NewService(stubRepository{}, nil, nil, writer)

	accounts := map[string]any{
		// 7d 100% + 另一个 5h 99%：取 7d 的 reset_seconds 做恢复时间
		"42": map[string]any{
			"windows": []any{
				map[string]any{"key": "5h", "used_percent": 99.0, "reset_seconds": float64(300)},
				map[string]any{"key": "7d", "used_percent": 100.0, "reset_seconds": float64(34800)}, // 9h 40m
			},
		},
		// 两个窗口都 100%：取两者中较晚的 reset
		"7": map[string]any{
			"windows": []any{
				map[string]any{"key": "5h", "used_percent": 100.0, "reset_seconds": float64(1200)},
				map[string]any{"key": "7d", "used_percent": 100.0, "reset_seconds": float64(3600)},
			},
		},
		// 全部 <100%：清空
		"3": map[string]any{
			"windows": []any{
				map[string]any{"key": "5h", "used_percent": 42.0, "reset_seconds": float64(600)},
			},
		},
		// 无 windows：跳过
		"1": map[string]any{},
	}

	svc.persistRateLimitFromWindows(t.Context(), accounts)

	if got, ok := writer.rateLimited[42]; !ok || got == nil {
		t.Fatalf("expected account 42 to be MarkRateLimited, got %+v", got)
	} else if until := time.Until(*got); until < 9*time.Hour+30*time.Minute || until > 9*time.Hour+50*time.Minute {
		t.Errorf("account 42 reset expected ~9h40m, got %s", until)
	}

	if got, ok := writer.rateLimited[7]; !ok || got == nil {
		t.Fatalf("expected account 7 to be MarkRateLimited, got %+v", got)
	} else if until := time.Until(*got); until < 55*time.Minute || until > 65*time.Minute {
		t.Errorf("account 7 should take LATER of two resets (~1h), got %s", until)
	}

	if !writer.cleared[3] {
		t.Errorf("account 3 should have ClearRateLimited called")
	}
	if _, ok := writer.rateLimited[1]; ok {
		t.Errorf("account 1 has no windows, should not call MarkRateLimited")
	}
	if writer.cleared[1] {
		t.Errorf("account 1 has no windows, should not call ClearRateLimited")
	}
}
