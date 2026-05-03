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
	if filter.State != "" {
		query = query.Where(entaccount.StateEQ(entaccount.State(filter.State)))
	}
	if filter.AccountType != "" {
		query = query.Where(entaccount.TypeEQ(filter.AccountType))
	}
	if filter.GroupID != nil {
		query = query.Where(entaccount.HasGroupsWith(entgroup.ID(*filter.GroupID)))
	} else if filter.Ungrouped {
		query = query.Where(entaccount.Not(entaccount.HasGroups()))
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

// ListAll 查询符合筛选条件的全部账号（不分页，用于导出）。
func (s *AccountStore) ListAll(ctx context.Context, filter appaccount.ListFilter) ([]appaccount.Account, error) {
	query := s.db.Account.Query()

	if filter.Keyword != "" {
		query = query.Where(entaccount.NameContains(filter.Keyword))
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
		query = query.Where(entaccount.HasGroupsWith(entgroup.ID(*filter.GroupID)))
	} else if filter.Ungrouped {
		query = query.Where(entaccount.Not(entaccount.HasGroups()))
	}
	if filter.ProxyID != nil {
		query = query.Where(entaccount.HasProxyWith(entproxy.IDEQ(*filter.ProxyID)))
	}
	if len(filter.IDs) > 0 {
		query = query.Where(entaccount.IDIn(filter.IDs...))
	}

	accounts, err := query.
		WithGroups().
		WithProxy().
		Order(ent.Desc(entaccount.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, err
	}

	return mapAccounts(accounts), nil
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
		SetRateMultiplier(input.RateMultiplier).
		SetUpstreamIsPool(input.UpstreamIsPool)

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
	if input.State != nil {
		builder = builder.SetState(entaccount.State(*input.State))
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
			entusagelog.FieldAccountCost,
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
			AccountCost:  item.AccountCost,
			ActualCost:   item.ActualCost,
			DurationMs:   item.DurationMs,
			CreatedAt:    item.CreatedAt,
		})
	}
	return result, nil
}

// BatchWindowStats 批量查询多个账号在 [startTime, now] 时间窗口内的聚合统计。
//
// 实现：一次 GROUP BY account_id 的聚合查询，覆盖所有传入的账号 ID。
// 返回的 map 只包含至少有一条 usage_log 的账号，其余账号由调用方按零值处理。
func (s *AccountStore) BatchWindowStats(ctx context.Context, accountIDs []int, startTime time.Time) (map[int]appaccount.AccountWindowStats, error) {
	if len(accountIDs) == 0 {
		return map[int]appaccount.AccountWindowStats{}, nil
	}

	var rows []struct {
		AccountID           int     `json:"account_usage_logs"`
		Count               int     `json:"count"`
		InputTokens         int64   `json:"input_tokens"`
		OutputTokens        int64   `json:"output_tokens"`
		CachedInputTokens   int64   `json:"cached_input_tokens"`
		CacheCreationTokens int64   `json:"cache_creation_tokens"`
		AccountCost         float64 `json:"account_cost"`
		ActualCost          float64 `json:"actual_cost"`
	}

	err := s.db.UsageLog.Query().
		Where(
			entusagelog.HasAccountWith(entaccount.IDIn(accountIDs...)),
			entusagelog.CreatedAtGTE(startTime),
		).
		GroupBy(entusagelog.AccountColumn).
		Aggregate(
			ent.Count(),
			ent.As(ent.Sum(entusagelog.FieldInputTokens), "input_tokens"),
			ent.As(ent.Sum(entusagelog.FieldOutputTokens), "output_tokens"),
			ent.As(ent.Sum(entusagelog.FieldCachedInputTokens), "cached_input_tokens"),
			ent.As(ent.Sum(entusagelog.FieldCacheCreationTokens), "cache_creation_tokens"),
			ent.As(ent.Sum(entusagelog.FieldAccountCost), "account_cost"),
			ent.As(ent.Sum(entusagelog.FieldActualCost), "actual_cost"),
		).
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}

	result := make(map[int]appaccount.AccountWindowStats, len(rows))
	for _, r := range rows {
		if r.AccountID == 0 {
			continue
		}
		result[r.AccountID] = appaccount.AccountWindowStats{
			Requests:    int64(r.Count),
			Tokens:      r.InputTokens + r.OutputTokens + r.CachedInputTokens + r.CacheCreationTokens,
			AccountCost: r.AccountCost,
			UserCost:    r.ActualCost,
		}
	}
	return result, nil
}

// imageModelPrefix 与 app/account/stats.go::isImageModel 保持一致。
// 改这里时记得同步那边，避免"统计口径"和"列表展示"对不上。
const imageModelPrefix = "gpt-image"

// BatchImageStats 一次拿"今日生图数"和"累计生图数"两组聚合，
// 仅算 model 前缀 "gpt-image" 的请求。两条 GROUP BY 查询：
//
//	SELECT account_id, COUNT(*) FROM usage_log
//	  WHERE account_id IN (...) AND model LIKE 'gpt-image%' [AND created_at >= ?]
//	  GROUP BY account_id
//
// 调用方传 openai 平台的账号 ID 子集即可——chat-only 平台传进来不会出错（match 0 行），
// 只是浪费一次查询。
func (s *AccountStore) BatchImageStats(ctx context.Context, accountIDs []int, todayStart time.Time) (map[int]appaccount.AccountImageStats, error) {
	result := map[int]appaccount.AccountImageStats{}
	if len(accountIDs) == 0 {
		return result, nil
	}

	type row struct {
		AccountID int `json:"account_usage_logs"`
		Count     int `json:"count"`
	}

	// 累计：不带 created_at 限制
	var totalRows []row
	if err := s.db.UsageLog.Query().
		Where(
			entusagelog.HasAccountWith(entaccount.IDIn(accountIDs...)),
			entusagelog.ModelHasPrefix(imageModelPrefix),
		).
		GroupBy(entusagelog.AccountColumn).
		Aggregate(ent.Count()).
		Scan(ctx, &totalRows); err != nil {
		return nil, err
	}
	for _, r := range totalRows {
		if r.AccountID == 0 {
			continue
		}
		entry := result[r.AccountID]
		entry.TotalCount = int64(r.Count)
		result[r.AccountID] = entry
	}

	// 今日：created_at >= todayStart
	var todayRows []row
	if err := s.db.UsageLog.Query().
		Where(
			entusagelog.HasAccountWith(entaccount.IDIn(accountIDs...)),
			entusagelog.ModelHasPrefix(imageModelPrefix),
			entusagelog.CreatedAtGTE(todayStart),
		).
		GroupBy(entusagelog.AccountColumn).
		Aggregate(ent.Count()).
		Scan(ctx, &todayRows); err != nil {
		return nil, err
	}
	for _, r := range todayRows {
		if r.AccountID == 0 {
			continue
		}
		entry := result[r.AccountID]
		entry.TodayCount = int64(r.Count)
		result[r.AccountID] = entry
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
		State:          item.State.String(),
		Priority:       item.Priority,
		MaxConcurrency: item.MaxConcurrency,
		RateMultiplier: item.RateMultiplier,
		ErrorMsg:       item.ErrorMsg,
		UpstreamIsPool: item.UpstreamIsPool,
		Extra:          cloneAnyMap(item.Extra),
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
