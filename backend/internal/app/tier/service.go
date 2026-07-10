package tier

import (
	"context"

	"github.com/DouDOU-start/airgate-core/internal/pkg/logx"
	"github.com/DouDOU-start/airgate-core/internal/pkg/pagination"
)

// Service 等级应用服务。
type Service struct {
	repo Repository
}

// NewService 创建等级服务。
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// List 查询等级列表。
func (s *Service) List(ctx context.Context, filter ListFilter) (ListResult, error) {
	page, pageSize := pagination.Normalize(filter.Page, filter.PageSize)
	filter.Page = page
	filter.PageSize = pageSize

	list, total, err := s.repo.List(ctx, filter)
	if err != nil {
		logx.LoggerFromContext(ctx).Error("tier_lookup_failed",
			"op", "list",
			logx.LogFieldError, err)
		return ListResult{}, err
	}
	return ListResult{List: list, Total: total, Page: page, PageSize: pageSize}, nil
}

// Get 获取等级详情。
func (s *Service) Get(ctx context.Context, id int) (Tier, error) {
	return s.repo.FindByID(ctx, id)
}

// Create 创建等级。
func (s *Service) Create(ctx context.Context, input CreateInput) (Tier, error) {
	logger := logx.LoggerFromContext(ctx)
	if err := validateRates(input.Rates); err != nil {
		return Tier{}, err
	}
	item, err := s.repo.Create(ctx, input)
	if err != nil {
		logger.Error("tier_persist_failed",
			"op", "create",
			"name", input.Name,
			logx.LogFieldError, err)
		return Tier{}, err
	}
	logger.Info("tier_create_succeeded", "tier_id", item.ID, "name", item.Name)
	return item, nil
}

// Update 更新等级。
func (s *Service) Update(ctx context.Context, id int, input UpdateInput) (Tier, error) {
	logger := logx.LoggerFromContext(ctx)
	if input.HasRates {
		if err := validateRates(input.Rates); err != nil {
			return Tier{}, err
		}
	}
	item, err := s.repo.Update(ctx, id, input)
	if err != nil {
		logger.Error("tier_persist_failed",
			"op", "update",
			"tier_id", id,
			logx.LogFieldError, err)
		return Tier{}, err
	}
	logger.Info("tier_update_succeeded", "tier_id", id)
	return item, nil
}

// Delete 删除等级（仍有用户归属时拒绝，见 Repository.Delete）。
func (s *Service) Delete(ctx context.Context, id int) error {
	logger := logx.LoggerFromContext(ctx)
	if err := s.repo.Delete(ctx, id); err != nil {
		logger.Error("tier_persist_failed",
			"op", "delete",
			"tier_id", id,
			logx.LogFieldError, err)
		return err
	}
	logger.Info("tier_delete_succeeded", "tier_id", id)
	return nil
}

// validateRates 校验倍率表：条目值必须 > 0（0 值语义是"未设置"，直接删条目即可）。
func validateRates(rates map[int64]float64) error {
	for _, v := range rates {
		if v <= 0 {
			return ErrInvalidRate
		}
	}
	return nil
}
