package bookmark

import (
	"context"

	"github.com/DouDOU-start/airgate-core/internal/pkg/logx"
	"github.com/DouDOU-start/airgate-core/internal/pkg/pagination"
)

// Service 提供备忘录域用例编排。
type Service struct {
	repo Repository
}

// NewService 创建备忘录服务。
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// List 查询备忘录列表。
func (s *Service) List(ctx context.Context, filter ListFilter) ([]Bookmark, int64, error) {
	page, pageSize := pagination.Normalize(filter.Page, filter.PageSize)
	filter.Page = page
	filter.PageSize = pageSize

	list, total, err := s.repo.List(ctx, filter)
	if err != nil {
		logx.LoggerFromContext(ctx).Error("bookmark_lookup_failed",
			"op", "list",
			logx.LogFieldError, err)
		return nil, 0, err
	}
	return list, total, nil
}

// Get 获取备忘录详情。
func (s *Service) Get(ctx context.Context, id int) (Bookmark, error) {
	item, err := s.repo.FindByID(ctx, id)
	if err != nil {
		logx.LoggerFromContext(ctx).Error("bookmark_lookup_failed",
			"bookmark_id", id,
			logx.LogFieldError, err)
	}
	return item, err
}

// Create 创建备忘录。
func (s *Service) Create(ctx context.Context, input CreateInput) (Bookmark, error) {
	logger := logx.LoggerFromContext(ctx)
	item, err := s.repo.Create(ctx, input)
	if err != nil {
		logger.Error("bookmark_persist_failed",
			"op", "create",
			"name", input.Name,
			logx.LogFieldError, err)
		return item, err
	}
	logger.Info("bookmark_create_succeeded", "bookmark_id", item.ID)
	return item, nil
}

// Update 更新备忘录。
func (s *Service) Update(ctx context.Context, id int, input UpdateInput) (Bookmark, error) {
	logger := logx.LoggerFromContext(ctx)
	item, err := s.repo.Update(ctx, id, input)
	if err != nil {
		logger.Error("bookmark_persist_failed",
			"op", "update",
			"bookmark_id", id,
			logx.LogFieldError, err)
		return item, err
	}
	logger.Info("bookmark_update_succeeded", "bookmark_id", id)
	return item, nil
}

// Delete 删除备忘录。
func (s *Service) Delete(ctx context.Context, id int) error {
	logger := logx.LoggerFromContext(ctx)
	if err := s.repo.Delete(ctx, id); err != nil {
		logger.Error("bookmark_persist_failed",
			"op", "delete",
			"bookmark_id", id,
			logx.LogFieldError, err)
		return err
	}
	logger.Info("bookmark_delete_succeeded", "bookmark_id", id)
	return nil
}
