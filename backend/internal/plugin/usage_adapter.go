package plugin

import (
	"strconv"
	"strings"

	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

type usageSnapshot struct {
	InputTokens           int
	OutputTokens          int
	CachedInputTokens     int
	CacheCreationTokens   int
	ReasoningOutputTokens int
	TextInputTokens       int
	ImageInputTokens      int
	ImageCount            int

	InputPrice         float64
	OutputPrice        float64
	CachedInputPrice   float64
	CacheCreationPrice float64
	ImageUnitPrice     float64
	ImageUnit          string

	InputCost         float64
	OutputCost        float64
	CachedInputCost   float64
	CacheCreationCost float64

	ServiceTier  string
	ImageSize    string
	FirstTokenMs int64
}

func usageSnapshotFromSDK(usage *sdk.Usage) usageSnapshot {
	if usage == nil {
		return usageSnapshot{}
	}
	snap := usageSnapshot{
		InputTokens:           usage.InputTokens,
		OutputTokens:          usage.OutputTokens,
		CachedInputTokens:     usage.CachedInputTokens,
		CacheCreationTokens:   usage.CacheCreationTokens,
		ReasoningOutputTokens: usage.ReasoningOutputTokens,
		InputPrice:            usage.InputPrice,
		OutputPrice:           usage.OutputPrice,
		CachedInputPrice:      usage.CachedInputPrice,
		CacheCreationPrice:    usage.CacheCreationPrice,
		InputCost:             usage.InputCost,
		OutputCost:            usage.OutputCost,
		CachedInputCost:       usage.CachedInputCost,
		CacheCreationCost:     usage.CacheCreationCost,
		FirstTokenMs:          usage.FirstTokenMs,
	}

	meta := usage.Metadata
	snap.TextInputTokens = metadataInt(meta, "openai.image.input_text_tokens")
	snap.ImageInputTokens = metadataInt(meta, "openai.image.input_image_tokens")
	snap.ImageCount = metadataInt(meta, "openai.image.count")
	snap.ImageUnitPrice = metadataFloat(meta, "openai.image.unit_price")
	snap.ImageUnit = metadataText(meta, "openai.image.unit")
	snap.ServiceTier = metadataText(meta, "service_tier", "tier")
	snap.ImageSize = metadataText(meta, "openai.image.size")

	return snap
}

func usageMetadataFromSDK(usage *sdk.Usage, snap usageSnapshot) map[string]string {
	meta := map[string]string{}
	if usage == nil {
		return meta
	}

	for key, value := range usage.Metadata {
		putMetadata(meta, key, value)
	}
	putMetadata(meta, "openai.image.size", snap.ImageSize)
	putMetadataInt(meta, "openai.image.input_text_tokens", snap.TextInputTokens)
	putMetadataInt(meta, "openai.image.input_image_tokens", snap.ImageInputTokens)
	putMetadataInt(meta, "openai.image.count", snap.ImageCount)
	putMetadataFloat(meta, "openai.image.unit_price", snap.ImageUnitPrice)
	putMetadata(meta, "openai.image.unit", snap.ImageUnit)
	return meta
}

func metadataText(meta map[string]string, keys ...string) string {
	for _, key := range keys {
		value := strings.TrimSpace(meta[key])
		if value != "" {
			return value
		}
	}
	return ""
}

func metadataInt(meta map[string]string, keys ...string) int {
	for _, key := range keys {
		raw := strings.TrimSpace(meta[key])
		if raw == "" {
			continue
		}
		if value, err := strconv.Atoi(raw); err == nil {
			return value
		}
		if value, err := strconv.ParseFloat(raw, 64); err == nil {
			return int(value)
		}
	}
	return 0
}

func metadataFloat(meta map[string]string, keys ...string) float64 {
	for _, key := range keys {
		raw := strings.TrimSpace(meta[key])
		if raw == "" {
			continue
		}
		if value, err := strconv.ParseFloat(raw, 64); err == nil {
			return value
		}
	}
	return 0
}

func putMetadata(meta map[string]string, key, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	meta[key] = value
}

func putMetadataInt(meta map[string]string, key string, value int) {
	if value <= 0 {
		return
	}
	meta[key] = strconv.Itoa(value)
}

func putMetadataFloat(meta map[string]string, key string, value float64) {
	if value <= 0 {
		return
	}
	meta[key] = strconv.FormatFloat(value, 'f', -1, 64)
}
