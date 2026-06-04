package plugin

import (
	"strconv"
	"strings"

	"github.com/DouDOU-start/airgate-core/internal/billing"
	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

type usageSnapshot struct {
	InputTokens           int
	OutputTokens          int
	CachedInputTokens     int
	CacheCreationTokens   int
	CacheCreation5mTokens int
	CacheCreation1hTokens int
	ReasoningOutputTokens int

	InputPrice           float64
	OutputPrice          float64
	CachedInputPrice     float64
	CacheCreationPrice   float64
	CacheCreation1hPrice float64

	InputCost         float64
	ImageInputCost    float64
	OutputCost        float64
	CachedInputCost   float64
	CacheCreationCost float64
	ImageCost         float64
	ImageCount        int
	ImageTier         string

	ServiceTier  string
	ImageSize    string
	FirstTokenMs int64
}

func usageSnapshotFromSDK(usage *sdk.Usage) usageSnapshot {
	if usage == nil {
		return usageSnapshot{}
	}
	snap := usageSnapshot{FirstTokenMs: usage.FirstTokenMs}

	for _, metric := range usage.Metrics {
		key := normalizedUsageKey(metric.Key, metric.Kind, metric.Label)
		switch key {
		case "input_tokens", "input_token", "prompt_tokens", "prompt_token":
			snap.InputTokens += int(metric.Value)
		case "output_tokens", "output_token", "completion_tokens", "completion_token":
			snap.OutputTokens += int(metric.Value)
		case "cached_input_tokens", "cached_input_token", "cache_read_tokens", "cache_read_token":
			snap.CachedInputTokens += int(metric.Value)
		case "cache_creation_tokens", "cache_creation_token",
			"cache_creation_input_tokens", "cache_creation_input_token":
			snap.CacheCreationTokens += int(metric.Value)
		case "cache_creation_5m_tokens", "cache_creation_5m_token",
			"cache_creation_5m_input_tokens", "cache_creation_5m_input_token":
			snap.CacheCreation5mTokens += int(metric.Value)
		case "cache_creation_1h_tokens", "cache_creation_1h_token",
			"cache_creation_1h_input_tokens", "cache_creation_1h_input_token":
			snap.CacheCreation1hTokens += int(metric.Value)
		case "reasoning_output_tokens", "reasoning_tokens", "reasoning_token":
			snap.ReasoningOutputTokens += int(metric.Value)
		case "images", "image", "image_count":
			snap.ImageCount += int(metric.Value)
		}
	}

	for _, detail := range usage.CostDetails {
		key := normalizedUsageKey(detail.Key, "", detail.Label)
		applyUsageCost(&snap, key, detail.AccountCost, detail.Metadata)
		applyUsagePrice(&snap, key, detail.Metadata)
	}
	if snap.InputCost+snap.ImageInputCost+snap.OutputCost+snap.CachedInputCost+snap.CacheCreationCost+snap.ImageCost <= 0 {
		accountCost := usage.AccountCost
		if accountCost <= 0 {
			for _, metric := range usage.Metrics {
				accountCost += metric.AccountCost
			}
			for _, detail := range usage.CostDetails {
				accountCost += detail.AccountCost
			}
		}
		snap.InputCost = accountCost
	}

	for _, attr := range usage.Attributes {
		key := normalizedUsageKey(attr.Key, attr.Kind, attr.Label)
		switch key {
		case "service_tier", "tier":
			if snap.ServiceTier == "" {
				snap.ServiceTier = attr.Value
			}
		case "image_size", "resolution", "size":
			if snap.ImageSize == "" {
				snap.ImageSize = attr.Value
			}
		case "image_tier", "resolution_tier":
			if snap.ImageTier == "" {
				snap.ImageTier = attr.Value
			}
		}
	}

	if usage.Metadata != nil {
		if snap.ServiceTier == "" {
			snap.ServiceTier = usage.Metadata["service_tier"]
		}
		if snap.ImageSize == "" {
			snap.ImageSize = usage.Metadata["image_size"]
		}
		if snap.ImageTier == "" {
			snap.ImageTier = usage.Metadata["image_tier"]
		}
	}
	if snap.ImageTier == "" && snap.ImageSize != "" {
		if tier, ok := billing.ImageTierForSize(snap.ImageSize); ok {
			snap.ImageTier = tier
		}
	}

	return snap
}

func applyUsageCost(snap *usageSnapshot, key string, cost float64, metadata map[string]string) {
	if snap == nil || cost <= 0 {
		return
	}
	switch key {
	case "input", "input_tokens", "input_token", "prompt_tokens", "prompt_token":
		snap.InputCost += cost
	case "image_input_tokens", "image_input_token":
		snap.ImageInputCost += cost
	case "output", "output_tokens", "output_token", "completion_tokens", "completion_token":
		snap.OutputCost += cost
	case "image", "images", "image_generation", "image_tool", "image_outputs", "image_output",
		"image_output_tokens", "image_output_token":
		snap.ImageCost += cost
		if snap.ImageTier == "" {
			snap.ImageTier = firstMetadataValue(metadata, "image_tier", "tier", "resolution_tier")
		}
		if snap.ImageSize == "" {
			snap.ImageSize = firstMetadataValue(metadata, "image_size", "size", "resolution")
		}
		if snap.ImageCount <= 0 {
			snap.ImageCount = parsePositiveInt(firstMetadataValue(metadata, "image_count", "count", "quantity"))
		}
	case "cached_input", "cached_input_tokens", "cached_input_token", "cache_read_tokens", "cache_read_token":
		snap.CachedInputCost += cost
	case "cache_creation", "cache_creation_tokens", "cache_creation_token",
		"cache_creation_input_tokens", "cache_creation_input_token",
		"cache_creation_5m", "cache_creation_5m_tokens", "cache_creation_5m_token",
		"cache_creation_5m_input_tokens", "cache_creation_5m_input_token",
		"cache_creation_1h", "cache_creation_1h_tokens", "cache_creation_1h_token",
		"cache_creation_1h_input_tokens", "cache_creation_1h_input_token":
		snap.CacheCreationCost += cost
	}
}

func applyUsagePrice(snap *usageSnapshot, key string, metadata map[string]string) {
	if snap == nil || len(metadata) == 0 {
		return
	}
	price, err := strconv.ParseFloat(strings.TrimSpace(metadata["unit_price"]), 64)
	if err != nil || price <= 0 {
		return
	}
	switch key {
	case "input", "input_tokens", "input_token", "prompt_tokens", "prompt_token":
		snap.InputPrice = price
	case "output", "output_tokens", "output_token", "completion_tokens", "completion_token",
		"image", "images", "image_generation", "image_tool":
		snap.OutputPrice = price
	case "cached_input", "cached_input_tokens", "cached_input_token", "cache_read_tokens", "cache_read_token":
		snap.CachedInputPrice = price
	case "cache_creation", "cache_creation_tokens", "cache_creation_token",
		"cache_creation_input_tokens", "cache_creation_input_token",
		"cache_creation_5m", "cache_creation_5m_tokens", "cache_creation_5m_token",
		"cache_creation_5m_input_tokens", "cache_creation_5m_input_token":
		snap.CacheCreationPrice = price
	case "cache_creation_1h", "cache_creation_1h_tokens", "cache_creation_1h_token",
		"cache_creation_1h_input_tokens", "cache_creation_1h_input_token":
		snap.CacheCreation1hPrice = price
	}
}

func firstMetadataValue(metadata map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(metadata[key]); value != "" {
			return value
		}
	}
	return ""
}

func parsePositiveInt(raw string) int {
	if raw == "" {
		return 0
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return 0
	}
	return value
}

func normalizedUsageKey(parts ...string) string {
	for _, part := range parts {
		part = strings.TrimSpace(strings.ToLower(part))
		if part == "" {
			continue
		}
		part = strings.ReplaceAll(part, "-", "_")
		part = strings.ReplaceAll(part, " ", "_")
		return part
	}
	return ""
}
