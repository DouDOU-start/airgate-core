package store

import (
	"context"

	"github.com/DouDOU-start/airgate-core/ent"
	entapikey "github.com/DouDOU-start/airgate-core/ent/apikey"
	entgroup "github.com/DouDOU-start/airgate-core/ent/group"
	entusagelog "github.com/DouDOU-start/airgate-core/ent/usagelog"
	entuser "github.com/DouDOU-start/airgate-core/ent/user"
	entusersubscription "github.com/DouDOU-start/airgate-core/ent/usersubscription"
	appgroup "github.com/DouDOU-start/airgate-core/internal/app/group"
)

// GroupStore 使用 Ent 实现分组仓储。
type GroupStore struct {
	db *ent.Client
}

// NewGroupStore 创建分组仓储。
func NewGroupStore(db *ent.Client) *GroupStore {
	return &GroupStore{db: db}
}

// List 查询管理员分组列表。
func (s *GroupStore) List(ctx context.Context, filter appgroup.ListFilter) ([]appgroup.Group, int64, error) {
	query := applyGroupListFilters(s.db.Group.Query(), filter.Keyword, filter.Platform, filter.ServiceTier)

	total, err := query.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	list, err := query.
		Offset((filter.Page-1)*filter.PageSize).
		Limit(filter.PageSize).
		Order(ent.Desc(entgroup.FieldSortWeight), ent.Desc(entgroup.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}

	return mapGroups(list), int64(total), nil
}

// ListAvailable 查询用户可用分组列表。
func (s *GroupStore) ListAvailable(ctx context.Context, filter appgroup.AvailableFilter) ([]appgroup.Group, int64, error) {
	query := s.db.Group.Query().Where(
		entgroup.Or(
			entgroup.IsExclusiveEQ(false),
			entgroup.And(
				entgroup.IsExclusiveEQ(true),
				entgroup.HasAllowedUsersWith(entuser.IDEQ(filter.UserID)),
			),
		),
	)
	query = applyGroupListFilters(query, filter.Keyword, filter.Platform, "")

	total, err := query.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	list, err := query.
		Offset((filter.Page-1)*filter.PageSize).
		Limit(filter.PageSize).
		Order(ent.Desc(entgroup.FieldSortWeight), ent.Desc(entgroup.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}

	return mapGroups(list), int64(total), nil
}

// FindByID 按 ID 查询分组。
func (s *GroupStore) FindByID(ctx context.Context, id int) (appgroup.Group, error) {
	item, err := s.db.Group.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return appgroup.Group{}, appgroup.ErrGroupNotFound
		}
		return appgroup.Group{}, err
	}
	return mapGroup(item), nil
}

// Create 创建分组。
func (s *GroupStore) Create(ctx context.Context, input appgroup.CreateInput) (appgroup.Group, error) {
	builder := s.db.Group.Create().
		SetName(input.Name).
		SetPlatform(input.Platform).
		SetRateMultiplier(input.RateMultiplier).
		SetIsExclusive(input.IsExclusive).
		SetSubscriptionType(entgroup.SubscriptionType(input.SubscriptionType)).
		SetServiceTier(input.ServiceTier).
		SetSortWeight(input.SortWeight)

	if input.Quotas != nil {
		builder = builder.SetQuotas(appgroupCloneQuotas(input.Quotas))
	}
	if input.ModelRouting != nil {
		builder = builder.SetModelRouting(appgroupCloneModelRouting(input.ModelRouting))
	}

	item, err := builder.Save(ctx)
	if err != nil {
		return appgroup.Group{}, err
	}

	return mapGroup(item), nil
}

