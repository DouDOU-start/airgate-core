package plugin

import (
	"math"
	"testing"

	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

func TestUsageSnapshotFromSDK_RecognizesClaudeCacheCreationKeys(t *testing.T) {
	usage := &sdk.Usage{
		Metrics: []sdk.UsageMetric{
			{Key: "input_tokens", Value: 2},
			{Key: "output_tokens", Value: 163},
			{Key: "cached_input_tokens", Value: 141779},
			{Key: "cache_creation_input_tokens", Value: 1756},
			{Key: "cache_creation_5m_input_tokens", Value: 1756},
			{Key: "cache_creation_1h_input_tokens", Value: 0},
		},
		CostDetails: []sdk.UsageCostDetail{
			{Key: "input_tokens", AccountCost: 0.00001, Metadata: map[string]string{"unit_price": "5"}},
			{Key: "cached_input_tokens", AccountCost: 0.0708895, Metadata: map[string]string{"unit_price": "0.5"}},
			{Key: "cache_creation_5m_input_tokens", AccountCost: 0.010975, Metadata: map[string]string{"unit_price": "6.25"}},
			{Key: "output_tokens", AccountCost: 0.004075, Metadata: map[string]string{"unit_price": "25"}},
		},
	}

	snap := usageSnapshotFromSDK(usage)

	if snap.CacheCreationTokens != 1756 {
		t.Fatalf("CacheCreationTokens = %d, want 1756", snap.CacheCreationTokens)
	}
	if snap.CacheCreation5mTokens != 1756 {
		t.Fatalf("CacheCreation5mTokens = %d, want 1756", snap.CacheCreation5mTokens)
	}
	if snap.CacheCreation1hTokens != 0 {
		t.Fatalf("CacheCreation1hTokens = %d, want 0", snap.CacheCreation1hTokens)
	}
	if math.Abs(snap.CacheCreationCost-0.010975) > 1e-12 {
		t.Fatalf("CacheCreationCost = %.12f, want %.12f", snap.CacheCreationCost, 0.010975)
	}
	if math.Abs(snap.CacheCreationPrice-6.25) > 1e-12 {
		t.Fatalf("CacheCreationPrice = %.12f, want 6.25", snap.CacheCreationPrice)
	}
}
