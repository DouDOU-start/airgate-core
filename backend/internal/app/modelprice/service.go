package modelprice

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/DouDOU-start/airgate-core/internal/pkg/logx"
	"github.com/DouDOU-start/airgate-core/internal/pkg/pagination"
	"github.com/DouDOU-start/airgate-core/internal/relay/pricing"
)

// Invalidator pricing 缓存失效窄接口（由 relay/pricing.Cache 实现，可为 nil——测试时不接）。
type Invalidator interface {
	Invalidate()
}

// GroupRateRangeReader 非专属分组倍率区间读取器（由 group.Service 实现），供模型广场展示
// "大概打几折"的粗粒度区间；未注入或无有效区间时模型广场只展示价格，不展示倍率。
type GroupRateRangeReader interface {
	PublicRateRange(ctx context.Context) (min, max float64, ok bool)
}

// Service 提供模型价目表用例编排。
type Service struct {
	repo        Repository
	invalidator Invalidator
	groupRates  GroupRateRangeReader
	syncFetcher PriceSyncFetcher
}

// NewService 创建价目表服务。
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// SetInvalidator 注入 pricing 缓存失效器（server 装配阶段调用；nil 安全）。
func (s *Service) SetInvalidator(invalidator Invalidator) {
	s.invalidator = invalidator
}

// SetGroupRateReader 注入分组倍率区间读取器（server 装配阶段调用；nil 安全）。
func (s *Service) SetGroupRateReader(reader GroupRateRangeReader) {
	s.groupRates = reader
}

