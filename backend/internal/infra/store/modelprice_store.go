package store

import (
	"context"
	"fmt"

	"github.com/DouDOU-start/airgate-core/ent"
	entmodelprice "github.com/DouDOU-start/airgate-core/ent/modelprice"
	entmodeltag "github.com/DouDOU-start/airgate-core/ent/modeltag"
	appmodelprice "github.com/DouDOU-start/airgate-core/internal/app/modelprice"
)

// ModelPriceStore 使用 Ent 实现模型价目表仓储。
type ModelPriceStore struct {
	db *ent.Client
}

// NewModelPriceStore 创建价目表仓储。
func NewModelPriceStore(db *ent.Client) *ModelPriceStore {
	return &ModelPriceStore{db: db}
}

// List 查询价目表列表。
func (s *ModelPriceStore) List(ctx context.Context, filter appmodelprice.ListFilter) ([]appmodelprice.ModelPrice, int64, error) {
	query := s.db.ModelPrice.Query()
	if filter.Keyword != "" {
		query = query.Where(entmodelprice.ModelContainsFold(filter.Keyword))
	}
	if filter.TagID != nil {
		query = query.Where(entmodelprice.TagIDEQ(*filter.TagID))
	}

	total, err := query.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	items, err := query.
		WithTag().
		Offset((filter.Page - 1) * filter.PageSize).
		Limit(filter.PageSize).
		Order(ent.Asc(entmodelprice.FieldModel)).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}

	return mapModelPriceList(items), int64(total), nil
}

// ListAll 全量加载价目表，供 pricing 缓存重载使用。
func (s *ModelPriceStore) ListAll(ctx context.Context) ([]appmodelprice.ModelPrice, error) {
	items, err := s.db.ModelPrice.Query().WithTag().All(ctx)
	if err != nil {
		return nil, err
	}
	return mapModelPriceList(items), nil
}

// FindByID 按 ID 查询价格条目。
func (s *ModelPriceStore) FindByID(ctx context.Context, id int) (appmodelprice.ModelPrice, error) {
	item, err := s.db.ModelPrice.Query().Where(entmodelprice.IDEQ(id)).WithTag().Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return appmodelprice.ModelPrice{}, appmodelprice.ErrModelPriceNotFound
		}
		return appmodelprice.ModelPrice{}, err
	}
	return mapModelPrice(item), nil
}

// Create 创建价格条目。
func (s *ModelPriceStore) Create(ctx context.Context, input appmodelprice.CreateInput) (appmodelprice.ModelPrice, error) {
	builder := s.db.ModelPrice.Create().
		SetModel(input.Model).
		SetInputPrice(input.InputPrice).
		SetOutputPrice(input.OutputPrice).
		SetCachedInputPrice(input.CachedInputPrice).
		SetCacheCreationPrice(input.CacheCreationPrice).
		SetCacheCreation1hPrice(input.CacheCreation1hPrice).
		SetPricingExtra(input.PricingExtra).
		SetPerRequestPrice(input.PerRequestPrice)
	if input.TagID != nil && *input.TagID > 0 {
		builder = builder.SetTagID(*input.TagID)
	}
	item, err := builder.Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return appmodelprice.ModelPrice{}, appmodelprice.ErrModelPriceExists
		}
		return appmodelprice.ModelPrice{}, err
	}
	return s.FindByID(ctx, item.ID)
}

// Update 更新价格条目。
func (s *ModelPriceStore) Update(ctx context.Context, id int, input appmodelprice.UpdateInput) (appmodelprice.ModelPrice, error) {
	builder := s.db.ModelPrice.UpdateOneID(id).
		SetNillableModel(input.Model).
		SetNillableInputPrice(input.InputPrice).
		SetNillableOutputPrice(input.OutputPrice).
		SetNillableCachedInputPrice(input.CachedInputPrice).
		SetNillableCacheCreationPrice(input.CacheCreationPrice).
		SetNillableCacheCreation1hPrice(input.CacheCreation1hPrice).
		SetNillablePerRequestPrice(input.PerRequestPrice)
	// pricing_extra 整体替换：非 nil 时整块写入（空 map 清空扩展）。
	if input.PricingExtra != nil {
		builder = builder.SetPricingExtra(input.PricingExtra)
	}
	// 标签三态：nil 不改；0 清空；正数设为该标签。
	if input.TagID != nil {
		if *input.TagID > 0 {
			builder = builder.SetTagID(*input.TagID)
		} else {
			builder = builder.ClearTagID()
		}
	}
	item, err := builder.Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return appmodelprice.ModelPrice{}, appmodelprice.ErrModelPriceNotFound
		}
		if ent.IsConstraintError(err) {
			return appmodelprice.ModelPrice{}, appmodelprice.ErrModelPriceExists
		}
		return appmodelprice.ModelPrice{}, err
	}
	return s.FindByID(ctx, item.ID)
}

// Delete 删除价格条目。
func (s *ModelPriceStore) Delete(ctx context.Context, id int) error {
	if err := s.db.ModelPrice.DeleteOneID(id).Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return appmodelprice.ErrModelPriceNotFound
		}
		return err
	}
	return nil
}

