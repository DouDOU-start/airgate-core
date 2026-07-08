package modelprice

import (
	"context"

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
		prices[item.Model] = pricing.Price{
			Input:         item.InputPrice,
			Output:        item.OutputPrice,
			CachedInput:   item.CachedInputPrice,
			CacheCreation: item.CacheCreationPrice,
			PerRequest:    item.PerRequestPrice,
		}
	}
	return prices, nil
}

// invalidate 写路径成功后使 pricing 缓存失效；未注入时为空操作。
func (s *Service) invalidate() {
	if s.invalidator != nil {
		s.invalidator.Invalidate()
	}
}
