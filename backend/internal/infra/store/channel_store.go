package store

import (
	"context"
	"time"

	"entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"

	"github.com/DouDOU-start/airgate-core/ent"
	entchannel "github.com/DouDOU-start/airgate-core/ent/channel"
	entchannelkey "github.com/DouDOU-start/airgate-core/ent/channelkey"
	entgroup "github.com/DouDOU-start/airgate-core/ent/group"
	"github.com/DouDOU-start/airgate-core/ent/predicate"
	entusagelog "github.com/DouDOU-start/airgate-core/ent/usagelog"
	appchannel "github.com/DouDOU-start/airgate-core/internal/app/channel"
)

// ChannelStore 使用 Ent 实现渠道仓储（渠道 + 其下 ChannelKey 子实体）。
type ChannelStore struct {
	db *ent.Client
}

// NewChannelStore 创建渠道仓储。
func NewChannelStore(db *ent.Client) *ChannelStore {
	return &ChannelStore{db: db}
}

// withKeys 预加载渠道下的 key（含各 key 的 groups 边），按 ID 升序。
func withKeys(q *ent.ChannelQuery) *ent.ChannelQuery {
	return q.WithKeys(func(kq *ent.ChannelKeyQuery) {
		kq.WithGroups().Order(ent.Asc(entchannelkey.FieldID))
	})
}

// List 查询渠道列表（keyword 筛渠道名；type/status/tag/group_id 筛渠道下的 key）。
func (s *ChannelStore) List(ctx context.Context, filter appchannel.ListFilter) ([]appchannel.Channel, int64, error) {
	query := s.db.Channel.Query()
	if filter.Keyword != "" {
		query = query.Where(entchannel.NameContains(filter.Keyword))
	}
	if filter.Type != "" {
		query = query.Where(entchannel.HasKeysWith(entchannelkey.TypeEQ(entchannelkey.Type(filter.Type))))
	}
	if filter.Status != "" {
		query = query.Where(entchannel.HasKeysWith(entchannelkey.StatusEQ(entchannelkey.Status(filter.Status))))
	}
	if filter.Tag != "" {
		tag := filter.Tag
		query = query.Where(entchannel.HasKeysWith(predicate.ChannelKey(func(selector *sql.Selector) {
			selector.Where(sqljson.ValueContains(entchannelkey.FieldTags, tag))
		})))
	}
	if filter.GroupID != nil {
		query = query.Where(entchannel.HasKeysWith(entchannelkey.HasGroupsWith(entgroup.IDEQ(*filter.GroupID))))
	}

	total, err := query.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	items, err := withKeys(query).
		Offset((filter.Page - 1) * filter.PageSize).
		Limit(filter.PageSize).
		Order(ent.Desc(entchannel.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}

	return mapChannelList(items), int64(total), nil
}

// keySortField 密钥视图排序字段 → ent 列名映射；未知/空值落到默认的 created_at。
func keySortField(sortBy string) string {
	switch sortBy {
	case appchannel.KeySortByPriority:
		return entchannelkey.FieldPriority
	case appchannel.KeySortByWeight:
		return entchannelkey.FieldWeight
	case appchannel.KeySortByName:
		return entchannelkey.FieldName
	case appchannel.KeySortByStatus:
		return entchannelkey.FieldStatus
	default:
		return entchannelkey.FieldCreatedAt
	}
}

// ListKeys 密钥视图：跨渠道平铺分页查询 key（keyword 同时匹配 key 名与所属渠道名）。
func (s *ChannelStore) ListKeys(ctx context.Context, filter appchannel.KeyListFilter) ([]appchannel.ChannelKey, int64, error) {
	query := s.db.ChannelKey.Query()
	if filter.Keyword != "" {
		query = query.Where(entchannelkey.Or(
			entchannelkey.NameContains(filter.Keyword),
			entchannelkey.HasChannelWith(entchannel.NameContains(filter.Keyword)),
		))
	}
	if filter.Type != "" {
		query = query.Where(entchannelkey.TypeEQ(entchannelkey.Type(filter.Type)))
	}
	if filter.Status != "" {
		query = query.Where(entchannelkey.StatusEQ(entchannelkey.Status(filter.Status)))
	}
	if filter.Tag != "" {
		tag := filter.Tag
		query = query.Where(predicate.ChannelKey(func(selector *sql.Selector) {
			selector.Where(sqljson.ValueContains(entchannelkey.FieldTags, tag))
		}))
	}
	if filter.ChannelID != nil {
		query = query.Where(entchannelkey.HasChannelWith(entchannel.IDEQ(*filter.ChannelID)))
	}
	if filter.GroupID != nil {
		query = query.Where(entchannelkey.HasGroupsWith(entgroup.IDEQ(*filter.GroupID)))
	}

	total, err := query.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	orderField := keySortField(filter.SortBy)
	orderFn := ent.Desc
	if filter.SortOrder == appchannel.SortOrderAsc {
		orderFn = ent.Asc
	}

	items, err := query.
		WithChannel().
		WithGroups().
		Offset((filter.Page-1)*filter.PageSize).
		Limit(filter.PageSize).
		Order(orderFn(orderField), ent.Asc(entchannelkey.FieldID)).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}

	result := make([]appchannel.ChannelKey, 0, len(items))
	for _, item := range items {
		baseURL, channelName := "", ""
		if ch, err := item.Edges.ChannelOrErr(); err == nil {
			baseURL = ch.BaseURL
			channelName = ch.Name
		}
		result = append(result, mapChannelKey(item, baseURL, channelName))
	}
	return result, int64(total), nil
}

