package store

import (
	"context"
	"sort"
	"time"

	"entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"

	"github.com/DouDOU-start/airgate-core/ent"
	entchannel "github.com/DouDOU-start/airgate-core/ent/channel"
	entcredential "github.com/DouDOU-start/airgate-core/ent/channelcredential"
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
		kq.WithGroups().WithCredential(func(cq *ent.ChannelCredentialQuery) {
			cq.WithKeys()
		}).Order(ent.Asc(entchannelkey.FieldID))
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
		status := filter.Status
		if status == appchannel.StatusEnabled {
			query = query.Where(entchannel.HasKeysWith(
				entchannelkey.StatusEQ(entchannelkey.StatusEnabled),
				entchannelkey.Or(
					entchannelkey.CredentialIDIsNil(),
					entchannelkey.HasCredentialWith(entcredential.StatusEQ(entcredential.StatusEnabled)),
				),
			))
		} else {
			query = query.Where(entchannel.HasKeysWith(entchannelkey.Or(
				entchannelkey.StatusEQ(entchannelkey.Status(status)),
				entchannelkey.HasCredentialWith(entcredential.StatusEQ(entcredential.Status(status))),
			)))
		}
	}
	if filter.Tag != "" {
		tag := filter.Tag
		query = query.Where(entchannel.Or(
			entchannel.HasKeysWith(predicate.ChannelKey(func(selector *sql.Selector) {
				selector.Where(sqljson.ValueContains(entchannelkey.FieldTags, tag))
			})),
			entchannel.HasCredentialsWith(predicate.ChannelCredential(func(selector *sql.Selector) {
				selector.Where(sqljson.ValueContains(entcredential.FieldTags, tag))
			})),
		))
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
			entchannelkey.HasCredentialWith(entcredential.NameContains(filter.Keyword)),
			entchannelkey.HasChannelWith(entchannel.NameContains(filter.Keyword)),
		))
	}
	if filter.Type != "" {
		query = query.Where(entchannelkey.TypeEQ(entchannelkey.Type(filter.Type)))
	}
	if filter.Status != "" {
		if filter.Status == appchannel.StatusEnabled {
			query = query.Where(
				entchannelkey.StatusEQ(entchannelkey.StatusEnabled),
				entchannelkey.Or(
					entchannelkey.CredentialIDIsNil(),
					entchannelkey.HasCredentialWith(entcredential.StatusEQ(entcredential.StatusEnabled)),
				),
			)
		} else {
			query = query.Where(entchannelkey.Or(
				entchannelkey.StatusEQ(entchannelkey.Status(filter.Status)),
				entchannelkey.HasCredentialWith(entcredential.StatusEQ(entcredential.Status(filter.Status))),
			))
		}
	}
	if filter.Tag != "" {
		tag := filter.Tag
		query = query.Where(entchannelkey.Or(
			predicate.ChannelKey(func(selector *sql.Selector) {
				selector.Where(sqljson.ValueContains(entchannelkey.FieldTags, tag))
			}),
			entchannelkey.HasCredentialWith(predicate.ChannelCredential(func(selector *sql.Selector) {
				selector.Where(sqljson.ValueContains(entcredential.FieldTags, tag))
			})),
		))
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
		WithCredential(func(cq *ent.ChannelCredentialQuery) { cq.WithKeys() }).
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
		WithCredential(func(cq *ent.ChannelCredentialQuery) { cq.WithKeys() }).
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

// CreateKey 在指定渠道下新增一条物理凭证，并为所选协议分别创建端点。
func (s *ChannelStore) CreateKey(ctx context.Context, channelID int, key appchannel.KeyInput) (appchannel.ChannelKey, error) {
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return appchannel.ChannelKey{}, err
	}
	defer func() { _ = tx.Rollback() }()

	credential, err := applyCredentialCreate(tx.ChannelCredential.Create().SetChannelID(channelID), key).Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return appchannel.ChannelKey{}, appchannel.ErrInvalidReference
		}
		return appchannel.ChannelKey{}, err
	}

	var firstID int
	for _, channelType := range requestedTypes(key) {
		endpointInput := key
		endpointInput.Type = channelType
		item, err := applyKeyCreate(tx.ChannelKey.Create().
			SetChannelID(channelID).
			SetCredentialID(credential.ID), endpointInput).Save(ctx)
		if err != nil {
			if ent.IsConstraintError(err) {
				return appchannel.ChannelKey{}, appchannel.ErrInvalidReference
			}
			return appchannel.ChannelKey{}, err
		}
		if firstID == 0 {
			firstID = item.ID
		}
	}
	if err := tx.Commit(); err != nil {
		return appchannel.ChannelKey{}, err
	}
	return s.FindKeyByID(ctx, firstID)
}

