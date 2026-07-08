package store

import (
	"context"
	"time"

	"entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"

	"github.com/DouDOU-start/airgate-core/ent"
	entchannel "github.com/DouDOU-start/airgate-core/ent/channel"
	entgroup "github.com/DouDOU-start/airgate-core/ent/group"
	"github.com/DouDOU-start/airgate-core/ent/predicate"
	appchannel "github.com/DouDOU-start/airgate-core/internal/app/channel"
)

// ChannelStore 使用 Ent 实现渠道仓储。
type ChannelStore struct {
	db *ent.Client
}

// NewChannelStore 创建渠道仓储。
func NewChannelStore(db *ent.Client) *ChannelStore {
	return &ChannelStore{db: db}
}

// List 查询渠道列表（keyword/type/status/tag/group_id 筛选）。
func (s *ChannelStore) List(ctx context.Context, filter appchannel.ListFilter) ([]appchannel.Channel, int64, error) {
	query := s.db.Channel.Query()
	if filter.Keyword != "" {
		query = query.Where(entchannel.NameContains(filter.Keyword))
	}
	if filter.Type != "" {
		query = query.Where(entchannel.TypeEQ(entchannel.Type(filter.Type)))
	}
	if filter.Status != "" {
		query = query.Where(entchannel.StatusEQ(entchannel.Status(filter.Status)))
	}
	if filter.Tag != "" {
		tag := filter.Tag
		query = query.Where(predicate.Channel(func(selector *sql.Selector) {
			selector.Where(sqljson.ValueContains(entchannel.FieldTags, tag))
		}))
	}
	if filter.GroupID != nil {
		query = query.Where(entchannel.HasGroupsWith(entgroup.IDEQ(*filter.GroupID)))
	}

	total, err := query.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	items, err := query.
		Offset((filter.Page - 1) * filter.PageSize).
		Limit(filter.PageSize).
		Order(ent.Desc(entchannel.FieldCreatedAt)).
		WithGroups().
		WithProxy().
		All(ctx)
	if err != nil {
		return nil, 0, err
	}

	return mapChannelList(items), int64(total), nil
}

// ListAll 全量加载渠道（含 groups/proxy 边），供注册表 Reload 使用。
func (s *ChannelStore) ListAll(ctx context.Context) ([]appchannel.Channel, error) {
	items, err := s.db.Channel.Query().
		WithGroups().
		WithProxy().
		All(ctx)
	if err != nil {
		return nil, err
	}
	return mapChannelList(items), nil
}

// FindByID 按 ID 查询渠道（含 groups/proxy 边）。
func (s *ChannelStore) FindByID(ctx context.Context, id int) (appchannel.Channel, error) {
	item, err := s.db.Channel.Query().
		Where(entchannel.IDEQ(id)).
		WithGroups().
		WithProxy().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return appchannel.Channel{}, appchannel.ErrChannelNotFound
		}
		return appchannel.Channel{}, err
	}
	return mapChannel(item), nil
}

// Create 创建渠道。
func (s *ChannelStore) Create(ctx context.Context, input appchannel.CreateInput) (appchannel.Channel, error) {
	builder := s.db.Channel.Create().
		SetName(input.Name).
		SetType(entchannel.Type(input.Type)).
		SetBaseURL(input.BaseURL).
		SetAPIKeys(input.APIKeys).
		SetModels(input.Models).
		SetNillablePriority(input.Priority).
		SetNillableWeight(input.Weight).
		SetNillableMaxConcurrency(input.MaxConcurrency).
		SetNillableMaxRpm(input.MaxRPM).
		SetNillableCostRatio(input.CostRatio)

	if input.ModelMapping != nil {
		builder = builder.SetModelMapping(input.ModelMapping)
	}
	if input.ParamOverride != nil {
		builder = builder.SetParamOverride(input.ParamOverride)
	}
	if input.HeaderOverride != nil {
		builder = builder.SetHeaderOverride(input.HeaderOverride)
	}
	if input.Status != nil {
		builder = builder.SetStatus(entchannel.Status(*input.Status))
	}
	if input.Tags != nil {
		builder = builder.SetTags(input.Tags)
	}
	if input.TestModel != "" {
		builder = builder.SetTestModel(input.TestModel)
	}
	if input.CustomConfig != nil {
		builder = builder.SetCustomConfig(input.CustomConfig)
	}
	if len(input.GroupIDs) > 0 {
		builder = builder.AddGroupIDs(input.GroupIDs...)
	}
	if input.ProxyID != nil && *input.ProxyID > 0 {
		builder = builder.SetProxyID(*input.ProxyID)
	}

	item, err := builder.Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return appchannel.Channel{}, appchannel.ErrInvalidReference
		}
		return appchannel.Channel{}, err
	}
	// 重新加载边，保证返回值带 group_ids/proxy_id。
	return s.FindByID(ctx, item.ID)
}

