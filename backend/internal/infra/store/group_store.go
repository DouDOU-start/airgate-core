package store

import (
	"context"
	"time"

	"github.com/DouDOU-start/airgate-core/ent"
	entapikey "github.com/DouDOU-start/airgate-core/ent/apikey"
	entchannel "github.com/DouDOU-start/airgate-core/ent/channel"
	entgroup "github.com/DouDOU-start/airgate-core/ent/group"
	entusagelog "github.com/DouDOU-start/airgate-core/ent/usagelog"
	entuser "github.com/DouDOU-start/airgate-core/ent/user"
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
	query := applyGroupListFilters(s.db.Group.Query(), filter.Keyword, filter.Platform)

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
	query = applyGroupListFilters(query, filter.Keyword, filter.Platform)

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
		SetStatusVisible(input.StatusVisible).
		SetNote(input.Note).
		SetSortWeight(input.SortWeight)

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
	if input.StatusVisible != nil {
		builder = builder.SetStatusVisible(*input.StatusVisible)
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

	// 渠道绑定守卫：channel_groups 对 group_id 是 ON DELETE CASCADE，
	// 直接删除会静默解绑，使专属渠道变成公共渠道（对所有分组可调度）。
	channelCount, err := tx.Channel.Query().
		Where(entchannel.HasGroupsWith(entgroup.IDEQ(id))).
		Count(ctx)
	if err != nil {
		return err
	}
	if channelCount > 0 {
		return &appgroup.GroupHasChannelsError{Count: channelCount}
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
		return err
	}

	if err = tx.Commit(); err != nil {
		return err
	}

	return nil
}

// StatsForGroups 批量查询分组统计信息（今日/累计实扣金额，聚合 actual_cost 而非价目表原价）。
// todayStart 必须由调用方按用户时区计算好；store 层不再自己读 time.Now。
func (s *GroupStore) StatsForGroups(ctx context.Context, groupIDs []int, todayStart time.Time) (map[int]appgroup.GroupStats, error) {
	if len(groupIDs) == 0 {
		return nil, nil
	}

	result := make(map[int]appgroup.GroupStats, len(groupIDs))

	// 1. 查询每个分组的累计实扣
	var totalRows []struct {
		GroupID    int     `json:"group_usage_logs"`
		ActualCost float64 `json:"actual_cost"`
	}
	err := s.db.UsageLog.Query().
		Where(entusagelog.HasGroupWith(entgroup.IDIn(groupIDs...))).
		GroupBy("group_usage_logs").
		Aggregate(ent.As(ent.Sum(entusagelog.FieldActualCost), "actual_cost")).
		Scan(ctx, &totalRows)
	if err != nil {
		return nil, err
	}
	for _, row := range totalRows {
		stats := result[row.GroupID]
		stats.TotalCost = row.ActualCost
		result[row.GroupID] = stats
	}

	// 2. 查询每个分组的今日实扣
	var todayRows []struct {
		GroupID    int     `json:"group_usage_logs"`
		ActualCost float64 `json:"actual_cost"`
	}
	err = s.db.UsageLog.Query().
		Where(
			entusagelog.HasGroupWith(entgroup.IDIn(groupIDs...)),
			entusagelog.CreatedAtGTE(todayStart),
		).
		GroupBy("group_usage_logs").
		Aggregate(ent.As(ent.Sum(entusagelog.FieldActualCost), "actual_cost")).
		Scan(ctx, &todayRows)
	if err != nil {
		return nil, err
	}
	for _, row := range todayRows {
		stats := result[row.GroupID]
		stats.TodayCost = row.ActualCost
		result[row.GroupID] = stats
	}

	return result, nil
}

func applyGroupListFilters(query *ent.GroupQuery, keyword, platform string) *ent.GroupQuery {
	if keyword != "" {
		query = query.Where(entgroup.NameContains(keyword))
	}
	if platform != "" {
		query = query.Where(entgroup.PlatformEQ(platform))
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
		ID:             item.ID,
		Name:           item.Name,
		Platform:       item.Platform,
		RateMultiplier: item.RateMultiplier,
		IsExclusive:    item.IsExclusive,
		StatusVisible:  item.StatusVisible,
		Note:           item.Note,
		SortWeight:     item.SortWeight,
		CreatedAt:      item.CreatedAt,
		UpdatedAt:      item.UpdatedAt,
	}
}
