package store

import (
	"context"
	"time"

	"github.com/DouDOU-start/airgate-core/ent"
	entaccount "github.com/DouDOU-start/airgate-core/ent/account"
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
	// 若无需复制账号，走快路径。
	if len(input.CopyAccountsFromGroupIDs) == 0 {
		builder := s.db.Group.Create().
			SetName(input.Name).
			SetPlatform(input.Platform).
			SetRateMultiplier(input.RateMultiplier).
			SetIsExclusive(input.IsExclusive).
			SetStatusVisible(input.StatusVisible).
			SetSubscriptionType(entgroup.SubscriptionType(input.SubscriptionType)).
			SetServiceTier(input.ServiceTier).
			SetForceInstructions(input.ForceInstructions).
			SetNote(input.Note).
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

	// 需要复制账号：在事务内校验源分组平台、收集去重的账号 ID，随后一次性绑定。
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return appgroup.Group{}, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	// 去重源分组 ID。
	seenGroup := make(map[int]struct{}, len(input.CopyAccountsFromGroupIDs))
	uniqueSourceGroupIDs := make([]int, 0, len(input.CopyAccountsFromGroupIDs))
	for _, gid := range input.CopyAccountsFromGroupIDs {
		if _, ok := seenGroup[gid]; ok {
			continue
		}
		seenGroup[gid] = struct{}{}
		uniqueSourceGroupIDs = append(uniqueSourceGroupIDs, gid)
	}

	// 校验源分组存在且平台一致。
	srcGroups, err := tx.Group.Query().
		Where(entgroup.IDIn(uniqueSourceGroupIDs...)).
		All(ctx)
	if err != nil {
		return appgroup.Group{}, err
	}
	if len(srcGroups) != len(uniqueSourceGroupIDs) {
		return appgroup.Group{}, appgroup.ErrGroupNotFound
	}
	for _, g := range srcGroups {
		if g.Platform != input.Platform {
			return appgroup.Group{}, appgroup.ErrSourceGroupPlatformMismatch
		}
	}

	// 从源分组收集去重后的账号 ID。
	accountIDs, err := tx.Account.Query().
		Where(entaccount.HasGroupsWith(entgroup.IDIn(uniqueSourceGroupIDs...))).
		IDs(ctx)
	if err != nil {
		return appgroup.Group{}, err
	}

	builder := tx.Group.Create().
		SetName(input.Name).
		SetPlatform(input.Platform).
		SetRateMultiplier(input.RateMultiplier).
		SetIsExclusive(input.IsExclusive).
		SetStatusVisible(input.StatusVisible).
		SetSubscriptionType(entgroup.SubscriptionType(input.SubscriptionType)).
		SetServiceTier(input.ServiceTier).
		SetForceInstructions(input.ForceInstructions).
		SetNote(input.Note).
		SetSortWeight(input.SortWeight)

	if input.Quotas != nil {
		builder = builder.SetQuotas(appgroupCloneQuotas(input.Quotas))
	}
	if input.ModelRouting != nil {
		builder = builder.SetModelRouting(appgroupCloneModelRouting(input.ModelRouting))
	}
	if len(accountIDs) > 0 {
		builder = builder.AddAccountIDs(accountIDs...)
	}

	item, err := builder.Save(ctx)
	if err != nil {
		return appgroup.Group{}, err
	}

	if err := tx.Commit(); err != nil {
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
	if input.StatusVisible != nil {
		builder = builder.SetStatusVisible(*input.StatusVisible)
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
	if input.ForceInstructions != nil {
		builder = builder.SetForceInstructions(*input.ForceInstructions)
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

// StatsForGroups 批量查询分组统计信息（账号数、容量、用量）。
// todayStart 必须由调用方按用户时区计算好；store 层不再自己读 time.Now。
func (s *GroupStore) StatsForGroups(ctx context.Context, groupIDs []int, todayStart time.Time) (map[int]appgroup.GroupStats, map[int][]appgroup.AccountCapacity, error) {
	if len(groupIDs) == 0 {
		return nil, nil, nil
	}

	result := make(map[int]appgroup.GroupStats, len(groupIDs))
	activeAccounts := make(map[int][]appgroup.AccountCapacity, len(groupIDs))

	// 1. 查询每个分组的账号按状态统计，同时收集活跃账号的容量
	groups, err := s.db.Group.Query().
		Where(entgroup.IDIn(groupIDs...)).
		WithAccounts(func(q *ent.AccountQuery) {
			q.Select(entaccount.FieldStatus, entaccount.FieldMaxConcurrency)
		}).
		All(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, g := range groups {
		stats := appgroup.GroupStats{}
		for _, a := range g.Edges.Accounts {
			switch a.Status {
			case entaccount.StatusActive:
				stats.AccountActive++
				stats.CapacityTotal += a.MaxConcurrency
				activeAccounts[g.ID] = append(activeAccounts[g.ID], appgroup.AccountCapacity{
					AccountID:      a.ID,
					MaxConcurrency: a.MaxConcurrency,
				})
			case entaccount.StatusError:
				stats.AccountError++
			case entaccount.StatusDisabled:
				stats.AccountDisabled++
			}
			stats.AccountTotal++
		}
		result[g.ID] = stats
	}

	// 2. 查询每个分组的总用量
	var totalRows []struct {
		GroupID   int     `json:"group_usage_logs"`
		TotalCost float64 `json:"total_cost"`
	}
	err = s.db.UsageLog.Query().
		Where(entusagelog.HasGroupWith(entgroup.IDIn(groupIDs...))).
		GroupBy("group_usage_logs").
		Aggregate(ent.As(ent.Sum(entusagelog.FieldTotalCost), "total_cost")).
		Scan(ctx, &totalRows)
	if err != nil {
		return nil, nil, err
	}
	for _, row := range totalRows {
		stats := result[row.GroupID]
		stats.TotalCost = row.TotalCost
		result[row.GroupID] = stats
	}

	// 3. 查询每个分组的今日用量
	var todayRows []struct {
		GroupID   int     `json:"group_usage_logs"`
		TotalCost float64 `json:"total_cost"`
	}
	err = s.db.UsageLog.Query().
		Where(
			entusagelog.HasGroupWith(entgroup.IDIn(groupIDs...)),
			entusagelog.CreatedAtGTE(todayStart),
		).
		GroupBy("group_usage_logs").
		Aggregate(ent.As(ent.Sum(entusagelog.FieldTotalCost), "total_cost")).
		Scan(ctx, &todayRows)
	if err != nil {
		return nil, nil, err
	}
	for _, row := range todayRows {
		stats := result[row.GroupID]
		stats.TodayCost = row.TotalCost
		result[row.GroupID] = stats
	}

	return result, activeAccounts, nil
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
		ID:                item.ID,
		Name:              item.Name,
		Platform:          item.Platform,
		RateMultiplier:    item.RateMultiplier,
		IsExclusive:       item.IsExclusive,
		StatusVisible:     item.StatusVisible,
		SubscriptionType:  string(item.SubscriptionType),
		Quotas:            appgroupCloneQuotas(item.Quotas),
		ModelRouting:      appgroupCloneModelRouting(item.ModelRouting),
		ServiceTier:       item.ServiceTier,
		ForceInstructions: item.ForceInstructions,
		Note:              item.Note,
		SortWeight:        item.SortWeight,
		CreatedAt:         item.CreatedAt,
		UpdatedAt:         item.UpdatedAt,
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
