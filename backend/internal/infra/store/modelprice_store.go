package store

import (
	"context"
	"fmt"

	"github.com/DouDOU-start/airgate-core/ent"
	entmodelprice "github.com/DouDOU-start/airgate-core/ent/modelprice"
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

	total, err := query.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	items, err := query.
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
	items, err := s.db.ModelPrice.Query().All(ctx)
	if err != nil {
		return nil, err
	}
	return mapModelPriceList(items), nil
}

// FindByID 按 ID 查询价格条目。
func (s *ModelPriceStore) FindByID(ctx context.Context, id int) (appmodelprice.ModelPrice, error) {
	item, err := s.db.ModelPrice.Get(ctx, id)
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
	item, err := s.db.ModelPrice.Create().
		SetModel(input.Model).
		SetInputPrice(input.InputPrice).
		SetOutputPrice(input.OutputPrice).
		SetCachedInputPrice(input.CachedInputPrice).
		SetCacheCreationPrice(input.CacheCreationPrice).
		SetCacheCreation1hPrice(input.CacheCreation1hPrice).
		SetPricingExtra(input.PricingExtra).
		SetPerRequestPrice(input.PerRequestPrice).
		Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return appmodelprice.ModelPrice{}, appmodelprice.ErrModelPriceExists
		}
		return appmodelprice.ModelPrice{}, err
	}
	return mapModelPrice(item), nil
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
	return mapModelPrice(item), nil
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

// Upsert 按 model 名批量 upsert（单事务：全部成功或全部回滚），返回新建与更新条数。
func (s *ModelPriceStore) Upsert(ctx context.Context, items []appmodelprice.ImportItem) (int, int, error) {
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return 0, 0, err
	}

	created, updated := 0, 0
	for _, item := range items {
		existing, err := tx.ModelPrice.Query().
			Where(entmodelprice.ModelEQ(item.Model)).
			Only(ctx)
		switch {
		case err == nil:
			_, err = existing.Update().
				SetInputPrice(item.InputPrice).
				SetOutputPrice(item.OutputPrice).
				SetCachedInputPrice(item.CachedInputPrice).
				SetCacheCreationPrice(item.CacheCreationPrice).
				SetCacheCreation1hPrice(item.CacheCreation1hPrice).
				SetPricingExtra(item.PricingExtra).
				SetPerRequestPrice(item.PerRequestPrice).
				Save(ctx)
			if err != nil {
				return 0, 0, rollbackUpsert(tx, item.Model, err)
			}
			updated++
		case ent.IsNotFound(err):
			_, err = tx.ModelPrice.Create().
				SetModel(item.Model).
				SetInputPrice(item.InputPrice).
				SetOutputPrice(item.OutputPrice).
				SetCachedInputPrice(item.CachedInputPrice).
				SetCacheCreationPrice(item.CacheCreationPrice).
				SetCacheCreation1hPrice(item.CacheCreation1hPrice).
				SetPricingExtra(item.PricingExtra).
				SetPerRequestPrice(item.PerRequestPrice).
				Save(ctx)
			if err != nil {
				return 0, 0, rollbackUpsert(tx, item.Model, err)
			}
			created++
		default:
			return 0, 0, rollbackUpsert(tx, item.Model, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return created, updated, nil
}

// rollbackUpsert 回滚导入事务并携带出错模型名。
func rollbackUpsert(tx *ent.Tx, model string, err error) error {
	if rerr := tx.Rollback(); rerr != nil {
		return fmt.Errorf("导入 %q 失败: %w（回滚失败: %v）", model, err, rerr)
	}
	return fmt.Errorf("导入 %q 失败: %w", model, err)
}

func mapModelPriceList(items []*ent.ModelPrice) []appmodelprice.ModelPrice {
	result := make([]appmodelprice.ModelPrice, 0, len(items))
	for _, item := range items {
		result = append(result, mapModelPrice(item))
	}
	return result
}

func mapModelPrice(item *ent.ModelPrice) appmodelprice.ModelPrice {
	return appmodelprice.ModelPrice{
		ID:                   item.ID,
		Model:                item.Model,
		InputPrice:           item.InputPrice,
		OutputPrice:          item.OutputPrice,
		CachedInputPrice:     item.CachedInputPrice,
		CacheCreationPrice:   item.CacheCreationPrice,
		CacheCreation1hPrice: item.CacheCreation1hPrice,
		PerRequestPrice:      item.PerRequestPrice,
		PricingExtra:         item.PricingExtra,
		CreatedAt:            item.CreatedAt,
		UpdatedAt:            item.UpdatedAt,
	}
}
