package account

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"

	"github.com/DouDOU-start/airgate-core/internal/plugin"
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

func TestGetModelsUsesUpstreamForAPIKeyAccount(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %s, want /v1/models", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Fatalf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"upstream-a"},{"id":"upstream-b","name":"Upstream B"}]}`))
	}))
	defer upstream.Close()

	service := NewService(stubRepository{
		findByID: func(_ context.Context, _ int, opts LoadOptions) (Account, error) {
			if !opts.WithProxy {
				t.Fatalf("expected WithProxy to be true")
			}
			return Account{
				ID:          1,
				Platform:    "openai",
				Type:        "apikey",
				Credentials: map[string]string{"api_key": "sk-test", "base_url": upstream.URL},
			}, nil
		},
	}, stubPluginCatalog{models: []sdk.ModelInfo{{ID: "fallback"}}}, nil, nil)

	models, err := service.GetModels(t.Context(), 1)
	if err != nil {
		t.Fatalf("GetModels returned error: %v", err)
	}
	if len(models) != 2 || models[0].ID != "upstream-a" || models[0].Name != "upstream-a" || models[1].ID != "upstream-b" || models[1].Name != "Upstream B" {
		t.Fatalf("models = %+v", models)
	}
}

func TestGetModelsUsesPluginModelsForOAuthAccount(t *testing.T) {
	upstreamCalled := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	service := NewService(stubRepository{
		findByID: func(context.Context, int, LoadOptions) (Account, error) {
			return Account{
				ID:          1,
				Platform:    "openai",
				Type:        "oauth",
				Credentials: map[string]string{"access_token": "token", "base_url": upstream.URL},
			}, nil
		},
	}, stubPluginCatalog{models: []sdk.ModelInfo{{ID: "plugin-model", Name: "Plugin Model"}}}, nil, nil)

	models, err := service.GetModels(t.Context(), 1)
	if err != nil {
		t.Fatalf("GetModels returned error: %v", err)
	}
	if upstreamCalled {
		t.Fatalf("OAuth account should not request upstream models")
	}
	if len(models) != 1 || models[0].ID != "plugin-model" || models[0].Name != "Plugin Model" {
		t.Fatalf("models = %+v", models)
	}
}

func TestShouldPersistQuotaExtraAllowsClearingPlanMetadata(t *testing.T) {
	if !shouldPersistQuotaExtra("plan_type", "") {
		t.Fatalf("empty plan_type should be persisted to clear stale subscription data")
	}
	if !shouldPersistQuotaExtra("subscription_active_until", "") {
		t.Fatalf("empty subscription_active_until should be persisted to clear stale subscription data")
	}
	if shouldPersistQuotaExtra("email", "") {
		t.Fatalf("empty non-plan metadata should not be persisted")
	}
}

