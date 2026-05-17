package handler

import (
	"testing"
	"time"

	appauth "github.com/DouDOU-start/airgate-core/internal/app/auth"
	appdashboard "github.com/DouDOU-start/airgate-core/internal/app/dashboard"
	appgroup "github.com/DouDOU-start/airgate-core/internal/app/group"
	apppluginadmin "github.com/DouDOU-start/airgate-core/internal/app/pluginadmin"
	appproxy "github.com/DouDOU-start/airgate-core/internal/app/proxy"
	appsettings "github.com/DouDOU-start/airgate-core/internal/app/settings"
	appsubscription "github.com/DouDOU-start/airgate-core/internal/app/subscription"
	appusage "github.com/DouDOU-start/airgate-core/internal/app/usage"
	appuser "github.com/DouDOU-start/airgate-core/internal/app/user"
	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

func TestUserToRespClonesAllowedGroupIDs(t *testing.T) {
	user := appauth.User{
		ID:              1,
		Email:           "u@test.com",
		Username:        "用户",
		Role:            "admin",
		MaxConcurrency:  3,
		GroupRates:      map[int64]float64{2: 1.5},
		AllowedGroupIDs: []int64{1, 2},
		Status:          "active",
	}

	resp := userToResp(user)
	user.AllowedGroupIDs[0] = 99

	if resp.ID != 1 || resp.Email != "u@test.com" || resp.AllowedGroupIDs[0] != 1 {
		t.Fatalf("认证用户响应异常: %+v", resp)
	}
}

func TestDomainMappersCopySimpleFields(t *testing.T) {
	now := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)

	groupResp := toGroupRespFromDomain(appgroup.Group{
		ID: 2, Name: "默认组", Platform: "openai", RateMultiplier: 1.2,
		IsExclusive: true, StatusVisible: true, SubscriptionType: "monthly",
		ServiceTier: "standard", ForceInstructions: "规则", SortWeight: 5,
		CreatedAt: now, UpdatedAt: now,
	})
	if groupResp.ID != 2 || groupResp.Platform != "openai" || !groupResp.IsExclusive || groupResp.CreatedAt != now {
		t.Fatalf("分组响应异常: %+v", groupResp)
	}

	proxyResp := toProxyRespFromDomain(appproxy.Proxy{ID: 3, Name: "代理", Protocol: "http", Address: "127.0.0.1", Port: 8080, Username: "user", Status: "active"})
	if proxyResp.ID != 3 || proxyResp.Protocol != "http" || proxyResp.Port != 8080 {
		t.Fatalf("代理响应异常: %+v", proxyResp)
	}

	testProxyResp := toTestProxyRespFromDomain(appproxy.TestResult{Success: true, Latency: 12, IPAddress: "1.1.1.1", Country: "中国"})
	if !testProxyResp.Success || testProxyResp.IPAddress != "1.1.1.1" {
		t.Fatalf("代理测试响应异常: %+v", testProxyResp)
	}

	settingResp := toSettingResp(appsettings.Setting{Key: "site_name", Value: "AirGate", Group: "site"})
	if settingResp.Key != "site_name" || settingResp.Value != "AirGate" {
		t.Fatalf("设置响应异常: %+v", settingResp)
	}
}

func TestSubscriptionMappersCloneUsageAndWindows(t *testing.T) {
	now := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	usage := map[string]any{"requests": float64(3)}
	resp := toSubscriptionRespFromDomain(appsubscription.Subscription{
		ID: 1, UserID: 2, GroupID: 3, GroupName: "高级组",
		EffectiveAt: now, ExpiresAt: now.Add(time.Hour), Usage: usage, Status: "active",
	})
	usage["requests"] = float64(9)

	if resp.ID != 1 || resp.UserID != 2 || resp.GroupID != 3 || resp.Usage["requests"] != float64(3) {
		t.Fatalf("订阅响应异常: %+v", resp)
	}

	progress := toSubscriptionProgressRespFromDomain(appsubscription.SubscriptionProgress{
		GroupID:   3,
		GroupName: "高级组",
		Daily:     &appsubscription.UsageWindow{Used: 1, Limit: 10, Reset: "tomorrow"},
	})
	if progress.Daily == nil || progress.Daily.Used != 1 || progress.Weekly != nil {
		t.Fatalf("订阅进度响应异常: %+v", progress)
	}
}

