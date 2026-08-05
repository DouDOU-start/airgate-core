package store

import (
	"context"

	"github.com/DouDOU-start/airgate-core/ent"
	entaccount "github.com/DouDOU-start/airgate-core/ent/account"
	entgroup "github.com/DouDOU-start/airgate-core/ent/group"
	entproxy "github.com/DouDOU-start/airgate-core/ent/proxy"
	appaccount "github.com/DouDOU-start/airgate-core/internal/app/account"
)

// AccountStore 使用 Ent 实现账号仓储。
// 只存/取 credentials_enc 与 email，不做加解密。
type AccountStore struct {
	db *ent.Client
}

// NewAccountStore 创建账号仓储。
func NewAccountStore(db *ent.Client) *AccountStore {
	return &AccountStore{db: db}
}

func applyAccountListFilters(query *ent.AccountQuery, filter appaccount.ListFilter) *ent.AccountQuery {
	if filter.Keyword != "" {
		query = query.Where(entaccount.Or(
			entaccount.NameContains(filter.Keyword),
			entaccount.EmailContains(filter.Keyword),
		))
	}
	if filter.Platform != "" {
		query = query.Where(entaccount.PlatformEQ(filter.Platform))
	}
	if filter.State != "" {
		query = query.Where(entaccount.StateEQ(entaccount.State(filter.State)))
	}
	if filter.AccountType != "" {
		query = query.Where(entaccount.TypeEQ(filter.AccountType))
	}
	if filter.GroupID != nil {
		query = query.Where(entaccount.HasGroupsWith(entgroup.IDEQ(*filter.GroupID)))
	} else if filter.Ungrouped {
		query = query.Where(entaccount.Not(entaccount.HasGroups()))
	}
	if filter.ProxyID != nil {
		query = query.Where(entaccount.HasProxyWith(entproxy.IDEQ(*filter.ProxyID)))
	}
	if len(filter.IDs) > 0 {
		query = query.Where(entaccount.IDIn(filter.IDs...))
	}
	return query
}

// List 分页查询账号列表（含 groups / proxy）。
// 运行时指标排序由 service 用 ListIDs+ListByIDs 实现，不在此列。
func (s *AccountStore) List(ctx context.Context, filter appaccount.ListFilter) ([]appaccount.Account, int64, error) {
	query := applyAccountListFilters(s.db.Account.Query(), filter)

	total, err := query.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	items, err := applyAccountListOrder(query, filter).
		WithGroups().
		WithProxy().
		Offset((filter.Page - 1) * filter.PageSize).
		Limit(filter.PageSize).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}
	return mapAccounts(items), int64(total), nil
}

// ListIDs 按筛选条件（忽略分页）返回全部匹配账号 id。
func (s *AccountStore) ListIDs(ctx context.Context, filter appaccount.ListFilter) ([]int, error) {
	return applyAccountListFilters(s.db.Account.Query(), filter).IDs(ctx)
}

// ListByIDs 按 id 批量取账号详情（不保证返回顺序）。
func (s *AccountStore) ListByIDs(ctx context.Context, ids []int) ([]appaccount.Account, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	items, err := s.db.Account.Query().
		Where(entaccount.IDIn(ids...)).
		WithGroups().
		WithProxy().
		All(ctx)
	if err != nil {
		return nil, err
	}
	return mapAccounts(items), nil
}

func applyAccountListOrder(query *ent.AccountQuery, filter appaccount.ListFilter) *ent.AccountQuery {
	asc := filter.SortOrder == "asc"
	switch filter.SortBy {
	case appaccount.SortByPriority:
		if asc {
			return query.Order(ent.Asc(entaccount.FieldPriority), ent.Desc(entaccount.FieldID))
		}
		return query.Order(ent.Desc(entaccount.FieldPriority), ent.Desc(entaccount.FieldID))
	case appaccount.SortByWeight:
		if asc {
			return query.Order(ent.Asc(entaccount.FieldWeight), ent.Desc(entaccount.FieldID))
		}
		return query.Order(ent.Desc(entaccount.FieldWeight), ent.Desc(entaccount.FieldID))
	case appaccount.SortByCreatedAt:
		if asc {
			return query.Order(ent.Asc(entaccount.FieldCreatedAt), ent.Desc(entaccount.FieldID))
		}
		return query.Order(ent.Desc(entaccount.FieldCreatedAt), ent.Desc(entaccount.FieldID))
	default:
		return query.Order(ent.Desc(entaccount.FieldCreatedAt))
	}
}