func TestShouldAutoRefreshQuotaSkipsPureAPIKeyAccounts(t *testing.T) {
	cases := []struct {
		name string
		item Account
		want bool
	}{
		{
			name: "apikey type",
			item: Account{Type: "apikey", Credentials: map[string]string{"api_key": "sk-test"}},
			want: false,
		},
		{
			name: "legacy api key credentials without type",
			item: Account{Credentials: map[string]string{"api_key": "sk-test"}},
			want: false,
		},
		{
			name: "oauth access token",
			item: Account{Type: "oauth", Credentials: map[string]string{"access_token": "at"}},
			want: true,
		},
		{
			name: "oauth refresh token",
			item: Account{Type: "oauth", Credentials: map[string]string{"refresh_token": "rt"}},
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldAutoRefreshQuota(tc.item); got != tc.want {
				t.Fatalf("shouldAutoRefreshQuota() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestListResolvesPluginOAuthPlanFilter(t *testing.T) {
	var captured ListFilter
	service := NewService(stubRepository{
		list: func(_ context.Context, filter ListFilter) ([]Account, int64, error) {
			captured = filter
			return nil, 0, nil
		},
	}, stubPluginCatalog{
		metas: []plugin.PluginMeta{{
			Platform: "kiro",
			Metadata: map[string]string{
				oauthPlanMetadataKey: `[{"key":"pro","label":"Pro","credential_key":"plan_type","match":"contains","matches":["Builder Id Pro"]}]`,
			},
		}},
	}, noOpConcurrency{}, nil)

	_, err := service.List(t.Context(), ListFilter{
		Page:        1,
		PageSize:    20,
		Platform:    "kiro",
		AccountType: oauthPlanFilterID("kiro", "pro"),
	})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if captured.AccountType != "" {
		t.Fatalf("captured AccountType = %q, want empty after virtual filter resolution", captured.AccountType)
	}
	if captured.Credential == nil {
		t.Fatal("captured Credential is nil")
	}
	if captured.Credential.Platform != "kiro" ||
		captured.Credential.AccountType != "oauth" ||
		captured.Credential.Key != "plan_type" ||
		captured.Credential.MatchMode != "contains" ||
		len(captured.Credential.Values) != 1 ||
		captured.Credential.Values[0] != "Builder Id Pro" {
		t.Fatalf("captured Credential = %+v", captured.Credential)
	}
}

func TestListKeepsUnknownOAuthPlanFilterExact(t *testing.T) {
	var captured ListFilter
	service := NewService(stubRepository{
		list: func(_ context.Context, filter ListFilter) ([]Account, int64, error) {
			captured = filter
			return nil, 0, nil
		},
	}, stubPluginCatalog{}, noOpConcurrency{}, nil)

	_, err := service.List(t.Context(), ListFilter{Page: 1, PageSize: 20, AccountType: oauthPlanFilterID("openai", "plus")})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if captured.AccountType != oauthPlanFilterID("openai", "plus") {
		t.Fatalf("captured AccountType = %q, want unresolved virtual filter to remain exact", captured.AccountType)
	}
	if captured.Credential != nil {
		t.Fatalf("captured Credential = %+v, want nil", captured.Credential)
	}
}

type stubRepository struct {
	create   func(context.Context, CreateInput) (Account, error)
	findByID func(context.Context, int, LoadOptions) (Account, error)
	list     func(context.Context, ListFilter) ([]Account, int64, error)
	listAll  func(context.Context, ListFilter) ([]Account, error)
}

type noOpConcurrency struct{}

func (noOpConcurrency) GetCurrentCounts(context.Context, []int) map[int]int {
	return map[int]int{}
}

func (s stubRepository) List(ctx context.Context, filter ListFilter) ([]Account, int64, error) {
	if s.list != nil {
		return s.list(ctx, filter)
	}
	return nil, 0, nil
}

func (s stubRepository) ListAll(ctx context.Context, filter ListFilter) ([]Account, error) {
	if s.listAll != nil {
		return s.listAll(ctx, filter)
	}
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

func (s stubRepository) FindByID(ctx context.Context, id int, opts LoadOptions) (Account, error) {
	if s.findByID == nil {
		return Account{}, nil
	}
	return s.findByID(ctx, id, opts)
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
	rateLimited    map[int]*time.Time
	cleared        map[int]bool
	markersCleared map[int]int
	disabled       map[int]string
}

func newStubStateWriter() *stubStateWriter {
	return &stubStateWriter{
		rateLimited:    map[int]*time.Time{},
		cleared:        map[int]bool{},
		markersCleared: map[int]int{},
		disabled:       map[int]string{},
	}
}

func (s *stubStateWriter) MarkRateLimited(_ context.Context, accountID int, until time.Time, _ string) {
	cp := until
	s.rateLimited[accountID] = &cp
}

func (s *stubStateWriter) ClearRateLimited(_ context.Context, accountID int) {
	s.cleared[accountID] = true
}

func (s *stubStateWriter) ClearRateLimitMarkers(_ context.Context, accountID int) int {
	s.markersCleared[accountID]++
	return 0
}

func (s *stubStateWriter) MarkDisabled(_ context.Context, accountID int, reason string) {
	s.disabled[accountID] = reason
}

func (s *stubStateWriter) ManualRecover(_ context.Context, _ int) error {
	return nil
}

func (s *stubStateWriter) ManualDisable(_ context.Context, accountID int, reason string) error {
	s.disabled[accountID] = reason
	return nil
}

type stubPluginCatalog struct {
	models []sdk.ModelInfo
	metas  []plugin.PluginMeta
}

func (s stubPluginCatalog) GetPluginByPlatform(string) *plugin.PluginInstance { return nil }
func (s stubPluginCatalog) GetModels(string) []sdk.ModelInfo                  { return s.models }
func (s stubPluginCatalog) GetAccountTypes(string) []sdk.AccountType          { return nil }
func (s stubPluginCatalog) GetCredentialFields(string) []sdk.CredentialField  { return nil }
func (s stubPluginCatalog) GetAllPluginMeta() []plugin.PluginMeta             { return s.metas }

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

func TestConnectivityTestErrorMessage(t *testing.T) {
	cases := []struct {
		name    string
		outcome sdk.ForwardOutcome
		want    string
	}{
		{
			name: "优先透传上游错误体",
			outcome: sdk.ForwardOutcome{
				Kind: sdk.OutcomeClientError,
				Upstream: sdk.UpstreamResponse{
					StatusCode: http.StatusBadRequest,
					Body:       []byte(`{"error":{"message":"model not supported"}}`),
				},
				Reason: "HTTP 400: fallback reason",
			},
			want: "HTTP 400: model not supported",
		},
		{
			name: "客户端错误可用 reason 兜底",
			outcome: sdk.ForwardOutcome{
				Kind:     sdk.OutcomeClientError,
				Upstream: sdk.UpstreamResponse{StatusCode: http.StatusBadRequest},
				Reason:   "The model is not supported.",
			},
			want: "HTTP 400: The model is not supported.",
		},
		{
			name: "空流诊断不直接展示给用户",
			outcome: sdk.ForwardOutcome{
				Kind:     sdk.OutcomeUpstreamTransient,
				Upstream: sdk.UpstreamResponse{StatusCode: http.StatusBadGateway},
				Reason:   "上游流式响应为空：已收到完成事件但没有文本、工具调用或响应输出",
			},
			want: "上游未返回有效响应，请检查测试模型是否被该上游账号支持或查看上游日志",
		},
		{
			name: "账号限流使用统一提示",
			outcome: sdk.ForwardOutcome{
				Kind: sdk.OutcomeAccountRateLimited,
			},
			want: "上游账号当前被限流，请稍后重试",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := connectivityTestErrorMessage(c.outcome); got != c.want {
				t.Fatalf("connectivityTestErrorMessage() = %q, want %q", got, c.want)
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
		// 插件显式声明忽略限流：即使用量超过 100%，也不写 rate_limited
		"9": map[string]any{
			"windows": []any{
				map[string]any{"key": "monthly", "used_percent": 180.0, "reset_seconds": float64(3600), "ignore_limit": true},
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
	if _, ok := writer.rateLimited[9]; ok {
		t.Errorf("account 9 uses ignore_limit, should not call MarkRateLimited")
	}
	if !writer.cleared[9] {
		t.Errorf("account 9 uses ignore_limit, should have ClearRateLimited called")
	}
	if _, ok := writer.rateLimited[1]; ok {
		t.Errorf("account 1 has no windows, should not call MarkRateLimited")
	}
	if writer.cleared[1] {
		t.Errorf("account 1 has no windows, should not call ClearRateLimited")
	}
}