// Update 更新分组。
func (s *GroupStore) Update(ctx context.Context, id int, input appgroup.UpdateInput) (appgroup.Group, error) {
	builder := s.db.Group.UpdateOneID(id)

	if input.Name != nil {
		builder = builder.SetName(*input.Name)
	}
	if input.RateMultiplier != nil {
		builder = builder.SetRateMultiplier(*input.RateMultiplier)
	}
	if input.IsExclusive != nil {
		builder = builder.SetIsExclusive(*input.IsExclusive)
	}
	if input.SubscriptionType != nil {
		builder = builder.SetSubscriptionType(entgroup.SubscriptionType(*input.SubscriptionType))
	}
	if input.Quotas != nil {
		builder = builder.SetQuotas(appgroupCloneQuotas(input.Quotas))
	}
	if input.ModelRouting != nil {
		builder = builder.SetModelRouting(appgroupCloneModelRouting(input.ModelRouting))
	}
	if input.ServiceTier != nil {
		builder = builder.SetServiceTier(*input.ServiceTier)
	}
	if input.SortWeight != nil {
		builder = builder.SetSortWeight(*input.SortWeight)
	}

	item, err := builder.Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return appgroup.Group{}, appgroup.ErrGroupNotFound
		}
		return appgroup.Group{}, err
	}

	return mapGroup(item), nil
}

// Delete 删除分组。
func (s *GroupStore) Delete(ctx context.Context, id int) error {
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err = tx.Group.Get(ctx, id); err != nil {
		if ent.IsNotFound(err) {
			return appgroup.ErrGroupNotFound
		}
		return err
	}

	hasSubscription, err := tx.UserSubscription.Query().
		Where(entusersubscription.HasGroupWith(entgroup.IDEQ(id))).
		Exist(ctx)
	if err != nil {
		return err
	}
	if hasSubscription {
		return appgroup.ErrGroupHasSubscriptions
	}

	if _, err = tx.APIKey.Update().
		Where(entapikey.HasGroupWith(entgroup.IDEQ(id))).
		ClearGroup().
		Save(ctx); err != nil {
		return err
	}

	if _, err = tx.UsageLog.Update().
		Where(entusagelog.HasGroupWith(entgroup.IDEQ(id))).
		ClearGroup().
		Save(ctx); err != nil {
		return err
	}

	if err = tx.Group.DeleteOneID(id).Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return appgroup.ErrGroupNotFound
		}
		if ent.IsConstraintError(err) {
			return appgroup.ErrGroupHasSubscriptions
		}
		return err
	}

	if err = tx.Commit(); err != nil {
		return err
	}

	return nil
}

func applyGroupListFilters(query *ent.GroupQuery, keyword, platform, serviceTier string) *ent.GroupQuery {
	if keyword != "" {
		query = query.Where(entgroup.NameContains(keyword))
	}
	if platform != "" {
		query = query.Where(entgroup.PlatformEQ(platform))
	}
	if serviceTier != "" {
		query = query.Where(entgroup.ServiceTierEQ(serviceTier))
	}
	return query
}

func mapGroups(items []*ent.Group) []appgroup.Group {
	result := make([]appgroup.Group, 0, len(items))
	for _, item := range items {
		result = append(result, mapGroup(item))
	}
	return result
}

func mapGroup(item *ent.Group) appgroup.Group {
	return appgroup.Group{
		ID:               item.ID,
		Name:             item.Name,
		Platform:         item.Platform,
		RateMultiplier:   item.RateMultiplier,
		IsExclusive:      item.IsExclusive,
		SubscriptionType: string(item.SubscriptionType),
		Quotas:           appgroupCloneQuotas(item.Quotas),
		ModelRouting:     appgroupCloneModelRouting(item.ModelRouting),
		ServiceTier:      item.ServiceTier,
		SortWeight:       item.SortWeight,
		CreatedAt:        item.CreatedAt,
		UpdatedAt:        item.UpdatedAt,
	}
}

func appgroupCloneQuotas(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

func appgroupCloneModelRouting(input map[string][]int64) map[string][]int64 {
	if input == nil {
		return nil
	}
	cloned := make(map[string][]int64, len(input))
	for key, value := range input {
		cloned[key] = append([]int64(nil), value...)
	}
	return cloned
}