// ListAll 全量加载渠道（含 keys 及其 groups 边），供注册表 Reload 使用。
func (s *ChannelStore) ListAll(ctx context.Context) ([]appchannel.Channel, error) {
	items, err := withKeys(s.db.Channel.Query()).All(ctx)
	if err != nil {
		return nil, err
	}
	return mapChannelList(items), nil
}

// FindByID 按 ID 查询渠道（含 keys 及其 groups 边）。
func (s *ChannelStore) FindByID(ctx context.Context, id int) (appchannel.Channel, error) {
	item, err := withKeys(s.db.Channel.Query().Where(entchannel.IDEQ(id))).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return appchannel.Channel{}, appchannel.ErrChannelNotFound
		}
		return appchannel.Channel{}, err
	}
	return mapChannel(item), nil
}

// FindKeyByID 按密钥端点 ID 查单把 key（含所属渠道 base_url 与 groups 边）。
func (s *ChannelStore) FindKeyByID(ctx context.Context, keyID int) (appchannel.ChannelKey, error) {
	item, err := s.db.ChannelKey.Query().
		Where(entchannelkey.IDEQ(keyID)).
		WithChannel().
		WithGroups().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return appchannel.ChannelKey{}, appchannel.ErrChannelNotFound
		}
		return appchannel.ChannelKey{}, err
	}
	baseURL, channelName := "", ""
	if ch, err := item.Edges.ChannelOrErr(); err == nil {
		baseURL = ch.BaseURL
		channelName = ch.Name
	}
	return mapChannelKey(item, baseURL, channelName), nil
}

// Create 创建渠道（仅 name/base_url；key 建后单独添加）。
func (s *ChannelStore) Create(ctx context.Context, input appchannel.CreateInput) (appchannel.Channel, error) {
	ch, err := s.db.Channel.Create().
		SetName(input.Name).
		SetBaseURL(input.BaseURL).
		Save(ctx)
	if err != nil {
		return appchannel.Channel{}, err
	}
	return s.FindByID(ctx, ch.ID)
}

// Update 更新渠道（partial，仅 name/base_url）。
func (s *ChannelStore) Update(ctx context.Context, id int, input appchannel.UpdateInput) (appchannel.Channel, error) {
	if err := s.db.Channel.UpdateOneID(id).
		SetNillableName(input.Name).
		SetNillableBaseURL(input.BaseURL).
		Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return appchannel.Channel{}, appchannel.ErrChannelNotFound
		}
		return appchannel.Channel{}, err
	}
	return s.FindByID(ctx, id)
}