// ListAll 查询符合筛选的全部账号（不分页，导出用）。
func (s *AccountStore) ListAll(ctx context.Context, filter appaccount.ListFilter) ([]appaccount.Account, error) {
	query := applyAccountListFilters(s.db.Account.Query(), filter)
	items, err := query.
		WithGroups().
		WithProxy().
		Order(ent.Desc(entaccount.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	return mapAccounts(items), nil
}

// Create 创建账号（credentials_enc 已由 service 加密）。
func (s *AccountStore) Create(ctx context.Context, input appaccount.PersistCreateInput) (appaccount.Account, error) {
	builder := s.db.Account.Create().
		SetName(input.Name).
		SetPlatform(input.Platform).
		SetType(input.Type).
		SetCredentialsEnc(input.CredentialsEnc).
		SetEmail(input.Email).
		SetPriority(input.Priority).
		SetWeight(input.Weight).
		SetMaxConcurrency(input.MaxConcurrency).
		SetRateMultiplier(input.RateMultiplier).
		SetUpstreamIsPool(input.UpstreamIsPool)

	if input.Extra != nil {
		builder = builder.SetExtra(input.Extra)
	}
	if len(input.GroupIDs) > 0 {
		builder = builder.AddGroupIDs(toIntSlice(input.GroupIDs)...)
	}
	if input.ProxyID != nil {
		builder = builder.SetProxyID(int(*input.ProxyID))
	}

	item, err := builder.Save(ctx)
	if err != nil {
		return appaccount.Account{}, err
	}
	return s.FindByID(ctx, item.ID, appaccount.LoadOptions{WithGroups: true, WithProxy: true})
}

// Update 更新账号。
func (s *AccountStore) Update(ctx context.Context, id int, input appaccount.PersistUpdateInput) (appaccount.Account, error) {
	builder := s.db.Account.UpdateOneID(id)

	if input.Name != nil {
		builder = builder.SetName(*input.Name)
	}
	if input.Type != nil {
		builder = builder.SetType(*input.Type)
	}
	if input.CredentialsEnc != nil {
		builder = builder.SetCredentialsEnc(*input.CredentialsEnc)
	}
	if input.Email != nil {
		builder = builder.SetEmail(*input.Email)
	}
	if input.State != nil {
		builder = builder.SetState(entaccount.State(*input.State))
	}
	if input.ClearStateUntil {
		builder = builder.ClearStateUntil()
	}
	if input.ClearErrorMsg {
		builder = builder.SetErrorMsg("")
	}
	if input.Priority != nil {
		builder = builder.SetPriority(*input.Priority)
	}
	if input.Weight != nil {
		builder = builder.SetWeight(*input.Weight)
	}
	if input.MaxConcurrency != nil {
		builder = builder.SetMaxConcurrency(*input.MaxConcurrency)
	}
	if input.RateMultiplier != nil {
		builder = builder.SetRateMultiplier(*input.RateMultiplier)
	}
	if input.UpstreamIsPool != nil {
		builder = builder.SetUpstreamIsPool(*input.UpstreamIsPool)
	}
	if input.HasGroupIDs {
		builder = builder.ClearGroups()
		if len(input.GroupIDs) > 0 {
			builder = builder.AddGroupIDs(toIntSlice(input.GroupIDs)...)
		}
	}
	if input.HasProxyID {
		if input.ProxyID == nil {
			builder = builder.ClearProxy()
		} else {
			builder = builder.ClearProxy().SetProxyID(int(*input.ProxyID))
		}
	}
	if input.HasExtra {
		if input.Extra == nil {
			builder = builder.SetExtra(map[string]interface{}{})
		} else {
			builder = builder.SetExtra(input.Extra)
		}
	}

	if _, err := builder.Save(ctx); err != nil {
		if ent.IsNotFound(err) {
			return appaccount.Account{}, appaccount.ErrAccountNotFound
		}
		return appaccount.Account{}, err
	}
	return s.FindByID(ctx, id, appaccount.LoadOptions{WithGroups: true, WithProxy: true})
}

// Delete 删除账号。
func (s *AccountStore) Delete(ctx context.Context, id int) error {
	if err := s.db.Account.DeleteOneID(id).Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return appaccount.ErrAccountNotFound
		}
		return err
	}
	return nil
}