// List 查询价目表列表。
func (s *Service) List(ctx context.Context, filter ListFilter) (ListResult, error) {
	page, pageSize := pagination.Normalize(filter.Page, filter.PageSize)
	filter.Page = page
	filter.PageSize = pageSize

	list, total, err := s.repo.List(ctx, filter)
	if err != nil {
		return ListResult{}, err
	}
	return ListResult{
		List:     list,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// PublicListResult 模型广场公开查询结果：价目分页列表 + 全局倍率区间（与具体模型无关）。
type PublicListResult struct {
	List          []ModelPrice
	Total         int64
	Page          int
	PageSize      int
	MultiplierMin float64
	MultiplierMax float64
	HasMultiplier bool
}

// ListPublic 查询模型广场公开视图：仅 market_visible=true 的条目 + 非专属分组的倍率区间。
func (s *Service) ListPublic(ctx context.Context, filter ListFilter) (PublicListResult, error) {
	filter.MarketVisibleOnly = true
	listResult, err := s.List(ctx, filter)
	if err != nil {
		return PublicListResult{}, err
	}

	result := PublicListResult{
		List:     listResult.List,
		Total:    listResult.Total,
		Page:     listResult.Page,
		PageSize: listResult.PageSize,
	}
	if s.groupRates != nil {
		if min, max, ok := s.groupRates.PublicRateRange(ctx); ok {
			result.MultiplierMin, result.MultiplierMax, result.HasMultiplier = min, max, true
		}
	}
	return result, nil
}

// Create 创建价格条目。
func (s *Service) Create(ctx context.Context, input CreateInput) (ModelPrice, error) {
	logger := logx.LoggerFromContext(ctx)
	item, err := s.repo.Create(ctx, input)
	if err != nil {
		logger.Error("model_price_persist_failed", "op", "create", "model", input.Model, logx.LogFieldError, err)
		return ModelPrice{}, err
	}
	logger.Info("model_price_created", "model_price_id", item.ID, "model", item.Model)

	s.invalidate()
	return item, nil
}

// Update 更新价格条目。
func (s *Service) Update(ctx context.Context, id int, input UpdateInput) (ModelPrice, error) {
	logger := logx.LoggerFromContext(ctx)
	item, err := s.repo.Update(ctx, id, input)
	if err != nil {
		logger.Error("model_price_persist_failed", "op", "update", "model_price_id", id, logx.LogFieldError, err)
		return ModelPrice{}, err
	}

	s.invalidate()
	return item, nil
}

// Delete 删除价格条目。
func (s *Service) Delete(ctx context.Context, id int) error {
	logger := logx.LoggerFromContext(ctx)
	if err := s.repo.Delete(ctx, id); err != nil {
		logger.Error("model_price_persist_failed", "op", "delete", "model_price_id", id, logx.LogFieldError, err)
		return err
	}
	logger.Info("model_price_deleted", "model_price_id", id)

	s.invalidate()
	return nil
}

// LoadAllPrices 实现 pricing.Loader：全量加载价目表为缓存数据。
func (s *Service) LoadAllPrices(ctx context.Context) (map[string]pricing.Price, error) {
	items, err := s.repo.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	prices := make(map[string]pricing.Price, len(items))
	for _, item := range items {
		tiers, longCtx := ParsePricingExtra(item.Model, item.PricingExtra)
		prices[item.Model] = pricing.Price{
			Input:                 item.InputPrice,
			Output:                item.OutputPrice,
			CachedInput:           item.CachedInputPrice,
			CacheCreation5m:       item.CacheCreationPrice,
			CacheCreation1h:       item.CacheCreation1hPrice,
			PerRequest:            item.PerRequestPrice,
			VideoPerSecond:        ParseVideoPerSecond(item.Model, item.PricingExtra),
			VideoResolutionPrices: ParseVideoResolutionPrices(item.Model, item.PricingExtra),
			ImageSizePrices:       ParseImageSizePrices(item.Model, item.PricingExtra),
			ServiceTiers:          tiers,
			LongContext:           toPricingLongContextRule(longCtx),
		}
	}
	return prices, nil
}

// ParseVideoResolutionPrices 解析 pricing_extra.video.resolution_prices（视频分辨率秒价表，USD/秒）。
// 键统一转成小写并去除首尾空白，值 <=0 的条目丢弃；字段缺失返回 nil。
func ParseVideoResolutionPrices(model string, extra map[string]interface{}) map[string]float64 {
	raw, ok := extra["video"]
	if !ok {
		return nil
	}
	m, ok := raw.(map[string]interface{})
	if !ok {
		slog.Warn("model_price_pricing_extra_invalid", "model", model, "field", "video")
		return nil
	}
	rawPrices, ok := m["resolution_prices"]
	if !ok {
		return nil
	}
	pm, ok := rawPrices.(map[string]interface{})
	if !ok {
		slog.Warn("model_price_pricing_extra_invalid", "model", model, "field", "video.resolution_prices")
		return nil
	}
	prices := make(map[string]float64, len(pm))
	for key, v := range pm {
		key = strings.ToLower(strings.TrimSpace(key))
		if f, ok := toFloat(v); ok && f > 0 && key != "" {
			prices[key] = f
		}
	}
	if len(prices) == 0 {
		return nil
	}
	return prices
}

// toPricingLongContextRule 把 app 层的 LongContextRule 转成 relay/pricing 包的等价类型
// （计费缓存需要的形态），nil 原样透传。
func toPricingLongContextRule(rule *LongContextRule) *pricing.LongContextRule {
	if rule == nil {
		return nil
	}
	return &pricing.LongContextRule{
		ThresholdTokens: rule.ThresholdTokens,
		InputMul:        rule.InputMultiplier,
		OutputMul:       rule.OutputMultiplier,
		CachedMul:       rule.CachedMultiplier,
	}
}

// ParseVideoPerSecond 解析 pricing_extra.video.per_second（视频按秒基础价，任务子系统用）。
// 字段缺失返回 0；形态非法记 warn 不阻断加载。
func ParseVideoPerSecond(model string, extra map[string]interface{}) float64 {
	raw, ok := extra["video"]
	if !ok {
		return 0
	}
	m, ok := raw.(map[string]interface{})
	if !ok {
		slog.Warn("model_price_pricing_extra_invalid", "model", model, "field", "video")
		return 0
	}
	if v, ok := toFloat(m["per_second"]); ok && v > 0 {
		return v
	}
	return 0
}

// ParseImageSizePrices 解析 pricing_extra.image.size_prices（图像分辨率价表，USD/张）：
// 键为 "quality:size" 或裸 "size"，值 <=0 的条目丢弃。字段缺失返回 nil；
// 形态非法记 warn 不阻断加载。导出供 server/handler 复用同一解析规则
// （模型广场展示口径与计费口径一致）。
func ParseImageSizePrices(model string, extra map[string]interface{}) map[string]float64 {
	raw, ok := extra["image"]
	if !ok {
		return nil
	}
	m, ok := raw.(map[string]interface{})
	if !ok {
		slog.Warn("model_price_pricing_extra_invalid", "model", model, "field", "image")
		return nil
	}
	rawPrices, ok := m["size_prices"]
	if !ok {
		return nil
	}
	pm, ok := rawPrices.(map[string]interface{})
	if !ok {
		slog.Warn("model_price_pricing_extra_invalid", "model", model, "field", "image.size_prices")
		return nil
	}
	prices := make(map[string]float64, len(pm))
	for key, v := range pm {
		key = strings.ToLower(strings.TrimSpace(key))
		if f, ok := toFloat(v); ok && f > 0 && key != "" {
			prices[key] = f
		}
	}
	if len(prices) == 0 {
		return nil
	}
	return prices
}

// ParsePricingExtra 把 pricing_extra JSON（map 形态）解析为服务档倍率与长上下文阶梯。
// 解析失败或字段缺失时按"无该扩展"处理（记 warn，不阻断加载）。导出供 server/handler
// 复用同一套解析规则，展示给模型广场公开页的服务档/长上下文与计费实际使用的口径一致。
func ParsePricingExtra(model string, extra map[string]interface{}) (map[string]float64, *LongContextRule) {
	if len(extra) == 0 {
		return nil, nil
	}

	var tiers map[string]float64
	if raw, ok := extra["service_tiers"]; ok {
		if m, ok := raw.(map[string]interface{}); ok {
			tiers = make(map[string]float64, len(m))
			for tier, v := range m {
				if f, ok := toFloat(v); ok {
					tiers[tier] = f
				}
			}
			if len(tiers) == 0 {
				tiers = nil
			}
		} else {
			slog.Warn("model_price_pricing_extra_invalid", "model", model, "field", "service_tiers")
		}
	}

	var longCtx *LongContextRule
	if raw, ok := extra["long_context"]; ok {
		if m, ok := raw.(map[string]interface{}); ok {
			threshold, tok := toFloat(m["threshold_tokens"])
			inMul, iok := toFloat(m["input_multiplier"])
			outMul, ook := toFloat(m["output_multiplier"])
			cachedMul, cok := toFloat(m["cached_multiplier"])
			if tok && iok && ook && cok {
				longCtx = &LongContextRule{
					ThresholdTokens:  int(threshold),
					InputMultiplier:  inMul,
					OutputMultiplier: outMul,
					CachedMultiplier: cachedMul,
				}
			} else {
				slog.Warn("model_price_pricing_extra_invalid", "model", model, "field", "long_context")
			}
		} else {
			slog.Warn("model_price_pricing_extra_invalid", "model", model, "field", "long_context")
		}
	}

	return tiers, longCtx
}

// toFloat 把 JSON/YAML 反序列化出的数值（float64 / int / json.Number）归一为 float64。
func toFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

// invalidate 写路径成功后使 pricing 缓存失效；未注入时为空操作。
func (s *Service) invalidate() {
	if s.invalidator != nil {
		s.invalidator.Invalidate()
	}
}

// —— 模型标签用例（家族归类，归属模型管理）——

// ListTags 列出全部标签（含模型计数）。
func (s *Service) ListTags(ctx context.Context) ([]Tag, error) {
	return s.repo.ListTags(ctx)
}

// CreateTag 新建标签。
func (s *Service) CreateTag(ctx context.Context, name string) (Tag, error) {
	logger := logx.LoggerFromContext(ctx)
	tag, err := s.repo.CreateTag(ctx, name)
	if err != nil {
		logger.Error("model_tag_persist_failed", "op", "create", "name", name, logx.LogFieldError, err)
		return Tag{}, err
	}
	logger.Info("model_tag_created", "model_tag_id", tag.ID, "name", tag.Name)
	return tag, nil
}

// RenameTag 重命名标签（引用侧经外键自动跟随）。
func (s *Service) RenameTag(ctx context.Context, id int, name string) (Tag, error) {
	logger := logx.LoggerFromContext(ctx)
	tag, err := s.repo.RenameTag(ctx, id, name)
	if err != nil {
		logger.Error("model_tag_persist_failed", "op", "rename", "model_tag_id", id, logx.LogFieldError, err)
		return Tag{}, err
	}
	return tag, nil
}

// DeleteTag 删除标签；引用该标签的模型 tag_id 置空（store 事务内完成）。
func (s *Service) DeleteTag(ctx context.Context, id int) error {
	logger := logx.LoggerFromContext(ctx)
	if err := s.repo.DeleteTag(ctx, id); err != nil {
		logger.Error("model_tag_persist_failed", "op", "delete", "model_tag_id", id, logx.LogFieldError, err)
		return err
	}
	logger.Info("model_tag_deleted", "model_tag_id", id)
	return nil
}