func TestDashboardAndUsageMappers(t *testing.T) {
	stats := toDashboardStatsResp(appdashboard.Stats{TotalAPIKeys: 2, TodayRequests: 7, RPM: 3})
	if stats.TotalAPIKeys != 2 || stats.TodayRequests != 7 || stats.RPM != 3 {
		t.Fatalf("仪表盘统计响应异常: %+v", stats)
	}

	trend := toDashboardTrendResp(appdashboard.Trend{
		ModelDistribution: []appdashboard.ModelStats{{Model: "gpt", Requests: 2}},
		UserRanking:       []appdashboard.UserRanking{{UserID: 1, Email: "u@test.com", Tokens: 30}},
		TokenTrend:        []appdashboard.TimeBucket{{Time: "10:00", InputTokens: 1}},
		TopUsers:          []appdashboard.UserTrend{{UserID: 1, Email: "u@test.com", Trend: []appdashboard.UserTrendPoint{{Time: "10:00", Tokens: 5}}}},
	})
	if len(trend.ModelDistribution) != 1 || len(trend.TopUsers) != 1 || len(trend.TopUsers[0].Trend) != 1 {
		t.Fatalf("仪表盘趋势响应异常: %+v", trend)
	}

	usageResp := toUsageStatsResp(appusage.StatsResult{
		Summary:   appusage.Summary{TotalRequests: 5},
		ByModel:   []appusage.ModelStats{{Model: "gpt", Requests: 2}},
		ByUser:    []appusage.UserStats{{UserID: 1, Email: "u@test.com"}},
		ByAccount: []appusage.AccountStats{{AccountID: 8, Name: "账号"}},
		ByGroup:   []appusage.GroupStats{{GroupID: 3, Name: "组"}},
	})
	if usageResp.TotalRequests != 5 || len(usageResp.ByModel) != 1 || len(usageResp.ByGroup) != 1 {
		t.Fatalf("用量统计响应异常: %+v", usageResp)
	}

	logResp := toUsageLogResp(appusage.LogRecord{ID: 9, Model: "gpt", ActualCost: 1.2, BilledCost: 2.4})
	customerResp := toCustomerUsageLogResp(appusage.LogRecord{
		ID:                    9,
		Model:                 "gpt",
		ActualCost:            1.2,
		BilledCost:            2.4,
		CacheCreationTokens:   11,
		ReasoningOutputTokens: 22,
		ReasoningEffort:       "high",
	})
	if logResp.ActualCost != 1.2 || customerResp.BilledCost != 2.4 || customerResp.Model != "gpt" ||
		customerResp.CacheCreationTokens != 11 || customerResp.ReasoningOutputTokens != 22 || customerResp.ReasoningEffort != "high" {
		t.Fatalf("用量日志响应异常: full=%+v customer=%+v", logResp, customerResp)
	}

	buckets := toUsageTrendBuckets([]appusage.TrendBucket{{Time: "10:00", InputTokens: 1, CacheRead: 2}})
	if len(buckets) != 1 || buckets[0].CacheRead != 2 {
		t.Fatalf("趋势桶响应异常: %+v", buckets)
	}
}

func TestUserAndAPIKeyMappers(t *testing.T) {
	now := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	userResp := toUserRespFromDomain(appuser.User{
		ID: 4, Email: "u@test.com", Username: "用户", Balance: 12,
		BalanceAlertThreshold: 3, Status: "active", CreatedAt: now, UpdatedAt: now,
	})
	if userResp.ID != 4 || userResp.BalanceAlertThreshold != 3 || userResp.UpdatedAt != now {
		t.Fatalf("用户响应异常: %+v", userResp)
	}

	groupID := 8
	keyResp := toAPIKeyRespFromUserDomain(appuser.APIKey{
		ID: 6, Name: "Key", KeyHash: "1234567890abcdef", GroupID: &groupID,
		ExpiresAt: &now, Status: "active",
	}, 4)
	if keyResp.ID != 6 || keyResp.UserID != 4 || keyResp.GroupID == nil || *keyResp.GroupID != 8 || keyResp.ExpiresAt == nil {
		t.Fatalf("用户 API Key 响应异常: %+v", keyResp)
	}

	balanceLog := toBalanceLogResp(appuser.BalanceLog{ID: 1, Action: "add", Amount: 2, CreatedAt: "2026-05-15"})
	if balanceLog.Action != "add" || balanceLog.Amount != 2 {
		t.Fatalf("余额日志响应异常: %+v", balanceLog)
	}
}

func TestPluginMappers(t *testing.T) {
	resp := toPluginResp(apppluginadmin.PluginMeta{
		Name: "gateway-openai", DisplayName: "OpenAI", Version: "1.0.0",
		AccountTypes:  []sdk.AccountType{{Key: "apikey", Label: "API Key"}},
		FrontendPages: []sdk.FrontendPage{{Path: "/plugins/openai", Title: "OpenAI"}},
		ConfigSchema:  []sdk.ConfigField{{Key: "base_url", Label: "Base URL", Type: "text"}},
	})
	if resp.Name != "gateway-openai" || len(resp.AccountTypes) != 1 || len(resp.FrontendPages) != 1 || len(resp.ConfigSchema) != 1 {
		t.Fatalf("插件响应异常: %+v", resp)
	}

	marketResp := toMarketplacePluginResp(apppluginadmin.MarketplacePlugin{
		Name: "gateway-openai", Version: "1.0.0", GithubRepo: "owner/repo",
		Installed: true, InstalledVersion: "0.9.0", HasUpdate: true,
	})
	if !marketResp.Installed || !marketResp.HasUpdate || marketResp.GithubRepo != "owner/repo" {
		t.Fatalf("市场插件响应异常: %+v", marketResp)
	}
}
