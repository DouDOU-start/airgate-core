package store

import (
	"context"

	"github.com/DouDOU-start/airgate-core/ent"
	entbookmark "github.com/DouDOU-start/airgate-core/ent/bookmark"
	appbookmark "github.com/DouDOU-start/airgate-core/internal/app/bookmark"
)

// BookmarkStore 使用 Ent 实现备忘录仓储。
type BookmarkStore struct {
	db *ent.Client
}

// NewBookmarkStore 创建备忘录仓储。
func NewBookmarkStore(db *ent.Client) *BookmarkStore {
	return &BookmarkStore{db: db}
}

// List 查询备忘录列表。
func (s *BookmarkStore) List(ctx context.Context, filter appbookmark.ListFilter) ([]appbookmark.Bookmark, int64, error) {
	query := s.db.Bookmark.Query()
	if filter.Keyword != "" {
		query = query.Where(
			entbookmark.Or(
				entbookmark.NameContains(filter.Keyword),
				entbookmark.BaseURLContains(filter.Keyword),
				entbookmark.RemarkContains(filter.Keyword),
			),
		)
	}

	total, err := query.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	list, err := query.
		Offset((filter.Page - 1) * filter.PageSize).
		Limit(filter.PageSize).
		Order(ent.Desc(entbookmark.FieldID)).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}

	return mapBookmarks(list), int64(total), nil
}

// FindByID 按 ID 查询备忘录。
func (s *BookmarkStore) FindByID(ctx context.Context, id int) (appbookmark.Bookmark, error) {
	item, err := s.db.Bookmark.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return appbookmark.Bookmark{}, appbookmark.ErrBookmarkNotFound
		}
		return appbookmark.Bookmark{}, err
	}
	return mapBookmark(item), nil
}

// Create 创建备忘录。
func (s *BookmarkStore) Create(ctx context.Context, input appbookmark.CreateInput) (appbookmark.Bookmark, error) {
	item, err := s.db.Bookmark.Create().
		SetName(input.Name).
		SetBaseURL(input.BaseURL).
		SetRemark(input.Remark).
		Save(ctx)
	if err != nil {
		return appbookmark.Bookmark{}, err
	}
	return mapBookmark(item), nil
}

// Update 更新备忘录。
func (s *BookmarkStore) Update(ctx context.Context, id int, input appbookmark.UpdateInput) (appbookmark.Bookmark, error) {
	builder := s.db.Bookmark.UpdateOneID(id)
	if input.Name != nil {
		builder = builder.SetName(*input.Name)
	}
	if input.BaseURL != nil {
		builder = builder.SetBaseURL(*input.BaseURL)
	}
	if input.Remark != nil {
		builder = builder.SetRemark(*input.Remark)
	}

	item, err := builder.Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return appbookmark.Bookmark{}, appbookmark.ErrBookmarkNotFound
		}
		return appbookmark.Bookmark{}, err
	}
	return mapBookmark(item), nil
}

// Delete 删除备忘录。
func (s *BookmarkStore) Delete(ctx context.Context, id int) error {
	err := s.db.Bookmark.DeleteOneID(id).Exec(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return appbookmark.ErrBookmarkNotFound
		}
		return err
	}
	return nil
}

func mapBookmarks(items []*ent.Bookmark) []appbookmark.Bookmark {
	result := make([]appbookmark.Bookmark, 0, len(items))
	for _, item := range items {
		result = append(result, mapBookmark(item))
	}
	return result
}

func mapBookmark(item *ent.Bookmark) appbookmark.Bookmark {
	return appbookmark.Bookmark{
		ID:        item.ID,
		Name:      item.Name,
		BaseURL:   item.BaseURL,
		Remark:    item.Remark,
		CreatedAt: item.CreatedAt,
		UpdatedAt: item.UpdatedAt,
	}
}
