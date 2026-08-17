package modelprice

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/relay/cpa"
)

// DefaultSyncURL 指向当前仓库维护的模型价格目录。
const DefaultSyncURL = "https://raw.githubusercontent.com/DouDOU-start/airgate-core/standalone-gateway/backend/internal/app/modelprice/model_prices_and_context_window.json"

type PriceSyncFetcher interface {
	Fetch(context.Context, string) ([]byte, error)
}

type httpPriceSyncFetcher struct {
	client *http.Client
}

func (f httpPriceSyncFetcher) Fetch(ctx context.Context, source string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, err
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("模型价格源返回 HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

type liteLLMPrice struct {
	InputCostPerToken                   *float64               `json:"input_cost_per_token"`
	InputCostPerTokenPriority           *float64               `json:"input_cost_per_token_priority"`
	OutputCostPerToken                  *float64               `json:"output_cost_per_token"`
	OutputCostPerTokenPriority          *float64               `json:"output_cost_per_token_priority"`
	CacheCreationInputTokenCost         *float64               `json:"cache_creation_input_token_cost"`
	CacheCreationInputTokenCostAbove1hr *float64               `json:"cache_creation_input_token_cost_above_1hr"`
	CacheReadInputTokenCost             *float64               `json:"cache_read_input_token_cost"`
	LongContextInputTokenThreshold      *int                   `json:"long_context_input_token_threshold"`
	LongContextInputCostMultiplier      *float64               `json:"long_context_input_cost_multiplier"`
	LongContextOutputCostMultiplier     *float64               `json:"long_context_output_cost_multiplier"`
	OutputCostPerImage                  *float64               `json:"output_cost_per_image"`
	LiteLLMProvider                     string                 `json:"litellm_provider"`
	Mode                                string                 `json:"mode"`
	AirgatePricingExtra                 map[string]interface{} `json:"airgate_pricing_extra"`
}

// SetSyncFetcher 替换网络拉取器，主要用于可重复的单元测试。
func (s *Service) SetSyncFetcher(fetcher PriceSyncFetcher) {
	if s != nil {
		s.syncFetcher = fetcher
	}
}

// Sync 从仓库内置的兼容 LiteLLM 目录刷新已有模型及 CPA 支持的模型。
// 仅存在于本地的条目和自定义 pricing_extra 字段会被保留。
func (s *Service) Sync(ctx context.Context) (SyncResult, error) {
	result := SyncResult{Source: DefaultSyncURL}
	remote, err := s.fetchRemotePrices(ctx)
	if err != nil {
		return result, err
	}
	result.Fetched = len(remote)

	existing, err := s.repo.ListAll(ctx)
	if err != nil {
		return result, err
	}
	byModel := make(map[string]ModelPrice, len(existing))
	targets := make(map[string]struct{}, len(existing)+100)
	for _, item := range existing {
		byModel[item.Model] = item
		targets[item.Model] = struct{}{}
	}
	for _, platform := range []string{"codex", "claude", "gemini", "vertex", "aistudio", "antigravity", "kimi", "xai"} {
		plans := []string{"pro"}
		if platform == "codex" {
			plans = []string{"free", "plus", "team", "pro"}
		}
		for _, plan := range plans {
			for _, info := range cpa.DefaultModelInfos(platform, plan) {
				targets[info.ID] = struct{}{}
			}
		}
	}
	names := make([]string, 0, len(targets))
	for name := range targets {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, model := range names {
		price, ok := findRemotePrice(remote, model)
		if !ok || !hasUsablePrice(price) {
			result.Skipped++
			continue
		}
		result.Matched++
		if current, exists := byModel[model]; exists {
			update, changed := syncUpdate(current, price)
			if !changed {
				result.Unchanged++
				continue
			}
			if _, err := s.repo.Update(ctx, current.ID, update); err != nil {
				return result, fmt.Errorf("更新模型 %s 失败：%w", model, err)
			}
			result.Updated++
			continue
		}
		if _, err := s.repo.Create(ctx, syncCreate(model, price)); err != nil {
			return result, fmt.Errorf("创建模型 %s 失败：%w", model, err)
		}
		result.Created++
	}
	s.invalidate()
	return result, nil
}

// SyncCandidates 返回目录中的模型，并标记模型是否已存在于本地。
func (s *Service) SyncCandidates(ctx context.Context) ([]SyncCandidate, error) {
	remote, err := s.fetchRemotePrices(ctx)
	if err != nil {
		return nil, err
	}
	existing, err := s.repo.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	exists := make(map[string]struct{}, len(existing))
	for _, item := range existing {
		exists[item.Model] = struct{}{}
	}
	models := make(map[string]struct{}, len(remote))
	for key, price := range remote {
		if !hasUsablePrice(price) {
			continue
		}
		model := key
		if slash := strings.LastIndex(model, "/"); slash >= 0 && slash < len(model)-1 {
			model = model[slash+1:]
		}
		model = strings.TrimSpace(model)
		if model != "" {
			models[model] = struct{}{}
		}
	}
	out := make([]SyncCandidate, 0, len(models))
	for model := range models {
		price, ok := findRemotePrice(remote, model)
		if !ok || !hasUsablePrice(price) {
			continue
		}
		_, present := exists[model]
		out = append(out, SyncCandidate{
			Model: model, Provider: price.LiteLLMProvider, Mode: price.Mode,
			InputPrice: perMillion(price.InputCostPerToken), OutputPrice: perMillion(price.OutputCostPerToken),
			CachedInputPrice:     perMillion(price.CacheReadInputTokenCost),
			CacheCreationPrice:   perMillion(price.CacheCreationInputTokenCost),
			CacheCreation1hPrice: perMillion(price.CacheCreationInputTokenCostAbove1hr),
			PerRequestPrice:      valueOrZero(price.OutputCostPerImage), Exists: present,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Model < out[j].Model })
	return out, nil
}

// SyncSelected 仅创建或更新管理员选择的目录模型。
func (s *Service) SyncSelected(ctx context.Context, selected []string) (SyncResult, error) {
	result := SyncResult{Source: DefaultSyncURL}
	models := normalizeSelectedModels(selected)
	if len(models) == 0 {
		return result, fmt.Errorf("请至少选择一个模型")
	}
	if len(models) > 500 {
		return result, fmt.Errorf("单次最多同步 500 个模型")
	}
	remote, err := s.fetchRemotePrices(ctx)
	if err != nil {
		return result, err
	}
	result.Fetched = len(remote)
	existing, err := s.repo.ListAll(ctx)
	if err != nil {
		return result, err
	}
	byModel := make(map[string]ModelPrice, len(existing))
	for _, item := range existing {
		byModel[item.Model] = item
	}
	for _, model := range models {
		price, ok := findRemotePrice(remote, model)
		if !ok || !hasUsablePrice(price) {
			result.Skipped++
			continue
		}
		result.Matched++
		if current, ok := byModel[model]; ok {
			update, changed := syncUpdate(current, price)
			if !changed {
				result.Unchanged++
				continue
			}
			if _, err := s.repo.Update(ctx, current.ID, update); err != nil {
				return result, fmt.Errorf("更新模型 %s 失败：%w", model, err)
			}
			result.Updated++
			continue
		}
		if _, err := s.repo.Create(ctx, syncCreate(model, price)); err != nil {
			return result, fmt.Errorf("创建模型 %s 失败：%w", model, err)
		}
		result.Created++
	}
	s.invalidate()
	return result, nil
}

func (s *Service) fetchRemotePrices(ctx context.Context) (map[string]liteLLMPrice, error) {
	fetcher := s.syncFetcher
	if fetcher == nil {
		fetcher = httpPriceSyncFetcher{client: &http.Client{Timeout: 30 * time.Second}}
	}
	body, err := fetcher.Fetch(ctx, DefaultSyncURL)
	if err != nil {
		return nil, err
	}
	remote := map[string]liteLLMPrice{}
	if err := json.Unmarshal(body, &remote); err != nil {
		return nil, fmt.Errorf("解析仓库模型价格目录失败：%w", err)
	}
	return remote, nil
}

func normalizeSelectedModels(input []string) []string {
	seen := make(map[string]struct{}, len(input))
	out := make([]string, 0, len(input))
	for _, model := range input {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		out = append(out, model)
	}
	sort.Strings(out)
	return out
}

var cpaModelPriceAliases = map[string]string{
	"gemini-3-flash":                "gemini-3-flash-preview",
	"gemini-3-flash-agent":          "gemini-3.5-flash",
	"gemini-3.1-flash-lite-preview": "gemini-3.1-flash-lite",
	"gemini-3.1-pro-low":            "gemini-3.1-pro-preview",
	"gemini-3.5-flash-extra-low":    "gemini-3.5-flash",
	"gemini-3.5-flash-low":          "gemini-3.5-flash",
	"gemini-3.6-flash-high":         "gemini-3.6-flash",
	"gemini-3.7-flash-high":         "gemini-3.7-flash",
	"gemini-flash-latest":           "gemini-3.7-flash",
	"gemini-flash-lite-latest":      "gemini-3.5-flash-lite",
	"gemini-pro-agent":              "gemini-3.1-pro-preview",
	"gemini-pro-latest":             "gemini-3.1-pro-preview",
}

func findRemotePrice(remote map[string]liteLLMPrice, model string) (liteLLMPrice, bool) {
	if price, ok := findExactRemotePrice(remote, model); ok {
		return price, true
	}
	if canonical := cpaModelPriceAliases[model]; canonical != "" {
		return findExactRemotePrice(remote, canonical)
	}
	return liteLLMPrice{}, false
}

func findExactRemotePrice(remote map[string]liteLLMPrice, model string) (liteLLMPrice, bool) {
	if price, ok := remote[model]; ok {
		return price, true
	}
	for _, prefix := range []string{"openai/", "anthropic/", "gemini/", "vertex_ai/", "xai/"} {
		if price, ok := remote[prefix+model]; ok {
			return price, true
		}
	}
	var found liteLLMPrice
	matches := 0
	for key, price := range remote {
		if slash := strings.LastIndex(key, "/"); slash >= 0 && key[slash+1:] == model {
			found, matches = price, matches+1
		}
	}
	return found, matches == 1
}

func hasUsablePrice(p liteLLMPrice) bool {
	if p.InputCostPerToken != nil || p.OutputCostPerToken != nil ||
		p.CacheReadInputTokenCost != nil || p.OutputCostPerImage != nil {
		return true
	}
	_, hasImage := p.AirgatePricingExtra["image"]
	_, hasVideo := p.AirgatePricingExtra["video"]
	return hasImage || hasVideo
}

func perMillion(value *float64) float64 {
	if value == nil || *value < 0 {
		return 0
	}
	return *value * 1_000_000
}

func syncCreate(model string, p liteLLMPrice) CreateInput {
	enabled := true
	return CreateInput{
		Model: model, InputPrice: perMillion(p.InputCostPerToken), OutputPrice: perMillion(p.OutputCostPerToken),
		CachedInputPrice: perMillion(p.CacheReadInputTokenCost), CacheCreationPrice: perMillion(p.CacheCreationInputTokenCost),
		CacheCreation1hPrice: perMillion(p.CacheCreationInputTokenCostAbove1hr),
		PerRequestPrice:      valueOrZero(p.OutputCostPerImage), PricingExtra: syncPricingExtra(nil, p),
		Enabled: &enabled, MarketVisible: true,
	}
}

func syncUpdate(current ModelPrice, p liteLLMPrice) (UpdateInput, bool) {
	extra := syncPricingExtra(current.PricingExtra, p)
	update := UpdateInput{PricingExtra: extra}
	changed := !jsonEqual(current.PricingExtra, extra)
	setPrice := func(remote *float64, current float64, target **float64) {
		if remote == nil {
			return
		}
		value := perMillion(remote)
		*target = &value
		changed = changed || !samePrice(current, value)
	}
	setPrice(p.InputCostPerToken, current.InputPrice, &update.InputPrice)
	setPrice(p.OutputCostPerToken, current.OutputPrice, &update.OutputPrice)
	setPrice(p.CacheReadInputTokenCost, current.CachedInputPrice, &update.CachedInputPrice)
	setPrice(p.CacheCreationInputTokenCost, current.CacheCreationPrice, &update.CacheCreationPrice)
	setPrice(p.CacheCreationInputTokenCostAbove1hr, current.CacheCreation1hPrice, &update.CacheCreation1hPrice)
	if p.OutputCostPerImage != nil {
		perRequest := valueOrZero(p.OutputCostPerImage)
		update.PerRequestPrice = &perRequest
		changed = changed || !samePrice(current.PerRequestPrice, perRequest)
	}
	return update, changed
}

func syncPricingExtra(existing map[string]interface{}, p liteLLMPrice) map[string]interface{} {
	out := make(map[string]interface{}, len(existing)+2)
	for key, value := range existing {
		out[key] = value
	}
	for key, value := range p.AirgatePricingExtra {
		out[key] = value
	}
	if p.InputCostPerToken != nil && *p.InputCostPerToken > 0 && p.InputCostPerTokenPriority != nil {
		tiers := cloneObject(out["service_tiers"])
		tiers["priority"] = *p.InputCostPerTokenPriority / *p.InputCostPerToken
		out["service_tiers"] = tiers
	} else if p.OutputCostPerToken != nil && *p.OutputCostPerToken > 0 && p.OutputCostPerTokenPriority != nil {
		tiers := cloneObject(out["service_tiers"])
		tiers["priority"] = *p.OutputCostPerTokenPriority / *p.OutputCostPerToken
		out["service_tiers"] = tiers
	}
	if p.LongContextInputTokenThreshold != nil && p.LongContextInputCostMultiplier != nil && p.LongContextOutputCostMultiplier != nil {
		out["long_context"] = map[string]interface{}{
			"threshold_tokens":  *p.LongContextInputTokenThreshold,
			"input_multiplier":  *p.LongContextInputCostMultiplier,
			"output_multiplier": *p.LongContextOutputCostMultiplier,
			"cached_multiplier": *p.LongContextInputCostMultiplier,
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneObject(value interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	if source, ok := value.(map[string]interface{}); ok {
		for key, item := range source {
			out[key] = item
		}
	}
	return out
}

func valueOrZero(value *float64) float64 {
	if value == nil || *value < 0 {
		return 0
	}
	return *value
}

func samePrice(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func jsonEqual(a, b map[string]interface{}) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}
