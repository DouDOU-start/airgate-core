package plugin

import (
	"strconv"
	"strings"

	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

const (
	openAIPluginSettingsKey = "openai"

	imagePrice1KKey = "image_price_1k"
	imagePrice2KKey = "image_price_2k"
	imagePrice4KKey = "image_price_4k"
)

func imageBillingCostOverride(usage *sdk.Usage, settings map[string]map[string]string) (float64, bool) {
	snap := usageSnapshotFromSDK(usage)
	if strings.TrimSpace(snap.ImageSize) == "" || snap.ImageCount <= 0 {
		return 0, false
	}
	tier, _, ok := imageTierForSize(snap.ImageSize)
	if !ok {
		return 0, false
	}
	price, ok := imageTierPriceFromSettings(settings, tier)
	if !ok {
		return 0, false
	}
	return float64(snap.ImageCount) * price, true
}

func shouldForwardPluginSetting(plugin, key string) bool {
	if !strings.EqualFold(plugin, openAIPluginSettingsKey) {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(key)) {
	case imagePrice1KKey, imagePrice2KKey, imagePrice4KKey:
		return false
	default:
		return true
	}
}

func imageTierPriceFromSettings(settings map[string]map[string]string, tier string) (float64, bool) {
	key := imageTierPriceKey(tier)
	if key == "" {
		return 0, false
	}
	for pluginName, kv := range settings {
		if !strings.EqualFold(pluginName, openAIPluginSettingsKey) {
			continue
		}
		for k, v := range kv {
			if !strings.EqualFold(k, key) {
				continue
			}
			raw := strings.TrimSpace(v)
			if raw == "" {
				return 0, false
			}
			price, err := strconv.ParseFloat(raw, 64)
			if err != nil || price < 0 {
				return 0, false
			}
			return price, true
		}
	}
	return 0, false
}

func imageTierPriceKey(tier string) string {
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case "1k":
		return imagePrice1KKey
	case "2k":
		return imagePrice2KKey
	case "4k":
		return imagePrice4KKey
	default:
		return ""
	}
}

func imageTierForSize(size string) (tier string, basePrice float64, ok bool) {
	width, height, ok := parseImageSizeForBilling(size)
	if !ok {
		return "", 0, false
	}
	longest := width
	if height > longest {
		longest = height
	}
	switch {
	case longest <= 1536:
		return "1k", 0.10, true
	case longest <= 2048:
		return "2k", 0.20, true
	default:
		return "4k", 0.40, true
	}
}

func parseImageSizeForBilling(size string) (int, int, bool) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(size)), "x")
	if len(parts) != 2 {
		return 0, 0, false
	}
	width, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || width <= 0 {
		return 0, 0, false
	}
	height, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || height <= 0 {
		return 0, 0, false
	}
	return width, height, true
}