// DeleteKey 删除一个协议端点；凭证已无任何端点时一并删除凭证。
func (s *ChannelStore) DeleteKey(ctx context.Context, keyID int) error {
	item, err := s.db.ChannelKey.Get(ctx, keyID)
	if err != nil {
		if ent.IsNotFound(err) {
			return appchannel.ErrChannelNotFound
		}
		return err
	}
	if err := s.db.ChannelKey.DeleteOneID(keyID).Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return appchannel.ErrChannelNotFound
		}
		return err
	}
	if item.CredentialID == nil {
		return nil
	}
	remaining, err := s.db.ChannelKey.Query().Where(entchannelkey.CredentialIDEQ(*item.CredentialID)).Count(ctx)
	if err != nil {
		return err
	}
	if remaining == 0 {
		if err := s.db.ChannelCredential.DeleteOneID(*item.CredentialID).Exec(ctx); err != nil && !ent.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func requestedTypes(key appchannel.KeyInput) []string {
	if key.Types != nil {
		return key.Types
	}
	if key.Type == "" {
		return nil
	}
	return []string{key.Type}
}

// applyCredentialCreate 把共享配置应用到物理凭证创建构建器（APIKey 为密文）。
func applyCredentialCreate(builder *ent.ChannelCredentialCreate, key appchannel.KeyInput) *ent.ChannelCredentialCreate {
	status := key.CredentialStatus
	if status == nil {
		status = key.Status
	}
	builder = builder.
		SetName(key.Name).
		SetAPIKey(key.APIKey).
		SetNillableMaxConcurrency(key.MaxConcurrency).
		SetNillableMaxRpm(key.MaxRPM).
		SetNillableCostRatio(key.CostRatio).
		SetNillableBalanceCheckEnabled(key.BalanceCheckEnabled).
		SetNillableUpstreamRateEnabled(key.UpstreamRateEnabled).
		SetNillableUseUpstreamRateForCost(key.UseUpstreamRateForCost)
	if status != nil {
		builder = builder.SetStatus(entcredential.Status(*status))
	}
	if key.Tags != nil {
		builder = builder.SetTags(key.Tags)
	}
	if key.UpstreamRatePath != nil {
		builder = builder.SetUpstreamRatePath(*key.UpstreamRatePath)
	}
	return builder
}

// applyCredentialUpdate 更新物理凭证共享配置；空 APIKey 表示保持现有密钥。
func applyCredentialUpdate(builder *ent.ChannelCredentialUpdateOne, key appchannel.KeyInput) *ent.ChannelCredentialUpdateOne {
	builder = builder.
		SetNillableMaxConcurrency(key.MaxConcurrency).
		SetNillableMaxRpm(key.MaxRPM).
		SetNillableCostRatio(key.CostRatio).
		SetNillableBalanceCheckEnabled(key.BalanceCheckEnabled).
		SetNillableUpstreamRateEnabled(key.UpstreamRateEnabled).
		SetNillableUseUpstreamRateForCost(key.UseUpstreamRateForCost)
	if key.Name != "" {
		builder = builder.SetName(key.Name)
	}
	if key.APIKey != "" {
		builder = builder.SetAPIKey(key.APIKey)
	}
	if key.CredentialStatus != nil {
		builder = builder.SetStatus(entcredential.Status(*key.CredentialStatus))
		if *key.CredentialStatus == appchannel.StatusEnabled {
			builder = builder.SetErrorMsg("")
		}
	}
	if key.Tags != nil {
		builder = builder.SetTags(key.Tags)
	}
	if key.UpstreamRatePath != nil {
		builder = builder.SetUpstreamRatePath(*key.UpstreamRatePath)
	}
	return builder
}

// applyKeyCreate 把协议专属配置应用到 ChannelKey 创建构建器。
func applyKeyCreate(builder *ent.ChannelKeyCreate, key appchannel.KeyInput) *ent.ChannelKeyCreate {
	models := key.Models
	if models == nil {
		models = []string{}
	}
	builder = builder.
		SetName(key.Name).
		SetType(entchannelkey.Type(key.Type)).
		SetModels(models).
		SetNillablePriority(key.Priority).
		SetNillableWeight(key.Weight).
		SetNillableProbeEnabled(key.ProbeEnabled)
	if key.ProbeModel != nil {
		builder = builder.SetProbeModel(*key.ProbeModel)
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
	if key.Status != nil {
		builder = builder.SetStatus(entchannelkey.Status(*key.Status))
	}
	if key.TestModel != nil {
		builder = builder.SetTestModel(*key.TestModel)
	}
	if len(key.GroupIDs) > 0 {
		builder = builder.AddGroupIDs(key.GroupIDs...)
	}
	return builder
}

// applyKeyUpdate 把协议专属配置应用到 ChannelKey 更新构建器。
func applyKeyUpdate(builder *ent.ChannelKeyUpdateOne, key appchannel.KeyInput) *ent.ChannelKeyUpdateOne {
	builder = builder.
		SetNillablePriority(key.Priority).
		SetNillableWeight(key.Weight).
		SetNillableProbeEnabled(key.ProbeEnabled)
	if key.ProbeModel != nil {
		builder = builder.SetProbeModel(*key.ProbeModel)
	}
	if key.Type != "" {
		builder = builder.SetType(entchannelkey.Type(key.Type))
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
	if key.TestModel != nil {
		builder = builder.SetTestModel(*key.TestModel)
	}
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

// UpdateKey 更新当前协议端点和共享凭证；Types 非 nil 时同步该凭证的完整协议集合。
func (s *ChannelStore) UpdateKey(ctx context.Context, keyID int, key appchannel.KeyInput) (appchannel.ChannelKey, error) {
	current, err := s.db.ChannelKey.Query().Where(entchannelkey.IDEQ(keyID)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return appchannel.ChannelKey{}, appchannel.ErrChannelNotFound
		}
		return appchannel.ChannelKey{}, err
	}
	if current.CredentialID == nil {
		return appchannel.ChannelKey{}, appchannel.ErrInvalidReference
	}
	channelID, err := current.QueryChannel().OnlyID(ctx)
	if err != nil {
		return appchannel.ChannelKey{}, err
	}

	tx, err := s.db.Tx(ctx)
	if err != nil {
		return appchannel.ChannelKey{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := applyCredentialUpdate(tx.ChannelCredential.UpdateOneID(*current.CredentialID), key).Exec(ctx); err != nil {
		return appchannel.ChannelKey{}, err
	}
	if key.Name != "" {
		if _, err := tx.ChannelKey.Update().
			Where(entchannelkey.CredentialIDEQ(*current.CredentialID)).
			SetName(key.Name).
			Save(ctx); err != nil {
			return appchannel.ChannelKey{}, err
		}
	}
	if err := applyKeyUpdate(tx.ChannelKey.UpdateOneID(keyID), key).Exec(ctx); err != nil {
		if ent.IsConstraintError(err) {
			return appchannel.ChannelKey{}, appchannel.ErrInvalidReference
		}
		return appchannel.ChannelKey{}, err
	}

	resultID := keyID
	if key.Types != nil {
		existing, err := tx.ChannelKey.Query().Where(entchannelkey.CredentialIDEQ(*current.CredentialID)).All(ctx)
		if err != nil {
			return appchannel.ChannelKey{}, err
		}
		byType := make(map[string]*ent.ChannelKey, len(existing))
		for _, item := range existing {
			byType[item.Type.String()] = item
		}
		desired := make(map[string]struct{}, len(key.Types))
		for _, channelType := range key.Types {
			desired[channelType] = struct{}{}
			if _, ok := byType[channelType]; ok {
				continue
			}
			endpointInput := key
			endpointInput.Type = channelType
			endpointInput.Status = nil
			created, err := applyKeyCreate(tx.ChannelKey.Create().
				SetChannelID(channelID).
				SetCredentialID(*current.CredentialID), endpointInput).Save(ctx)
			if err != nil {
				return appchannel.ChannelKey{}, err
			}
			byType[channelType] = created
		}
		for _, item := range existing {
			if _, keep := desired[item.Type.String()]; keep {
				continue
			}
			if err := tx.ChannelKey.DeleteOneID(item.ID).Exec(ctx); err != nil {
				return appchannel.ChannelKey{}, err
			}
		}
		if _, keep := desired[current.Type.String()]; !keep {
			for _, channelType := range key.Types {
				if item := byType[channelType]; item != nil {
					resultID = item.ID
					break
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return appchannel.ChannelKey{}, err
	}
	return s.FindKeyByID(ctx, resultID)
}

// Delete 删除渠道（级联删除其 key）。
func (s *ChannelStore) Delete(ctx context.Context, id int) error {
	if _, err := s.db.ChannelKey.Delete().Where(
		entchannelkey.HasChannelWith(entchannel.IDEQ(id)),
	).Exec(ctx); err != nil {
		return err
	}
	if _, err := s.db.ChannelCredential.Delete().Where(entcredential.ChannelIDEQ(id)).Exec(ctx); err != nil {
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
	credentialsOfChannels := entcredential.ChannelIDIn(input.IDs...)
	switch input.Action {
	case appchannel.BulkActionEnable:
		if _, err := s.db.ChannelCredential.Update().
			Where(credentialsOfChannels).
			SetStatus(entcredential.StatusEnabled).
			SetErrorMsg("").
			Save(ctx); err != nil {
			return 0, err
		}
		return s.db.ChannelKey.Update().
			Where(keysOfChannels).
			SetStatus(entchannelkey.StatusEnabled).
			SetErrorMsg("").
			Save(ctx)
	case appchannel.BulkActionDisable:
		if _, err := s.db.ChannelCredential.Update().
			Where(credentialsOfChannels).
			SetStatus(entcredential.StatusDisabledManual).
			Save(ctx); err != nil {
			return 0, err
		}
		return s.db.ChannelKey.Update().
			Where(keysOfChannels).
			SetStatus(entchannelkey.StatusDisabledManual).
			Save(ctx)
	case appchannel.BulkActionDelete:
		if _, err := s.db.ChannelKey.Delete().Where(keysOfChannels).Exec(ctx); err != nil {
			return 0, err
		}
		if _, err := s.db.ChannelCredential.Delete().Where(credentialsOfChannels).Exec(ctx); err != nil {
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

// UpdateCredentialState 更新物理凭证整体状态（status / error_msg）。
func (s *ChannelStore) UpdateCredentialState(ctx context.Context, credentialID int, status string, errMsg string) error {
	if err := s.db.ChannelCredential.UpdateOneID(credentialID).
		SetStatus(entcredential.Status(status)).
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

// UpdateKeyBalance 通过协议端点定位物理凭证并记录共享余额。
func (s *ChannelStore) UpdateKeyBalance(ctx context.Context, keyID int, balance float64, updatedAt time.Time) error {
	credentialID, err := s.credentialIDForKey(ctx, keyID)
	if err != nil {
		return err
	}
	if err := s.db.ChannelCredential.UpdateOneID(credentialID).
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

func (s *ChannelStore) credentialIDForKey(ctx context.Context, keyID int) (int, error) {
	item, err := s.db.ChannelKey.Query().Where(entchannelkey.IDEQ(keyID)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return 0, appchannel.ErrChannelNotFound
		}
		return 0, err
	}
	if item.CredentialID == nil {
		return 0, appchannel.ErrInvalidReference
	}
	return *item.CredentialID, nil
}

// ---- 健康探针持久化 ----

// UpdateKeyHealthState 更新密钥端点健康状态与计数器。
func (s *ChannelStore) UpdateKeyHealthState(ctx context.Context, keyID int, health string, failures, successes int) error {
	if err := s.db.ChannelKey.UpdateOneID(keyID).
		SetHealthStatus(entchannelkey.HealthStatus(health)).
		SetConsecutiveFailures(failures).
		SetConsecutiveSuccesses(successes).
		Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return appchannel.ErrChannelNotFound
		}
		return err
	}
	return nil
}

// UpdateKeyProbeTime 记录最近一次探针执行时间。
func (s *ChannelStore) UpdateKeyProbeTime(ctx context.Context, keyID int, at time.Time) error {
	if err := s.db.ChannelKey.UpdateOneID(keyID).
		SetLastProbeAt(at).
		Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return appchannel.ErrChannelNotFound
		}
		return err
	}
	return nil
}

// ListProbeEnabledKeys 查询所有 probe_enabled=true 的 key 的健康快照。
// 必须包含 disabled_auto 的 key/凭证：自动禁用正是探针要救回的对象，
// 只查 enabled 会让被判死的 key 永远退出探测集（手动禁用仍排除，不自动恢复）。
func (s *ChannelStore) ListProbeEnabledKeys(ctx context.Context) ([]appchannel.KeyHealthSnapshot, error) {
	keys, err := s.db.ChannelKey.Query().
		Where(
			entchannelkey.ProbeEnabled(true),
			entchannelkey.StatusIn(entchannelkey.StatusEnabled, entchannelkey.StatusDisabledAuto),
			entchannelkey.Or(
				entchannelkey.CredentialIDIsNil(),
				entchannelkey.HasCredentialWith(entcredential.StatusIn(entcredential.StatusEnabled, entcredential.StatusDisabledAuto)),
			),
		).
		Select(
			entchannelkey.FieldID,
			entchannelkey.FieldHealthStatus,
			entchannelkey.FieldConsecutiveFailures,
			entchannelkey.FieldConsecutiveSuccesses,
			entchannelkey.FieldLastProbeAt,
		).
		All(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]appchannel.KeyHealthSnapshot, len(keys))
	for i, k := range keys {
		result[i] = appchannel.KeyHealthSnapshot{
			KeyID:                k.ID,
			HealthStatus:         k.HealthStatus.String(),
			ConsecutiveFailures:  k.ConsecutiveFailures,
			ConsecutiveSuccesses: k.ConsecutiveSuccesses,
			LastProbeAt:          k.LastProbeAt,
		}
	}
	return result, nil
}

// ListBalanceSyncTargets 查询余额过期的已启用物理凭证，并为每条凭证返回一个可用协议端点 ID。
func (s *ChannelStore) ListBalanceSyncTargets(ctx context.Context, staleBefore time.Time) ([]int, error) {
	credentials, err := s.db.ChannelCredential.Query().
		Where(
			entcredential.BalanceCheckEnabled(true),
			entcredential.StatusEQ(entcredential.StatusEnabled),
			entcredential.Or(
				entcredential.BalanceUpdatedAtIsNil(),
				entcredential.BalanceUpdatedAtLT(staleBefore),
			),
		).
		WithKeys(func(q *ent.ChannelKeyQuery) {
			q.Where(entchannelkey.StatusEQ(entchannelkey.StatusEnabled)).Order(ent.Asc(entchannelkey.FieldID))
		}).
		All(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]int, 0, len(credentials))
	for _, credential := range credentials {
		if keys, err := credential.Edges.KeysOrErr(); err == nil && len(keys) > 0 {
			ids = append(ids, keys[0].ID)
		}
	}
	legacyIDs, err := s.db.ChannelKey.Query().Where(
		entchannelkey.CredentialIDIsNil(),
		entchannelkey.BalanceCheckEnabled(true),
		entchannelkey.StatusEQ(entchannelkey.StatusEnabled),
		entchannelkey.Or(
			entchannelkey.BalanceUpdatedAtIsNil(),
			entchannelkey.BalanceUpdatedAtLT(staleBefore),
		),
	).IDs(ctx)
	if err != nil {
		return nil, err
	}
	ids = append(ids, legacyIDs...)
	return ids, nil
}

// ListUpstreamRateTargets 查询启用倍率探测的物理凭证，每条凭证只返回一个协议端点。
func (s *ChannelStore) ListUpstreamRateTargets(ctx context.Context) ([]appchannel.UpstreamRateTarget, error) {
	credentials, err := s.db.ChannelCredential.Query().
		Where(
			entcredential.UpstreamRateEnabled(true),
			entcredential.StatusEQ(entcredential.StatusEnabled),
		).
		WithChannel().
		WithKeys(func(q *ent.ChannelKeyQuery) {
			q.Where(entchannelkey.StatusEQ(entchannelkey.StatusEnabled)).Order(ent.Asc(entchannelkey.FieldID))
		}).
		All(ctx)
	if err != nil {
		return nil, err
	}
	targets := make([]appchannel.UpstreamRateTarget, 0, len(credentials))
	for _, credential := range credentials {
		keys, edgeErr := credential.Edges.KeysOrErr()
		if edgeErr != nil || len(keys) == 0 {
			continue
		}
		baseURL := ""
		if ch, e := credential.Edges.ChannelOrErr(); e == nil {
			baseURL = ch.BaseURL
		}
		targets = append(targets, appchannel.UpstreamRateTarget{
			KeyID:            keys[0].ID,
			BaseURL:          baseURL,
			APIKeyCipher:     credential.APIKey,
			UpstreamRatePath: credential.UpstreamRatePath,
		})
	}
	legacyKeys, err := s.db.ChannelKey.Query().Where(
		entchannelkey.CredentialIDIsNil(),
		entchannelkey.UpstreamRateEnabled(true),
		entchannelkey.StatusEQ(entchannelkey.StatusEnabled),
	).WithChannel().All(ctx)
	if err != nil {
		return nil, err
	}
	for _, key := range legacyKeys {
		baseURL := ""
		if ch, edgeErr := key.Edges.ChannelOrErr(); edgeErr == nil {
			baseURL = ch.BaseURL
		}
		targets = append(targets, appchannel.UpstreamRateTarget{
			KeyID:            key.ID,
			BaseURL:          baseURL,
			APIKeyCipher:     key.APIKey,
			UpstreamRatePath: key.UpstreamRatePath,
		})
	}
	return targets, nil
}

// UpdateUpstreamRate 更新密钥端点的上游倍率探测结果。
func (s *ChannelStore) UpdateUpstreamRate(ctx context.Context, keyID int, rate float64, at time.Time) error {
	credentialID, err := s.credentialIDForKey(ctx, keyID)
	if err != nil {
		return err
	}
	return s.db.ChannelCredential.UpdateOneID(credentialID).
		SetUpstreamRate(rate).
		SetUpstreamRateAt(at).
		Exec(ctx)
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
		ID:                     item.ID,
		ChannelName:            channelName,
		BaseURL:                baseURL,
		Name:                   item.Name,
		Type:                   item.Type.String(),
		APIKey:                 item.APIKey,
		Models:                 item.Models,
		ModelMapping:           item.ModelMapping,
		ParamOverride:          item.ParamOverride,
		HeaderOverride:         item.HeaderOverride,
		Status:                 item.Status.String(),
		ErrorMsg:               item.ErrorMsg,
		Priority:               item.Priority,
		Weight:                 item.Weight,
		MaxConcurrency:         item.MaxConcurrency,
		MaxRPM:                 item.MaxRpm,
		CostRatio:              item.CostRatio,
		Tags:                   item.Tags,
		TestModel:              item.TestModel,
		ResponseTimeMs:         item.ResponseTimeMs,
		TestedAt:               item.TestedAt,
		LastUsedAt:             item.LastUsedAt,
		Balance:                item.Balance,
		BalanceUpdatedAt:       item.BalanceUpdatedAt,
		BalanceCheckEnabled:    item.BalanceCheckEnabled,
		ProbeEnabled:           item.ProbeEnabled,
		ProbeModel:             item.ProbeModel,
		HealthStatus:           item.HealthStatus.String(),
		ConsecutiveFailures:    item.ConsecutiveFailures,
		ConsecutiveSuccesses:   item.ConsecutiveSuccesses,
		LastProbeAt:            item.LastProbeAt,
		UpstreamRateEnabled:    item.UpstreamRateEnabled,
		UpstreamRatePath:       item.UpstreamRatePath,
		UseUpstreamRateForCost: item.UseUpstreamRateForCost,
		UpstreamRate:           item.UpstreamRate,
		UpstreamRateAt:         item.UpstreamRateAt,
		CreatedAt:              item.CreatedAt,
		UpdatedAt:              item.UpdatedAt,
	}
	key.CredentialStatus = key.Status
	if credential, err := item.Edges.CredentialOrErr(); err == nil {
		key.CredentialID = credential.ID
		key.CredentialStatus = credential.Status.String()
		key.CredentialErrorMsg = credential.ErrorMsg
		key.Name = credential.Name
		key.APIKey = credential.APIKey
		key.MaxConcurrency = credential.MaxConcurrency
		key.MaxRPM = credential.MaxRpm
		key.CostRatio = credential.CostRatio
		key.Tags = credential.Tags
		key.Balance = credential.Balance
		key.BalanceUpdatedAt = credential.BalanceUpdatedAt
		key.BalanceCheckEnabled = credential.BalanceCheckEnabled
		key.UpstreamRateEnabled = credential.UpstreamRateEnabled
		key.UpstreamRatePath = credential.UpstreamRatePath
		key.UseUpstreamRateForCost = credential.UseUpstreamRateForCost
		key.UpstreamRate = credential.UpstreamRate
		key.UpstreamRateAt = credential.UpstreamRateAt
		if endpoints, err := credential.Edges.KeysOrErr(); err == nil {
			key.CredentialProtocols = make([]string, 0, len(endpoints))
			seen := make(map[string]struct{}, len(endpoints))
			for _, endpoint := range endpoints {
				channelType := endpoint.Type.String()
				if _, ok := seen[channelType]; ok {
					continue
				}
				seen[channelType] = struct{}{}
				key.CredentialProtocols = append(key.CredentialProtocols, channelType)
			}
			sort.Strings(key.CredentialProtocols)
		}
	}
	if key.CredentialID == 0 {
		key.CredentialID = key.ID
	}
	if len(key.CredentialProtocols) == 0 {
		key.CredentialProtocols = []string{key.Type}
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
