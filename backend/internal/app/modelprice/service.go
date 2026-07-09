package modelprice

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/DouDOU-start/airgate-core/internal/pkg/pagination"
	"github.com/DouDOU-start/airgate-core/internal/relay/pricing"
	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

// Invalidator pricing 缓存失效窄接口（由 relay/pricing.Cache 实现，可为 nil——测试时不接）。
type Invalidator interface {
	Invalidate()
}

// Service 提供模型价目表用例编排。
type Service struct {
	repo        Repository
	invalidator Invalidator
}

// NewService 创建价目表服务。
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// SetInvalidator 注入 pricing 缓存失效器（server 装配阶段调用；nil 安全）。
func (s *Service) SetInvalidator(invalidator Invalidator) {
	s.invalidator = invalidator
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

// Create 创建价格条目。
func (s *Service) Create(ctx context.Context, input CreateInput) (ModelPrice, error) {
	logger := sdk.LoggerFromContext(ctx)
	item, err := s.repo.Create(ctx, input)
	if err != nil {
		logger.Error("model_price_persist_failed", "op", "create", "model", input.Model, sdk.LogFieldError, err)
		return ModelPrice{}, err
	}
	logger.Info("model_price_created", "model_price_id", item.ID, "model", item.Model)

	s.invalidate()
	return item, nil
}

// Update 更新价格条目。
func (s *Service) Update(ctx context.Context, id int, input UpdateInput) (ModelPrice, error) {
	logger := sdk.LoggerFromContext(ctx)
	item, err := s.repo.Update(ctx, id, input)
	if err != nil {
		logger.Error("model_price_persist_failed", "op", "update", "model_price_id", id, sdk.LogFieldError, err)
		return ModelPrice{}, err
	}

	s.invalidate()
	return item, nil
}

// Delete 删除价格条目。
func (s *Service) Delete(ctx context.Context, id int) error {
	logger := sdk.LoggerFromContext(ctx)
	if err := s.repo.Delete(ctx, id); err != nil {
		logger.Error("model_price_persist_failed", "op", "delete", "model_price_id", id, sdk.LogFieldError, err)
		return err
	}
	logger.Info("model_price_deleted", "model_price_id", id)

	s.invalidate()
	return nil
}

// Import 批量导入（按 model 名 upsert），返回新建/更新条数。
func (s *Service) Import(ctx context.Context, items []ImportItem) (ImportResult, error) {
	logger := sdk.LoggerFromContext(ctx)
	created, updated, err := s.repo.Upsert(ctx, items)
	if err != nil {
		logger.Error("model_price_persist_failed", "op", "import", sdk.LogFieldError, err)
		return ImportResult{}, err
	}
	logger.Info("model_price_imported", "created", created, "updated", updated)

	s.invalidate()
	return ImportResult{Created: created, Updated: updated}, nil
}

// LoadAllPrices 实现 pricing.Loader：全量加载价目表为缓存数据。
func (s *Service) LoadAllPrices(ctx context.Context) (map[string]pricing.Price, error) {
	items, err := s.repo.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	prices := make(map[string]pricing.Price, len(items))
	for _, item := range items {
		tiers, longCtx := parsePricingExtra(item.Model, item.PricingExtra)
		prices[item.Model] = pricing.Price{
			Input:           item.InputPrice,
			Output:          item.OutputPrice,
			CachedInput:     item.CachedInputPrice,
			CacheCreation5m: item.CacheCreationPrice,
			CacheCreation1h: item.CacheCreation1hPrice,
			PerRequest:      item.PerRequestPrice,
			ServiceTiers:    tiers,
			LongContext:     longCtx,
		}
	}
	return prices, nil
}

// parsePricingExtra 把 pricing_extra JSON（map 形态）解析为服务档倍率与长上下文阶梯。
// 解析失败或字段缺失时按"无该扩展"处理（记 warn，不阻断加载）。
func parsePricingExtra(model string, extra map[string]interface{}) (map[string]float64, *pricing.LongContextRule) {
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

	var longCtx *pricing.LongContextRule
	if raw, ok := extra["long_context"]; ok {
		if m, ok := raw.(map[string]interface{}); ok {
			threshold, tok := toFloat(m["threshold_tokens"])
			inMul, iok := toFloat(m["input_multiplier"])
			outMul, ook := toFloat(m["output_multiplier"])
			cachedMul, cok := toFloat(m["cached_multiplier"])
			if tok && iok && ook && cok {
				longCtx = &pricing.LongContextRule{
					ThresholdTokens: int(threshold),
					InputMul:        inMul,
					OutputMul:       outMul,
					CachedMul:       cachedMul,
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
	logger := sdk.LoggerFromContext(ctx)
	tag, err := s.repo.CreateTag(ctx, name)
	if err != nil {
		logger.Error("model_tag_persist_failed", "op", "create", "name", name, sdk.LogFieldError, err)
		return Tag{}, err
	}
	logger.Info("model_tag_created", "model_tag_id", tag.ID, "name", tag.Name)
	return tag, nil
}

// RenameTag 重命名标签（引用侧经外键自动跟随）。
func (s *Service) RenameTag(ctx context.Context, id int, name string) (Tag, error) {
	logger := sdk.LoggerFromContext(ctx)
	tag, err := s.repo.RenameTag(ctx, id, name)
	if err != nil {
		logger.Error("model_tag_persist_failed", "op", "rename", "model_tag_id", id, sdk.LogFieldError, err)
		return Tag{}, err
	}
	return tag, nil
}

// DeleteTag 删除标签；引用该标签的模型 tag_id 置空（store 事务内完成）。
func (s *Service) DeleteTag(ctx context.Context, id int) error {
	logger := sdk.LoggerFromContext(ctx)
	if err := s.repo.DeleteTag(ctx, id); err != nil {
		logger.Error("model_tag_persist_failed", "op", "delete", "model_tag_id", id, sdk.LogFieldError, err)
		return err
	}
	logger.Info("model_tag_deleted", "model_tag_id", id)
	return nil
}