func mapModelPriceList(items []*ent.ModelPrice) []appmodelprice.ModelPrice {
	result := make([]appmodelprice.ModelPrice, 0, len(items))
	for _, item := range items {
		result = append(result, mapModelPrice(item))
	}
	return result
}

func mapModelPrice(item *ent.ModelPrice) appmodelprice.ModelPrice {
	result := appmodelprice.ModelPrice{
		ID:                   item.ID,
		Model:                item.Model,
		InputPrice:           item.InputPrice,
		OutputPrice:          item.OutputPrice,
		CachedInputPrice:     item.CachedInputPrice,
		CacheCreationPrice:   item.CacheCreationPrice,
		CacheCreation1hPrice: item.CacheCreation1hPrice,
		PerRequestPrice:      item.PerRequestPrice,
		PricingExtra:         item.PricingExtra,
		TagID:                item.TagID,
		CreatedAt:            item.CreatedAt,
		UpdatedAt:            item.UpdatedAt,
	}
	if tag := item.Edges.Tag; tag != nil {
		result.TagName = tag.Name
	}
	return result
}

// —— 模型标签仓储 ——

// ListTags 列出全部标签（按名称排序），并以 GroupBy 一次性填充各标签的模型计数。
func (s *ModelPriceStore) ListTags(ctx context.Context) ([]appmodelprice.Tag, error) {
	tags, err := s.db.ModelTag.Query().
		Order(ent.Asc(entmodeltag.FieldName)).
		All(ctx)
	if err != nil {
		return nil, err
	}

	var rows []struct {
		TagID int `json:"tag_id"`
		Count int `json:"count"`
	}
	if err := s.db.ModelPrice.Query().
		Where(entmodelprice.TagIDNotNil()).
		GroupBy(entmodelprice.FieldTagID).
		Aggregate(ent.Count()).
		Scan(ctx, &rows); err != nil {
		return nil, err
	}
	counts := make(map[int]int64, len(rows))
	for _, row := range rows {
		counts[row.TagID] = int64(row.Count)
	}

	result := make([]appmodelprice.Tag, 0, len(tags))
	for _, tag := range tags {
		result = append(result, appmodelprice.Tag{
			ID:         tag.ID,
			Name:       tag.Name,
			ModelCount: counts[tag.ID],
		})
	}
	return result, nil
}

// CreateTag 新建标签。
func (s *ModelPriceStore) CreateTag(ctx context.Context, name string) (appmodelprice.Tag, error) {
	tag, err := s.db.ModelTag.Create().SetName(name).Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return appmodelprice.Tag{}, appmodelprice.ErrTagExists
		}
		return appmodelprice.Tag{}, err
	}
	return appmodelprice.Tag{ID: tag.ID, Name: tag.Name}, nil
}

// RenameTag 重命名标签。
func (s *ModelPriceStore) RenameTag(ctx context.Context, id int, name string) (appmodelprice.Tag, error) {
	tag, err := s.db.ModelTag.UpdateOneID(id).SetName(name).Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return appmodelprice.Tag{}, appmodelprice.ErrTagNotFound
		}
		if ent.IsConstraintError(err) {
			return appmodelprice.Tag{}, appmodelprice.ErrTagExists
		}
		return appmodelprice.Tag{}, err
	}
	count, err := s.db.ModelPrice.Query().Where(entmodelprice.TagIDEQ(id)).Count(ctx)
	if err != nil {
		return appmodelprice.Tag{}, err
	}
	return appmodelprice.Tag{ID: tag.ID, Name: tag.Name, ModelCount: int64(count)}, nil
}

// DeleteTag 删除标签：事务内先清空引用该标签的模型 tag_id，再删标签行。
func (s *ModelPriceStore) DeleteTag(ctx context.Context, id int) error {
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return err
	}
	if _, err := tx.ModelPrice.Update().
		Where(entmodelprice.TagIDEQ(id)).
		ClearTagID().
		Save(ctx); err != nil {
		return rollbackTag(tx, err)
	}
	if err := tx.ModelTag.DeleteOneID(id).Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return rollbackTag(tx, appmodelprice.ErrTagNotFound)
		}
		return rollbackTag(tx, err)
	}
	return tx.Commit()
}

// EnsureTag 按名称 find-or-create，返回标签 ID（种子导入用）。
func (s *ModelPriceStore) EnsureTag(ctx context.Context, name string) (int, error) {
	tag, err := s.db.ModelTag.Query().Where(entmodeltag.NameEQ(name)).Only(ctx)
	if err == nil {
		return tag.ID, nil
	}
	if !ent.IsNotFound(err) {
		return 0, err
	}
	created, err := s.db.ModelTag.Create().SetName(name).Save(ctx)
	if err != nil {
		// 并发竞态：同名标签刚被他人创建，回查一次。
		if ent.IsConstraintError(err) {
			if tag, qerr := s.db.ModelTag.Query().Where(entmodeltag.NameEQ(name)).Only(ctx); qerr == nil {
				return tag.ID, nil
			}
		}
		return 0, err
	}
	return created.ID, nil
}

// rollbackTag 回滚标签事务并透传原始错误。
func rollbackTag(tx *ent.Tx, err error) error {
	if rerr := tx.Rollback(); rerr != nil {
		return fmt.Errorf("%w（回滚失败: %v）", err, rerr)
	}
	return err
}
