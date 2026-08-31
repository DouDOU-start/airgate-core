package relayhook

import "testing"

func TestNormalizeRoutePlanRestrictsRateLimitedAuthorization(t *testing.T) {
	candidates := []Candidate{
		{Kind: "account", ID: 1, Platform: "codex", Type: "oauth", State: "rate_limited"},
		{Kind: "account", ID: 2, Platform: "codex", Type: "oauth", State: "active"},
		{Kind: "account", ID: 3, Platform: "xai", Type: "oauth", State: "rate_limited"},
		{Kind: "account", ID: 4, Platform: "codex", Type: "api_key", State: "rate_limited"},
		{Kind: "channel", ID: 5, Platform: "codex", Type: "oauth", State: "rate_limited"},
		{Kind: "account", ID: 6, Platform: "openai-codex", Type: "oauth", State: "rate_limited"},
		{Kind: "account", ID: 7, Platform: "openai_codex", Type: "oauth", State: "rate_limited"},
		{Kind: "account", ID: 8, Platform: "OPENAI", Type: "oauth", State: "rate_limited"},
	}
	plan, err := NormalizeRoutePlan(candidates, &RoutePlan{
		AccountIDs:                 []int{999, 1, 2, 3, 4, 5, 6, 7, 8, 1},
		AllowRateLimitedAccountIDs: []int{999, 2, 3, 4, 5, 1, 6, 7, 8, 1},
		Fallback:                   FallbackCore,
	})
	if err != nil {
		t.Fatalf("归一化路由计划失败: %v", err)
	}
	wantAccounts := []int{1, 2, 3, 4, 6, 7, 8}
	if plan == nil || !equalIntSlice(plan.AccountIDs, wantAccounts) {
		t.Fatalf("账号顺序 = %v，期望 %v", plan, wantAccounts)
	}
	if !equalIntSlice(plan.AllowRateLimitedAccountIDs, []int{1}) {
		t.Fatalf("限流放行账号 = %v，期望 [1]", plan.AllowRateLimitedAccountIDs)
	}
}

func TestNormalizeRoutePlanRejectsUnknownFallback(t *testing.T) {
	_, err := NormalizeRoutePlan(nil, &RoutePlan{Fallback: "none"})
	if err == nil {
		t.Fatal("未知 fallback 应被拒绝")
	}
}

func equalIntSlice(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