// CreateKey 在指定渠道下新增一把 key。
func (s *ChannelStore) CreateKey(ctx context.Context, channelID int, key appchannel.KeyInput) (appchannel.ChannelKey, error) {
	item, err := applyKeyCreate(s.db.ChannelKey.Create().SetChannelID(channelID), key).Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return appchannel.ChannelKey{}, appchannel.ErrInvalidReference
		}
		return appchannel.ChannelKey{}, err
	}
	return s.FindKeyByID(ctx, item.ID)
}

// DeleteKey 删除一把 key。
func (s *ChannelStore) DeleteKey(ctx context.Context, keyID int) error {
	if err := s.db.ChannelKey.DeleteOneID(keyID).Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return appchannel.ErrChannelNotFound
		}
		return err
	}
	return nil
}

// applyKeyCreate 把 KeyInput 应用到 ChannelKey 创建构建器（APIKey 为密文）。
func applyKeyCreate(builder *ent.ChannelKeyCreate, key appchannel.KeyInput) *ent.ChannelKeyCreate {
	models := key.Models
	if models == nil {
		models = []string{}
	}
	builder = builder.
		SetName(key.Name).
		SetType(entchannelkey.Type(key.Type)).
		SetAPIKey(key.APIKey).
		SetModels(models).
		SetNillablePriority(key.Priority).
		SetNillableWeight(key.Weight).
		SetNillableMaxConcurrency(key.MaxConcurrency).
		SetNillableMaxRpm(key.MaxRPM).
		SetNillableCostRatio(key.CostRatio).
		SetNillableBalanceCheckEnabled(key.BalanceCheckEnabled)
	if key.ModelMapping != nil {
		builder = builder.SetModelMapping(key.ModelMapping)
	}
	if key.ParamOverride != nil {
		builder = builder.SetParamOverride(key.ParamOverride)
	}
	if key.HeaderOverride != nil {
		builder = builder.SetHeaderOverride(key.HeaderOverride)
	}
	if key.Status != nil {
		builder = builder.SetStatus(entchannelkey.Status(*key.Status))
	}
	if key.Tags != nil {
		builder = builder.SetTags(key.Tags)
	}
	if key.TestModel != nil {
		builder = builder.SetTestModel(*key.TestModel)
	}
	if len(key.GroupIDs) > 0 {
		builder = builder.AddGroupIDs(key.GroupIDs...)
	}
	return builder
}

// applyKeyUpdate 把 KeyInput 应用到 ChannelKey 更新构建器（partial）。
// APIKey 空串 = 保持原密钥；Models/映射/覆写/Tags/GroupIDs 非 nil = 整组替换。
func applyKeyUpdate(builder *ent.ChannelKeyUpdateOne, key appchannel.KeyInput) *ent.ChannelKeyUpdateOne {
	builder = builder.
		SetNillablePriority(key.Priority).
		SetNillableWeight(key.Weight).
		SetNillableMaxConcurrency(key.MaxConcurrency).
		SetNillableMaxRpm(key.MaxRPM).
		SetNillableCostRatio(key.CostRatio).
		SetNillableBalanceCheckEnabled(key.BalanceCheckEnabled)
	// name 为可选标签：空串视为不改（单 key 更新路径可能不带 name）。
	if key.Name != "" {
		builder = builder.SetName(key.Name)
	}
	if key.Type != "" {
		builder = builder.SetType(entchannelkey.Type(key.Type))
	}
	if key.APIKey != "" {
		builder = builder.SetAPIKey(key.APIKey)
	}
	if key.Models != nil {
		builder = builder.SetModels(key.Models)
	}
	if key.ModelMapping != nil {
		builder = builder.SetModelMapping(key.ModelMapping)
	}
	if key.ParamOverride != nil {
		builder = builder.SetParamOverride(key.ParamOverride)
	}
	if key.HeaderOverride != nil {
		builder = builder.SetHeaderOverride(key.HeaderOverride)
	}
	if key.Tags != nil {
		builder = builder.SetTags(key.Tags)
	}
	if key.TestModel != nil {
		builder = builder.SetTestModel(*key.TestModel)
	}
	// 手动重新启用时清理上一轮状态残留（错误信息）。
	if key.Status != nil {
		builder = builder.SetStatus(entchannelkey.Status(*key.Status))
		if *key.Status == appchannel.StatusEnabled {
			builder = builder.SetErrorMsg("")
		}
	}
	if key.GroupIDs != nil {
		builder = builder.ClearGroups().AddGroupIDs(key.GroupIDs...)
	}
	return builder
}

