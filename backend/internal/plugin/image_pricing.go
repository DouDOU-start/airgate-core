package plugin

import (
	"strings"

	"github.com/DouDOU-start/airgate-core/internal/billing"
	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

type imageBillingOverride struct {
	cost          float64
	replacesTotal bool
}

func imageOutputBillingOverride(usage *sdk.Usage, userSettings, groupSettings map[string]map[string]string) (imageBillingOverride, bool) {
	snap := usageSnapshotFromSDK(usage)
	if snap.ImageCost <= 0 || snap.ImageCount <= 0 {
		return imageBillingOverride{}, false
	}
	tier := strings.TrimSpace(snap.ImageTier)
	if tier == "" && strings.TrimSpace(snap.ImageSize) != "" {
		if resolved, ok := billing.ImageTierForSize(snap.ImageSize); ok {
			tier = resolved
		}
	}
	if tier == "" {
		return imageBillingOverride{}, false
	}
	price, _, ok := billing.ResolveImageTierPrice(tier, userSettings, groupSettings)
	if !ok {
		return imageBillingOverride{}, false
	}
	return imageBillingOverride{
		cost:          float64(snap.ImageCount) * price,
		replacesTotal: fixedImagePriceReplacesTotal(usage),
	}, true
}

func fixedImagePriceReplacesTotal(usage *sdk.Usage) bool {
	if usage == nil {
		return false
	}
	model := strings.ToLower(strings.TrimSpace(usage.Model))
	return strings.HasPrefix(model, "gpt-image") ||
		strings.HasPrefix(model, "dall-e") ||
		strings.HasPrefix(model, "dalle")
}

func shouldForwardPluginSetting(plugin, key string) bool {
	if !strings.EqualFold(plugin, billing.OpenAIPluginSettingsKey) {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(key)) {
	case billing.ImagePrice1KKey, billing.ImagePrice2KKey, billing.ImagePrice4KKey:
		return false
	default:
		return true
	}
}
