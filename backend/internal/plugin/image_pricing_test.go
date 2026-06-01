package plugin

import (
	"math"
	"testing"

	"github.com/DouDOU-start/airgate-core/internal/billing"
	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

func TestImageOutputBillingOverride_UsesConfiguredTier(t *testing.T) {
	usage := &sdk.Usage{
		Model: "gpt-5.5",
		Attributes: []sdk.UsageAttribute{
			{Key: "image_size", Value: "1672x941"},
		},
		Metrics: []sdk.UsageMetric{
			{Key: "images", Kind: "image", Value: 2},
		},
		CostDetails: []sdk.UsageCostDetail{
			{Key: "images", AccountCost: 0.40},
		},
	}
	settings := map[string]map[string]string{
		"openai": {
			"image_price_2k": "0.08",
		},
	}

	got, ok := imageOutputBillingOverride(usage, nil, settings)
	if !ok {
		t.Fatal("expected override")
	}
	if math.Abs(got.cost-0.16) > 1e-9 {
		t.Fatalf("override = %v, want 0.16 for two 2K images", got.cost)
	}
	if got.replacesTotal {
		t.Fatal("responses model fixed image price should not replace token costs")
	}
}

func TestImageOutputBillingOverride_ImageModelReplacesTotal(t *testing.T) {
	usage := &sdk.Usage{
		Model: "gpt-image-2",
		Attributes: []sdk.UsageAttribute{
			{Key: "image_size", Value: "1024x1024"},
		},
		Metrics: []sdk.UsageMetric{
			{Key: "images", Kind: "image", Value: 1},
		},
		CostDetails: []sdk.UsageCostDetail{
			{Key: "images", AccountCost: 0.40},
		},
	}
	settings := map[string]map[string]string{
		"openai": {
			"image_price_1k": "0.10",
		},
	}

	got, ok := imageOutputBillingOverride(usage, nil, settings)
	if !ok {
		t.Fatal("expected override")
	}
	if math.Abs(got.cost-0.10) > 1e-9 {
		t.Fatalf("override = %v, want 0.10", got.cost)
	}
	if !got.replacesTotal {
		t.Fatal("image model fixed image price should replace the whole request")
	}
}

func TestImageOutputBillingOverride_FallsBackWhenTierUnset(t *testing.T) {
	usage := &sdk.Usage{
		Attributes: []sdk.UsageAttribute{
			{Key: "image_size", Value: "3840x2160"},
		},
		Metrics: []sdk.UsageMetric{
			{Key: "images", Kind: "image", Value: 1},
		},
		CostDetails: []sdk.UsageCostDetail{
			{Key: "images", AccountCost: 0.40},
		},
	}
	settings := map[string]map[string]string{
		"openai": {
			"image_price_2k": "0.08",
		},
	}

	if got, ok := imageOutputBillingOverride(usage, nil, settings); ok {
		t.Fatalf("override = %+v, want fallback", got)
	}
}

func TestImageTierForSize(t *testing.T) {
	tests := []struct {
		size     string
		wantTier string
	}{
		{size: "1024x1024", wantTier: "1k"},
		{size: "1672x941", wantTier: "2k"},
		{size: "3840x2160", wantTier: "4k"},
	}

	for _, tt := range tests {
		t.Run(tt.size, func(t *testing.T) {
			tier, ok := billing.ImageTierForSize(tt.size)
			if !ok {
				t.Fatal("expected tier")
			}
			if tier != tt.wantTier {
				t.Fatalf("ImageTierForSize() = %q, want %q", tier, tt.wantTier)
			}
		})
	}
}

func TestShouldForwardPluginSetting_HidesImagePrices(t *testing.T) {
	if shouldForwardPluginSetting("openai", "image_price_1k") {
		t.Fatal("image price settings should stay inside core")
	}
	if !shouldForwardPluginSetting("openai", "image_enabled") {
		t.Fatal("image_enabled should still be forwarded to the plugin")
	}
	if !shouldForwardPluginSetting("claude", "claude_code_only") {
		t.Fatal("non-openai plugin settings should still be forwarded")
	}
}