// UpdateKey 单把密钥端点 partial 更新（模型弹窗等按 key 编辑用）。
func (s *ChannelStore) UpdateKey(ctx context.Context, keyID int, key appchannel.KeyInput) (appchannel.ChannelKey, error) {
	if err := applyKeyUpdate(s.db.ChannelKey.UpdateOneID(keyID), key).Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return appchannel.ChannelKey{}, appchannel.ErrChannelNotFound
		}
		if ent.IsConstraintError(err) {
			return appchannel.ChannelKey{}, appchannel.ErrInvalidReference
		}
		return appchannel.ChannelKey{}, err
	}
	return s.FindKeyByID(ctx, keyID)
}

// Delete 删除渠道（级联删除其 key）。
func (s *ChannelStore) Delete(ctx context.Context, id int) error {
	if _, err := s.db.ChannelKey.Delete().Where(
		entchannelkey.HasChannelWith(entchannel.IDEQ(id)),
	).Exec(ctx); err != nil {
		return err
	}
	if err := s.db.Channel.DeleteOneID(id).Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return appchannel.ErrChannelNotFound
		}
		return err
	}
	return nil
}

// BulkUpdate 批量启停/删除/调优先级：enable/disable/set_priority 作用于选中渠道下的
// 全部 key；delete 删渠道（级联删 key）。返回受影响行数。
func (s *ChannelStore) BulkUpdate(ctx context.Context, input appchannel.BulkUpdateInput) (int, error) {
	keysOfChannels := entchannelkey.HasChannelWith(entchannel.IDIn(input.IDs...))
	switch input.Action {
	case appchannel.BulkActionEnable:
		return s.db.ChannelKey.Update().
			Where(keysOfChannels).
			SetStatus(entchannelkey.StatusEnabled).
			SetErrorMsg("").
			Save(ctx)
	case appchannel.BulkActionDisable:
		return s.db.ChannelKey.Update().
			Where(keysOfChannels).
			SetStatus(entchannelkey.StatusDisabledManual).
			Save(ctx)
	case appchannel.BulkActionDelete:
		if _, err := s.db.ChannelKey.Delete().Where(keysOfChannels).Exec(ctx); err != nil {
			return 0, err
		}
		return s.db.Channel.Delete().
			Where(entchannel.IDIn(input.IDs...)).
			Exec(ctx)
	case appchannel.BulkActionSetPriority:
		if input.Priority == nil {
			return 0, appchannel.ErrInvalidBulkAction
		}
		return s.db.ChannelKey.Update().
			Where(keysOfChannels).
			SetPriority(*input.Priority).
			Save(ctx)
	default:
		return 0, appchannel.ErrInvalidBulkAction
	}
}

// UpdateKeyState 更新密钥端点调度状态（status / error_msg）。
func (s *ChannelStore) UpdateKeyState(ctx context.Context, keyID int, status string, errMsg string) error {
	if err := s.db.ChannelKey.UpdateOneID(keyID).
		SetStatus(entchannelkey.Status(status)).
		SetErrorMsg(errMsg).
		Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return appchannel.ErrChannelNotFound
		}
		return err
	}
	return nil
}

