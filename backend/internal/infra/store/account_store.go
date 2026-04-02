package store

import (
	"context"
	"time"

	"github.com/DouDOU-start/airgate-core/ent"
	entaccount "github.com/DouDOU-start/airgate-core/ent/account"
	entgroup "github.com/DouDOU-start/airgate-core/ent/group"
	"github.com/DouDOU-start/airgate-core/ent/predicate"
	entproxy "github.com/DouDOU-start/airgate-core/ent/proxy"
	entusagelog "github.com/DouDOU-start/airgate-core/ent/usagelog"
	appaccount "github.com/DouDOU-start/airgate-core/internal/app/account"
)

// AccountStore 使用 Ent 实现账号仓储。
type AccountStore struct {
	db *ent.Client
}

// NewAccountStore 创建账号仓储。
func NewAccountStore(db *ent.Client) *AccountStore {
	return &AccountStore{db: db}
}

// List 查询账号列表。
func (s *AccountStore) List(ctx context.Context, filter appaccount.ListFilter) ([]appaccount.Account, int64, error) {
	query := s.db.Account.Query()

	if filter.Keyword != "" {
		query = query.Where(entaccount.NameContains(filter.Keyword))
	}
	if filter.Platform != "" {
		query = query.Where(entaccount.PlatformEQ(filter.Platform))
	}
	if filter.Status != "" {
		query = query.Where(entaccount.StatusEQ(entaccount.Status(filter.Status)))
	}
	if filter.GroupID != nil {
		query = query.Where(entaccount.HasGroupsWith(entgroup.ID(*filter.GroupID)))
	}
	if filter.ProxyID != nil {
		query = query.Where(entaccount.HasProxyWith(entproxy.IDEQ(*filter.ProxyID)))
	}

	total, err := query.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	accounts, err := query.
		WithGroups().
		WithProxy().
		Offset((filter.Page - 1) * filter.PageSize).
		Limit(filter.PageSize).
		Order(ent.Desc(entaccount.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}

	return mapAccounts(accounts), int64(total), nil
}

// Create 创建账号。
func (s *AccountStore) Create(ctx context.Context, input appaccount.CreateInput) (appaccount.Account, error) {
	builder := s.db.Account.Create().
		SetName(input.Name).
		SetPlatform(input.Platform).
		SetType(input.Type).
		SetCredentials(cloneCredentials(input.Credentials)).
		SetPriority(input.Priority).
		SetMaxConcurrency(input.MaxConcurrency).
		SetRateMultiplier(input.RateMultiplier)

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
func (s *AccountStore) Update(ctx context.Context, id int, input appaccount.UpdateInput) (appaccount.Account, error) {
	builder := s.db.Account.UpdateOneID(id)

	if input.Name != nil {
		builder = builder.SetName(*input.Name)
	}
	if input.Type != nil {
		builder = builder.SetType(*input.Type)
	}
	if input.Credentials != nil {
		builder = builder.SetCredentials(cloneCredentials(input.Credentials))
	}
	if input.Status != nil {
		builder = builder.SetStatus(entaccount.Status(*input.Status))
	}
	if input.Priority != nil {
		builder = builder.SetPriority(*input.Priority)
	}
	if input.MaxConcurrency != nil {
		builder = builder.SetMaxConcurrency(*input.MaxConcurrency)
	}
	if input.RateMultiplier != nil {
		builder = builder.SetRateMultiplier(*input.RateMultiplier)
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

// ListByPlatform 按平台查询账号。
func (s *AccountStore) ListByPlatform(ctx context.Context, platform string) ([]appaccount.Account, error) {
	accounts, err := s.db.Account.Query().
		Where(entaccount.PlatformEQ(platform)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	return mapAccounts(accounts), nil
}

// FindUsageLogs 查询账号在指定时间范围内的使用记录。
func (s *AccountStore) FindUsageLogs(ctx context.Context, id int, startDate, endDate time.Time) ([]appaccount.UsageLog, error) {
	predicates := []predicate.UsageLog{
		entusagelog.HasAccountWith(entaccount.IDEQ(id)),
		entusagelog.CreatedAtGTE(startDate),
		entusagelog.CreatedAtLTE(endDate),
	}

	logs, err := s.db.UsageLog.Query().
		Where(predicates...).
		Select(
			entusagelog.FieldModel,
			entusagelog.FieldInputTokens,
			entusagelog.FieldOutputTokens,
			entusagelog.FieldTotalCost,
			entusagelog.FieldActualCost,
			entusagelog.FieldDurationMs,
			entusagelog.FieldCreatedAt,
		).
		All(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]appaccount.UsageLog, 0, len(logs))
	for _, item := range logs {
		result = append(result, appaccount.UsageLog{
			Model:        item.Model,
			InputTokens:  int64(item.InputTokens),
			OutputTokens: int64(item.OutputTokens),
			TotalCost:    item.TotalCost,
			ActualCost:   item.ActualCost,
			DurationMs:   item.DurationMs,
			CreatedAt:    item.CreatedAt,
		})
	}
	return result, nil
}

// SaveCredentials 保存账号凭证。
func (s *AccountStore) SaveCredentials(ctx context.Context, id int, credentials map[string]string) error {
	if err := s.db.Account.UpdateOneID(id).
		SetCredentials(cloneCredentials(credentials)).
		Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return appaccount.ErrAccountNotFound
		}
		return err
	}
	return nil
}

// MarkError 将账号标记为错误状态。
func (s *AccountStore) MarkError(ctx context.Context, id int, message string) error {
	if err := s.db.Account.UpdateOneID(id).
		SetStatus(entaccount.StatusError).
		SetErrorMsg(message).
		Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return appaccount.ErrAccountNotFound
		}
		return err
	}
	return nil
}

func mapAccounts(accounts []*ent.Account) []appaccount.Account {
	result := make([]appaccount.Account, 0, len(accounts))
	for _, item := range accounts {
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
		Credentials:    cloneCredentials(item.Credentials),
		Status:         item.Status.String(),
		Priority:       item.Priority,
		MaxConcurrency: item.MaxConcurrency,
		RateMultiplier: item.RateMultiplier,
		ErrorMsg:       item.ErrorMsg,
		Extra:          cloneAnyMap(item.Extra),
		CreatedAt:      item.CreatedAt,
		UpdatedAt:      item.UpdatedAt,
	}

	if item.LastUsedAt != nil {
		value := *item.LastUsedAt
		result.LastUsedAt = &value
	}
	if item.Edges.Proxy != nil {
		result.Proxy = &appaccount.Proxy{
			ID:       item.Edges.Proxy.ID,
			Protocol: string(item.Edges.Proxy.Protocol),
			Address:  item.Edges.Proxy.Address,
			Port:     item.Edges.Proxy.Port,
			Username: item.Edges.Proxy.Username,
			Password: item.Edges.Proxy.Password,
		}
	}
	for _, relatedGroup := range item.Edges.Groups {
		result.GroupIDs = append(result.GroupIDs, int64(relatedGroup.ID))
	}

	return result
}

func toIntSlice(values []int64) []int {
	result := make([]int, 0, len(values))
	for _, value := range values {
		result = append(result, int(value))
	}
	return result
}

func cloneCredentials(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	cloned := make(map[string]string, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

func cloneAnyMap(input map[string]interface{}) map[string]any {
	if input == nil {
		return nil
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

var _ appaccount.Repository = (*AccountStore)(nil)
