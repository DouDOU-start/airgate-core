package account

import (
	"strings"
	"testing"
	"time"
)

func TestParseXAIFreeUsageExhausted(t *testing.T) {
	msg := `You've used all the included free usage for model grok-4.5-build-free for now. Usage resets over a rolling 24-hour window — tokens (actual/limit): 1065387/1000000.`
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	w, plan, ok := parseXAIFreeUsageExhausted(msg, now)
	if !ok {
		t.Fatal("expected parse ok")
	}
	if plan != "free" {
		t.Fatalf("plan=%q", plan)
	}
	if w.UsedPercent < 100 {
		// actual > limit → 100
		t.Fatalf("used_percent=%v want ~100", w.UsedPercent)
	}
	if w.WindowMinutes != 24*60 {
		t.Fatalf("window=%d", w.WindowMinutes)
	}
	if w.ResetsAt == nil || !w.ResetsAt.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("resets_at=%v", w.ResetsAt)
	}
}

func TestNormalizeXAIPlanType(t *testing.T) {
	cases := map[string]string{
		"Free":            "free",
		"super-grok":      "super",
		"SuperGrok":       "super",
		"SuperGrok Heavy": "super_heavy",
		"premium":         "premium",
		"":                "",
	}
	for in, want := range cases {
		if got := normalizeXAIPlanType(in); got != want {
			t.Errorf("normalizeXAIPlanType(%q)=%q want %q", in, got, want)
		}
	}
}

func TestPlanTypeFromClaims(t *testing.T) {
	if got := planTypeFromClaims(map[string]any{"plan": "Super Grok"}); !strings.EqualFold(got, "super") {
		t.Fatalf("got %q", got)
	}
	if got := planTypeFromClaims(map[string]any{
		"subscription": map[string]any{"plan_type": "free"},
	}); got != "free" {
		t.Fatalf("nested plan=%q", got)
	}
}

func TestParseXAIBillingConfigWeekly(t *testing.T) {
	summary := parseXAIBillingConfig(map[string]any{
		"current_period": map[string]any{
			"type":  "weekly",
			"start": "2026-07-01T00:00:00Z",
			"end":   "2026-07-08T00:00:00Z",
		},
		"credit_usage_percent": "42.5",
		"product_usage": []any{
			map[string]any{"product": "Grok 4", "usage_percent": "30"},
		},
	})
	if summary == nil {
		t.Fatal("summary nil")
	}
	if !summary.HasWeeklyData {
		t.Fatal("expected weekly data")
	}
	if summary.UsagePercent == nil || *summary.UsagePercent != 42.5 {
		t.Fatalf("usage=%v", summary.UsagePercent)
	}
	if summary.PeriodEnd != "2026-07-08T00:00:00Z" {
		t.Fatalf("period end=%q", summary.PeriodEnd)
	}
	if len(summary.ProductUsage) != 1 || summary.ProductUsage[0].Product != "Grok 4" {
		t.Fatalf("products=%v", summary.ProductUsage)
	}
	windows := summary.toWindows()
	if len(windows) < 2 {
		t.Fatalf("windows=%d", len(windows))
	}
	if windows[0].Key != "weekly" || windows[0].UsedPercent != 42.5 {
		t.Fatalf("weekly window=%+v", windows[0])
	}
}

func TestParseXAIBillingConfigMonthlyCentsObject(t *testing.T) {
	// CPA：monthly_limit / used 可能是 { "val": N }
	summary := parseXAIBillingConfig(map[string]any{
		"monthly_limit":      map[string]any{"val": "15000"},
		"used":               map[string]any{"val": 3750},
		"on_demand_cap":      "2500",
		"billing_period_end": "2026-07-31T00:00:00Z",
	})
	if summary == nil {
		t.Fatal("summary nil")
	}
	if summary.MonthlyLimitCents == nil || *summary.MonthlyLimitCents != 15000 {
		t.Fatalf("limit=%v", summary.MonthlyLimitCents)
	}
	if summary.UsedPercent == nil || *summary.UsedPercent != 25 {
		t.Fatalf("used%%=%v", summary.UsedPercent)
	}
	if summary.OnDemandUsedPercent == nil || *summary.OnDemandUsedPercent != 0 {
		t.Fatalf("on demand%%=%v", summary.OnDemandUsedPercent)
	}
	if planTypeFromXAIMonthlyLimit(summary.MonthlyLimitCents) != "super" {
		t.Fatalf("plan from limit")
	}
	windows := summary.toWindows()
	foundMonthly := false
	for _, w := range windows {
		if w.Key == "monthly" {
			foundMonthly = true
			if w.UsedPercent != 25 {
				t.Fatalf("monthly used=%v", w.UsedPercent)
			}
			if w.ResetsAt == nil {
				t.Fatal("monthly reset missing")
			}
		}
	}
	if !foundMonthly {
		t.Fatalf("no monthly window in %+v", windows)
	}
}

func TestParseXAIBillingConfigOnDemandAfterExhaust(t *testing.T) {
	summary := parseXAIBillingConfig(map[string]any{
		"monthly_limit": 10000,
		"used":          12500,
		"on_demand_cap": 5000,
	})
	if summary == nil {
		t.Fatal("summary nil")
	}
	if summary.UsedPercent == nil || *summary.UsedPercent != 100 {
		t.Fatalf("used%%=%v", summary.UsedPercent)
	}
	if summary.OnDemandUsedPercent == nil || *summary.OnDemandUsedPercent != 50 {
		t.Fatalf("on demand%%=%v", summary.OnDemandUsedPercent)
	}
}

func TestMergeXAIBillingSummary(t *testing.T) {
	weekly := parseXAIBillingConfig(map[string]any{
		"current_period": map[string]any{
			"type": "weekly",
			"end":  "2026-07-08T00:00:00Z",
		},
		"credit_usage_percent": 60,
		"product_usage":        []any{map[string]any{"product": "Grok 4", "usage_percent": 75}},
	})
	monthly := parseXAIBillingConfig(map[string]any{
		"monthly_limit":      10000,
		"used":               2500,
		"on_demand_cap":      5000,
		"billing_period_end": "2026-08-01T00:00:00Z",
	})
	merged := mergeXAIBillingSummary(weekly, monthly)
	if merged == nil {
		t.Fatal("merged nil")
	}
	if merged.UsagePercent == nil || *merged.UsagePercent != 60 {
		t.Fatalf("weekly usage=%v", merged.UsagePercent)
	}
	if merged.UsedPercent == nil || *merged.UsedPercent != 25 {
		t.Fatalf("monthly used=%v", merged.UsedPercent)
	}
	if merged.BillingPeriodEnd != "2026-08-01T00:00:00Z" {
		t.Fatalf("billing end=%q", merged.BillingPeriodEnd)
	}
	if len(merged.ProductUsage) != 1 {
		t.Fatalf("products=%v", merged.ProductUsage)
	}
}