// Update 更新渠道（partial 语义见 appchannel.UpdateInput）。
func (s *ChannelStore) Update(ctx context.Context, id int, input appchannel.UpdateInput) (appchannel.Channel, error) {
	builder := s.db.Channel.UpdateOneID(id).
		SetNillableName(input.Name).
		SetNillableBaseURL(input.BaseURL).
		SetNillableTestModel(input.TestModel).
		SetNillableErrorMsg(input.ErrorMsg).
		SetNillablePriority(input.Priority).
		SetNillableWeight(input.Weight).
		SetNillableMaxConcurrency(input.MaxConcurrency).
		SetNillableMaxRpm(input.MaxRPM).
		SetNillableCostRatio(input.CostRatio)

	if input.Type != nil {
		builder = builder.SetType(entchannel.Type(*input.Type))
	}
	if input.Status != nil {
		builder = builder.SetStatus(entchannel.Status(*input.Status))
	}
	if input.ClearStatusUntil {
		builder = builder.ClearStatusUntil()
	}
	if len(input.APIKeys) > 0 {
		builder = builder.SetAPIKeys(input.APIKeys)
	}
	if len(input.Models) > 0 {
		builder = builder.SetModels(input.Models)
	}
	if input.ModelMapping != nil {
		builder = builder.SetModelMapping(input.ModelMapping)
	}
	if input.ParamOverride != nil {
		builder = builder.SetParamOverride(input.ParamOverride)
	}
	if input.HeaderOverride != nil {
		builder = builder.SetHeaderOverride(input.HeaderOverride)
	}
	if input.Tags != nil {
		builder = builder.SetTags(input.Tags)
	}
	if input.CustomConfig != nil {
		builder = builder.SetCustomConfig(input.CustomConfig)
	}
	if input.GroupIDs != nil {
		builder = builder.ClearGroups().AddGroupIDs(input.GroupIDs...)
	}
	if input.ProxyID != nil {
		if *input.ProxyID > 0 {
			builder = builder.SetProxyID(*input.ProxyID)
		} else {
			builder = builder.ClearProxy()
		}
	}

	item, err := builder.Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return appchannel.Channel{}, appchannel.ErrChannelNotFound
		}
		if ent.IsConstraintError(err) {
			return appchannel.Channel{}, appchannel.ErrInvalidReference
		}
		return appchannel.Channel{}, err
	}
	return s.FindByID(ctx, item.ID)
}

// Delete 删除渠道。
func (s *ChannelStore) Delete(ctx context.Context, id int) error {
	if err := s.db.Channel.DeleteOneID(id).Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return appchannel.ErrChannelNotFound
		}
		return err
	}
	return nil
}

