package store

import (
	"context"

	"github.com/DouDOU-start/airgate-core/ent"
	enttier "github.com/DouDOU-start/airgate-core/ent/tier"
	entuser "github.com/DouDOU-start/airgate-core/ent/user"
	apptier "github.com/DouDOU-start/airgate-core/internal/app/tier"
)

// TierStore 使用 Ent 实现等级仓储。
type TierStore struct {
	db *ent.Client
}

// NewTierStore 创建等级仓储。
func NewTierStore(db *ent.Client) *TierStore {
	return &TierStore{db: db}
}

// List 查询等级列表（附带归属用户数）。
func (s *TierStore) List(ctx context.Context, filter apptier.ListFilter) ([]apptier.Tier, int64, error) {
	query := s.db.Tier.Query()
	if filter.Keyword != "" {
		query = query.Where(enttier.NameContains(filter.Keyword))
	}

	total, err := query.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	list, err := query.
		Offset((filter.Page-1)*filter.PageSize).
		Limit(filter.PageSize).
		Order(ent.Desc(enttier.FieldSortWeight), ent.Desc(enttier.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}

	// 一次分组聚合取各等级的用户数（tier_users 为 user 表上的等级外键列）
	var rows []struct {
		TierID int `json:"tier_users"`
		Count  int `json:"count"`
	}
	if err := s.db.User.Query().
		Where(entuser.HasTier()).
		GroupBy("tier_users").
		Aggregate(ent.Count()).
		Scan(ctx, &rows); err != nil {
		return nil, 0, err
	}
	counts := make(map[int]int, len(rows))
	for _, row := range rows {
		counts[row.TierID] = row.Count
	}

	result := make([]apptier.Tier, 0, len(list))
	for _, item := range list {
		mapped := mapTier(item)
		mapped.UserCount = counts[item.ID]
		result = append(result, mapped)
	}
	return result, int64(total), nil
}

// FindByID 按 ID 查询等级。
func (s *TierStore) FindByID(ctx context.Context, id int) (apptier.Tier, error) {
	item, err := s.db.Tier.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return apptier.Tier{}, apptier.ErrTierNotFound
		}
		return apptier.Tier{}, err
	}
	return mapTier(item), nil
}

// Create 创建等级。
func (s *TierStore) Create(ctx context.Context, input apptier.CreateInput) (apptier.Tier, error) {
	builder := s.db.Tier.Create().
		SetName(input.Name).
		SetNote(input.Note).
		SetSortWeight(input.SortWeight)
	if input.Rates != nil {
		builder = builder.SetRates(cloneUserGroupRates(input.Rates))
	}
	item, err := builder.Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return apptier.Tier{}, apptier.ErrTierNameExists
		}
		return apptier.Tier{}, err
	}
	return mapTier(item), nil
}

// Update 更新等级。
func (s *TierStore) Update(ctx context.Context, id int, input apptier.UpdateInput) (apptier.Tier, error) {
	builder := s.db.Tier.UpdateOneID(id)
	if input.Name != nil {
		builder = builder.SetName(*input.Name)
	}
	if input.HasRates {
		builder = builder.SetRates(cloneUserGroupRates(input.Rates))
	}
	if input.Note != nil {
		builder = builder.SetNote(*input.Note)
	}
	if input.SortWeight != nil {
		builder = builder.SetSortWeight(*input.SortWeight)
	}
	item, err := builder.Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return apptier.Tier{}, apptier.ErrTierNotFound
		}
		if ent.IsConstraintError(err) {
			return apptier.Tier{}, apptier.ErrTierNameExists
		}
		return apptier.Tier{}, err
	}
	return mapTier(item), nil
}

// Delete 删除等级。仍有用户归属时拒绝（避免这批用户的倍率静默回落成隐性调价）。
func (s *TierStore) Delete(ctx context.Context, id int) error {
	if _, err := s.db.Tier.Get(ctx, id); err != nil {
		if ent.IsNotFound(err) {
			return apptier.ErrTierNotFound
		}
		return err
	}

	count, err := s.db.User.Query().
		Where(entuser.HasTierWith(enttier.IDEQ(id))).
		Count(ctx)
	if err != nil {
		return err
	}
	if count > 0 {
		return &apptier.TierHasUsersError{Count: count}
	}

	if err := s.db.Tier.DeleteOneID(id).Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return apptier.ErrTierNotFound
		}
		return err
	}
	return nil
}

func mapTier(item *ent.Tier) apptier.Tier {
	return apptier.Tier{
		ID:         item.ID,
		Name:       item.Name,
		Rates:      cloneUserGroupRates(item.Rates),
		Note:       item.Note,
		SortWeight: item.SortWeight,
		CreatedAt:  item.CreatedAt,
		UpdatedAt:  item.UpdatedAt,
	}
}

var _ apptier.Repository = (*TierStore)(nil)