// UpdateKeyTestResult 记录密钥端点测试结果（响应耗时与测试时间）。
func (s *ChannelStore) UpdateKeyTestResult(ctx context.Context, keyID int, responseTimeMs int, testedAt time.Time) error {
	if err := s.db.ChannelKey.UpdateOneID(keyID).
		SetResponseTimeMs(responseTimeMs).
		SetTestedAt(testedAt).
		Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return appchannel.ErrChannelNotFound
		}
		return err
	}
	return nil
}

// UpdateKeyBalance 记录密钥端点余额刷新结果（key 级）。
func (s *ChannelStore) UpdateKeyBalance(ctx context.Context, keyID int, balance float64, updatedAt time.Time) error {
	if err := s.db.ChannelKey.UpdateOneID(keyID).
		SetBalance(balance).
		SetBalanceUpdatedAt(updatedAt).
		Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return appchannel.ErrChannelNotFound
		}
		return err
	}
	return nil
}

// channelKeyLatencyWindow 密钥端点平均首字延迟的聚合窗口：最近 5 分钟，接近实时观测。
const channelKeyLatencyWindow = 5 * time.Minute

// GetChannelKeyMoneyStats 按密钥端点聚合金额与延迟（实现 appchannel.StatsReader）：
// 成本 = Σ(total_cost × account_rate_multiplier)，收益 = Σ(actual_cost)；
// 累计与今日两个口径分两次分组聚合，方言无关；AvgFirstTokenMs 另按最近 5 分钟窗口聚合。
func (s *ChannelStore) GetChannelKeyMoneyStats(ctx context.Context, channelKeyIDs []int, todayStart time.Time) (map[int]appchannel.MoneyStats, error) {
	result := make(map[int]appchannel.MoneyStats, len(channelKeyIDs))
	if len(channelKeyIDs) == 0 {
		return result, nil
	}
	total, err := s.sumChannelKeyMoney(ctx, channelKeyIDs)
	if err != nil {
		return nil, err
	}
	today, err := s.sumChannelKeyMoney(ctx, channelKeyIDs, entusagelog.CreatedAtGTE(todayStart))
	if err != nil {
		return nil, err
	}
	latency, err := s.avgChannelKeyFirstTokenMs(ctx, channelKeyIDs, time.Now().Add(-channelKeyLatencyWindow))
	if err != nil {
		return nil, err
	}
	for id, item := range total {
		result[id] = appchannel.MoneyStats{
			Cost:            item.Cost,
			Revenue:         item.Revenue,
			TodayCost:       today[id].Cost,
			TodayRevenue:    today[id].Revenue,
			AvgFirstTokenMs: latency[id],
		}
	}
	return result, nil
}

// channelMoneyRow 金额分组聚合的单口径结果。
type channelMoneyRow struct {
	KeyID   int     `json:"channel_key_usage_logs"`
	Cost    float64 `json:"cost"`
	Revenue float64 `json:"revenue"`
}

func (s *ChannelStore) sumChannelKeyMoney(ctx context.Context, channelKeyIDs []int, extra ...predicate.UsageLog) (map[int]channelMoneyRow, error) {
	var rows []channelMoneyRow
	preds := append([]predicate.UsageLog{entusagelog.ChannelKeyIDIn(channelKeyIDs...)}, extra...)
	err := s.db.UsageLog.Query().
		Where(preds...).
		GroupBy(entusagelog.ChannelKeyColumn).
		Aggregate(
			ent.As(func(sel *sql.Selector) string {
				return "COALESCE(SUM(" + sel.C(entusagelog.FieldTotalCost) + " * " + sel.C(entusagelog.FieldAccountRateMultiplier) + "), 0)"
			}, "cost"),
			ent.As(func(sel *sql.Selector) string {
				return "COALESCE(SUM(" + sel.C(entusagelog.FieldActualCost) + "), 0)"
			}, "revenue"),
		).
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	result := make(map[int]channelMoneyRow, len(rows))
	for _, row := range rows {
		result[row.KeyID] = row
	}
	return result, nil
}