// FindByID 按 ID 查询账号。
func (s *AccountStore) FindByID(ctx context.Context, id int, opts appaccount.LoadOptions) (appaccount.Account, error) {
	query := s.db.Account.Query().Where(entaccount.IDEQ(id))
	if opts.WithGroups {
		query = query.WithGroups()
	}
	if opts.WithProxy {
		query = query.WithProxy()
	}

	item, err := query.Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return appaccount.Account{}, appaccount.ErrAccountNotFound
		}
		return appaccount.Account{}, err
	}
	return mapAccount(item), nil
}

// SaveCredentials 仅更新密文凭证与 email。
func (s *AccountStore) SaveCredentials(ctx context.Context, id int, credentialsEnc, email string) error {
	if err := s.db.Account.UpdateOneID(id).
		SetCredentialsEnc(credentialsEnc).
		SetEmail(email).
		Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return appaccount.ErrAccountNotFound
		}
		return err
	}
	return nil
}

func mapAccounts(items []*ent.Account) []appaccount.Account {
	result := make([]appaccount.Account, 0, len(items))
	for _, item := range items {
		result = append(result, mapAccount(item))
	}
	return result
}

func mapAccount(item *ent.Account) appaccount.Account {
	result := appaccount.Account{
		ID:             item.ID,
		Name:           item.Name,
		Platform:       item.Platform,
		Type:           item.Type,
		CredentialsEnc: item.CredentialsEnc,
		Email:          item.Email,
		State:          item.State.String(),
		Priority:       item.Priority,
		Weight:         item.Weight,
		MaxConcurrency: item.MaxConcurrency,
		RateMultiplier: item.RateMultiplier,
		ErrorMsg:       item.ErrorMsg,
		UpstreamIsPool: item.UpstreamIsPool,
		Extra:          cloneAccountExtra(item.Extra),
		CreatedAt:      item.CreatedAt,
		UpdatedAt:      item.UpdatedAt,
	}

	if item.LastUsedAt != nil {
		value := *item.LastUsedAt
		result.LastUsedAt = &value
	}
	if item.StateUntil != nil {
		value := *item.StateUntil
		result.StateUntil = &value
	}
	if item.Edges.Proxy != nil {
		p := item.Edges.Proxy
		result.Proxy = &appaccount.ProxyRef{
			ID:       p.ID,
			Name:     p.Name,
			Protocol: p.Protocol.String(),
			Address:  p.Address,
			Port:     p.Port,
			Username: p.Username,
			Password: p.Password,
			Status:   p.Status.String(),
		}
	}
	if len(item.Edges.Groups) > 0 {
		result.GroupIDs = make([]int64, 0, len(item.Edges.Groups))
		for _, g := range item.Edges.Groups {
			result.GroupIDs = append(result.GroupIDs, int64(g.ID))
		}
	}

	return result
}

func toIntSlice(values []int64) []int {
	result := make([]int, 0, len(values))
	for _, v := range values {
		result = append(result, int(v))
	}
	return result
}

func cloneAccountExtra(input map[string]interface{}) map[string]any {
	if input == nil {
		return nil
	}
	cloned := make(map[string]any, len(input))
	for k, v := range input {
		cloned[k] = v
	}
	return cloned
}

var _ appaccount.Repository = (*AccountStore)(nil)