// BulkUpdate 批量启停/删除/调优先级，返回受影响行数。
func (s *ChannelStore) BulkUpdate(ctx context.Context, input appchannel.BulkUpdateInput) (int, error) {
	switch input.Action {
	case appchannel.BulkActionEnable:
		// 重新启用同时清理上一轮状态残留。
		return s.db.Channel.Update().
			Where(entchannel.IDIn(input.IDs...)).
			SetStatus(entchannel.StatusEnabled).
			SetErrorMsg("").
			ClearStatusUntil().
			Save(ctx)
	case appchannel.BulkActionDisable:
		return s.db.Channel.Update().
			Where(entchannel.IDIn(input.IDs...)).
			SetStatus(entchannel.StatusDisabledManual).
			Save(ctx)
	case appchannel.BulkActionDelete:
		return s.db.Channel.Delete().
			Where(entchannel.IDIn(input.IDs...)).
			Exec(ctx)
	case appchannel.BulkActionSetPriority:
		if input.Priority == nil {
			return 0, appchannel.ErrInvalidBulkAction
		}
		return s.db.Channel.Update().
			Where(entchannel.IDIn(input.IDs...)).
			SetPriority(*input.Priority).
			Save(ctx)
	default:
		return 0, appchannel.ErrInvalidBulkAction
	}
}

// UpdateState 更新渠道调度状态（status / status_until / error_msg）。
func (s *ChannelStore) UpdateState(ctx context.Context, id int, status string, until *time.Time, errMsg string) error {
	builder := s.db.Channel.UpdateOneID(id).
		SetStatus(entchannel.Status(status)).
		SetErrorMsg(errMsg)
	if until != nil {
		builder = builder.SetStatusUntil(*until)
	} else {
		builder = builder.ClearStatusUntil()
	}
	if err := builder.Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return appchannel.ErrChannelNotFound
		}
		return err
	}
	return nil
}

// UpdateTestResult 记录渠道测试结果（响应耗时与测试时间）。
func (s *ChannelStore) UpdateTestResult(ctx context.Context, id int, responseTimeMs int, testedAt time.Time) error {
	err := s.db.Channel.UpdateOneID(id).
		SetResponseTimeMs(responseTimeMs).
		SetTestedAt(testedAt).
		Exec(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return appchannel.ErrChannelNotFound
		}
		return err
	}
	return nil
}

func mapChannelList(items []*ent.Channel) []appchannel.Channel {
	result := make([]appchannel.Channel, 0, len(items))
	for _, item := range items {
		result = append(result, mapChannel(item))
	}
	return result
}

func mapChannel(item *ent.Channel) appchannel.Channel {
	ch := appchannel.Channel{
		ID:             item.ID,
		Name:           item.Name,
		Type:           item.Type.String(),
		BaseURL:        item.BaseURL,
		APIKeys:        item.APIKeys,
		Models:         item.Models,
		ModelMapping:   item.ModelMapping,
		ParamOverride:  item.ParamOverride,
		HeaderOverride: item.HeaderOverride,
		Status:         item.Status.String(),
		StatusUntil:    item.StatusUntil,
		ErrorMsg:       item.ErrorMsg,
		Priority:       item.Priority,
		Weight:         item.Weight,
		MaxConcurrency: item.MaxConcurrency,
		MaxRPM:         item.MaxRpm,
		CostRatio:      item.CostRatio,
		Tags:           item.Tags,
		TestModel:      item.TestModel,
		CustomConfig:   item.CustomConfig,
		ResponseTimeMs: item.ResponseTimeMs,
		TestedAt:       item.TestedAt,
		LastUsedAt:     item.LastUsedAt,
		CreatedAt:      item.CreatedAt,
		UpdatedAt:      item.UpdatedAt,
	}
	if groups, err := item.Edges.GroupsOrErr(); err == nil {
		ch.GroupIDs = make([]int, 0, len(groups))
		for _, g := range groups {
			ch.GroupIDs = append(ch.GroupIDs, g.ID)
		}
	}
	if p, err := item.Edges.ProxyOrErr(); err == nil && p != nil {
		proxyID := p.ID
		ch.ProxyID = &proxyID
		ch.Proxy = &appchannel.ProxyInfo{
			Protocol: p.Protocol.String(),
			Address:  p.Address,
			Port:     p.Port,
			Username: p.Username,
			Password: p.Password,
		}
	}
	return ch
}