// channelLatencyRow 首字延迟分组聚合的单条结果：Sum/Requests 分别为窗口内首字延迟总和与样本数
// （复用 dashboard_store.go 的 usageLogFirstTokenCondition 口径：非图像 + first_token_ms > 0）。
type channelLatencyRow struct {
	KeyID    int   `json:"channel_key_usage_logs"`
	Sum      int64 `json:"first_token_ms_sum"`
	Requests int64 `json:"first_token_requests"`
}

// avgChannelKeyFirstTokenMs 按密钥端点聚合 windowStart 以来的平均首字延迟（ms）；
// 窗口内无有效样本（无请求或均非流式）的 key 不出现在返回 map 中。
func (s *ChannelStore) avgChannelKeyFirstTokenMs(ctx context.Context, channelKeyIDs []int, windowStart time.Time) (map[int]float64, error) {
	var rows []channelLatencyRow
	err := s.db.UsageLog.Query().
		Where(entusagelog.ChannelKeyIDIn(channelKeyIDs...), entusagelog.CreatedAtGTE(windowStart)).
		GroupBy(entusagelog.ChannelKeyColumn).
		Aggregate(
			ent.As(usageLogSumIf(usageLogFirstTokenCondition, entusagelog.FieldFirstTokenMs), "first_token_ms_sum"),
			ent.As(usageLogCountIf(usageLogFirstTokenCondition), "first_token_requests"),
		).
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	result := make(map[int]float64, len(rows))
	for _, row := range rows {
		if row.Requests > 0 {
			result[row.KeyID] = float64(row.Sum) / float64(row.Requests)
		}
	}
	return result, nil
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
		ID:        item.ID,
		Name:      item.Name,
		BaseURL:   item.BaseURL,
		CreatedAt: item.CreatedAt,
		UpdatedAt: item.UpdatedAt,
	}
	if keys, err := item.Edges.KeysOrErr(); err == nil {
		ch.Keys = make([]appchannel.ChannelKey, 0, len(keys))
		for _, k := range keys {
			mk := mapChannelKey(k, item.BaseURL, item.Name)
			mk.ChannelID = item.ID
			ch.Keys = append(ch.Keys, mk)
		}
	}
	return ch
}

func mapChannelKey(item *ent.ChannelKey, baseURL, channelName string) appchannel.ChannelKey {
	key := appchannel.ChannelKey{
		ID:                  item.ID,
		ChannelName:         channelName,
		BaseURL:             baseURL,
		Name:                item.Name,
		Type:                item.Type.String(),
		APIKey:              item.APIKey,
		Models:              item.Models,
		ModelMapping:        item.ModelMapping,
		ParamOverride:       item.ParamOverride,
		HeaderOverride:      item.HeaderOverride,
		Status:              item.Status.String(),
		ErrorMsg:            item.ErrorMsg,
		Priority:            item.Priority,
		Weight:              item.Weight,
		MaxConcurrency:      item.MaxConcurrency,
		MaxRPM:              item.MaxRpm,
		CostRatio:           item.CostRatio,
		Tags:                item.Tags,
		TestModel:           item.TestModel,
		ResponseTimeMs:      item.ResponseTimeMs,
		TestedAt:            item.TestedAt,
		LastUsedAt:          item.LastUsedAt,
		Balance:             item.Balance,
		BalanceUpdatedAt:    item.BalanceUpdatedAt,
		BalanceCheckEnabled: item.BalanceCheckEnabled,
		CreatedAt:           item.CreatedAt,
		UpdatedAt:           item.UpdatedAt,
	}
	if ch, err := item.Edges.ChannelOrErr(); err == nil {
		key.ChannelID = ch.ID
	}
	if groups, err := item.Edges.GroupsOrErr(); err == nil {
		key.GroupIDs = make([]int, 0, len(groups))
		for _, g := range groups {
			key.GroupIDs = append(key.GroupIDs, g.ID)
		}
	}
	return key
}
