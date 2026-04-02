package store

import (
	"context"
	"sort"
	"time"

	"github.com/DouDOU-start/airgate-core/ent"
	entaccount "github.com/DouDOU-start/airgate-core/ent/account"
	entapikey "github.com/DouDOU-start/airgate-core/ent/apikey"
	entgroup "github.com/DouDOU-start/airgate-core/ent/group"
	entusagelog "github.com/DouDOU-start/airgate-core/ent/usagelog"
	entuser "github.com/DouDOU-start/airgate-core/ent/user"
	appusage "github.com/DouDOU-start/airgate-core/internal/app/usage"
)

// UsageStore 使用 Ent 实现使用记录仓储。
type UsageStore struct {
	db *ent.Client
}

// NewUsageStore 创建使用记录仓储。
func NewUsageStore(db *ent.Client) *UsageStore {
	return &UsageStore{db: db}
}

// ListUser 查询用户使用记录。
func (s *UsageStore) ListUser(ctx context.Context, userID int64, filter appusage.ListFilter) ([]appusage.LogRecord, int64, error) {
	query := s.db.UsageLog.Query().
		Where(entusagelog.HasUserWith(entuser.IDEQ(int(userID))))
	query = applyUsageListFilter(query, filter)

	total, err := query.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	logs, err := query.
		WithUser().
		WithAPIKey().
		WithAccount().
		WithGroup().
		Offset((filter.Page - 1) * filter.PageSize).
		Limit(filter.PageSize).
		Order(ent.Desc(entusagelog.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}

	result := make([]appusage.LogRecord, 0, len(logs))
	for _, item := range logs {
		result = append(result, mapUsageLog(item))
	}
	return result, int64(total), nil
}

// ListAdmin 查询管理员使用记录。
func (s *UsageStore) ListAdmin(ctx context.Context, filter appusage.ListFilter) ([]appusage.LogRecord, int64, error) {
	query := s.db.UsageLog.Query()
	if filter.UserID != nil {
		query = query.Where(entusagelog.HasUserWith(entuser.IDEQ(int(*filter.UserID))))
	}
	query = applyUsageListFilter(query, filter)

	total, err := query.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	logs, err := query.
		WithUser().
		WithAPIKey().
		WithAccount().
		WithGroup().
		Offset((filter.Page - 1) * filter.PageSize).
		Limit(filter.PageSize).
		Order(ent.Desc(entusagelog.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}

	result := make([]appusage.LogRecord, 0, len(logs))
	for _, item := range logs {
		result = append(result, mapUsageLog(item))
	}
	return result, int64(total), nil
}

// SummaryUser 查询用户汇总统计。
func (s *UsageStore) SummaryUser(ctx context.Context, userID int64, filter appusage.StatsFilter) (appusage.Summary, error) {
	query := s.db.UsageLog.Query().
		Where(entusagelog.HasUserWith(entuser.IDEQ(int(userID))))
	query = applyUsageStatsFilter(query, filter)
	return scanSummary(ctx, query)
}

// SummaryAdmin 查询管理员汇总统计。
func (s *UsageStore) SummaryAdmin(ctx context.Context, filter appusage.StatsFilter) (appusage.Summary, error) {
	query := s.db.UsageLog.Query()
	if filter.UserID != nil {
		query = query.Where(entusagelog.HasUserWith(entuser.IDEQ(int(*filter.UserID))))
	}
	query = applyUsageStatsFilter(query, filter)
	return scanSummary(ctx, query)
}

// StatsByModel 按模型分组统计。
func (s *UsageStore) StatsByModel(ctx context.Context, filter appusage.StatsFilter) ([]appusage.ModelStats, error) {
	query := s.db.UsageLog.Query()
	if filter.UserID != nil {
		query = query.Where(entusagelog.HasUserWith(entuser.IDEQ(int(*filter.UserID))))
	}
	query = applyUsageStatsFilter(query, filter)

	var rows []struct {
		Model        string  `json:"model"`
		Count        int     `json:"count"`
		InputTokens  int64   `json:"input_tokens"`
		OutputTokens int64   `json:"output_tokens"`
		TotalCost    float64 `json:"total_cost"`
		ActualCost   float64 `json:"actual_cost"`
	}
	err := query.GroupBy(entusagelog.FieldModel).
		Aggregate(
			ent.Count(),
			ent.As(ent.Sum(entusagelog.FieldInputTokens), "input_tokens"),
			ent.As(ent.Sum(entusagelog.FieldOutputTokens), "output_tokens"),
			ent.As(ent.Sum(entusagelog.FieldTotalCost), "total_cost"),
			ent.As(ent.Sum(entusagelog.FieldActualCost), "actual_cost"),
		).
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}

	result := make([]appusage.ModelStats, 0, len(rows))
	for _, row := range rows {
		result = append(result, appusage.ModelStats{
			Model:      row.Model,
			Requests:   int64(row.Count),
			Tokens:     row.InputTokens + row.OutputTokens,
			TotalCost:  row.TotalCost,
			ActualCost: row.ActualCost,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Requests == result[j].Requests {
			return result[i].Model < result[j].Model
		}
		return result[i].Requests > result[j].Requests
	})
	return result, nil
}

// StatsByUser 按用户分组统计。
func (s *UsageStore) StatsByUser(ctx context.Context, filter appusage.StatsFilter) ([]appusage.UserStats, error) {
	query := s.db.UsageLog.Query()
	if filter.UserID != nil {
		query = query.Where(entusagelog.HasUserWith(entuser.IDEQ(int(*filter.UserID))))
	}
	query = applyUsageStatsFilter(query, filter)

	var rows []struct {
		UserID       int     `json:"user_usage_logs"`
		Count        int     `json:"count"`
		InputTokens  int64   `json:"input_tokens"`
		OutputTokens int64   `json:"output_tokens"`
		TotalCost    float64 `json:"total_cost"`
		ActualCost   float64 `json:"actual_cost"`
	}
	err := query.GroupBy("user_usage_logs").
		Aggregate(
			ent.Count(),
			ent.As(ent.Sum(entusagelog.FieldInputTokens), "input_tokens"),
			ent.As(ent.Sum(entusagelog.FieldOutputTokens), "output_tokens"),
			ent.As(ent.Sum(entusagelog.FieldTotalCost), "total_cost"),
			ent.As(ent.Sum(entusagelog.FieldActualCost), "actual_cost"),
		).
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}

	userIDs := make([]int, 0, len(rows))
	for _, row := range rows {
		if row.UserID > 0 {
			userIDs = append(userIDs, row.UserID)
		}
	}
	emailMap := make(map[int]string)
	if len(userIDs) > 0 {
		users, err := s.db.User.Query().Where(entuser.IDIn(userIDs...)).All(ctx)
		if err != nil {
			return nil, err
		}
		for _, item := range users {
			emailMap[item.ID] = item.Email
		}
	}

	result := make([]appusage.UserStats, 0, len(rows))
	for _, row := range rows {
		result = append(result, appusage.UserStats{
			UserID:     int64(row.UserID),
			Email:      emailMap[row.UserID],
			Requests:   int64(row.Count),
			Tokens:     row.InputTokens + row.OutputTokens,
			TotalCost:  row.TotalCost,
			ActualCost: row.ActualCost,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Requests == result[j].Requests {
			return result[i].UserID < result[j].UserID
		}
		return result[i].Requests > result[j].Requests
	})
	return result, nil
}

// StatsByAccount 按账号分组统计。
func (s *UsageStore) StatsByAccount(ctx context.Context, filter appusage.StatsFilter) ([]appusage.AccountStats, error) {
	query := s.db.UsageLog.Query()
	if filter.UserID != nil {
		query = query.Where(entusagelog.HasUserWith(entuser.IDEQ(int(*filter.UserID))))
	}
	query = applyUsageStatsFilter(query, filter)

	var rows []struct {
		AccountID    int     `json:"account_usage_logs"`
		Count        int     `json:"count"`
		InputTokens  int64   `json:"input_tokens"`
		OutputTokens int64   `json:"output_tokens"`
		TotalCost    float64 `json:"total_cost"`
		ActualCost   float64 `json:"actual_cost"`
	}
	err := query.GroupBy("account_usage_logs").
		Aggregate(
			ent.Count(),
			ent.As(ent.Sum(entusagelog.FieldInputTokens), "input_tokens"),
			ent.As(ent.Sum(entusagelog.FieldOutputTokens), "output_tokens"),
			ent.As(ent.Sum(entusagelog.FieldTotalCost), "total_cost"),
			ent.As(ent.Sum(entusagelog.FieldActualCost), "actual_cost"),
		).
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}

	accountIDs := make([]int, 0, len(rows))
	for _, row := range rows {
		if row.AccountID > 0 {
			accountIDs = append(accountIDs, row.AccountID)
		}
	}
	nameMap := make(map[int]string)
	if len(accountIDs) > 0 {
		accounts, err := s.db.Account.Query().Where(entaccount.IDIn(accountIDs...)).All(ctx)
		if err != nil {
			return nil, err
		}
		for _, item := range accounts {
			nameMap[item.ID] = item.Name
		}
	}

	result := make([]appusage.AccountStats, 0, len(rows))
	for _, row := range rows {
		result = append(result, appusage.AccountStats{
			AccountID:  int64(row.AccountID),
			Name:       nameMap[row.AccountID],
			Requests:   int64(row.Count),
			Tokens:     row.InputTokens + row.OutputTokens,
			TotalCost:  row.TotalCost,
			ActualCost: row.ActualCost,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Requests == result[j].Requests {
			return result[i].AccountID < result[j].AccountID
		}
		return result[i].Requests > result[j].Requests
	})
	return result, nil
}

// StatsByGroup 按分组统计。
func (s *UsageStore) StatsByGroup(ctx context.Context, filter appusage.StatsFilter) ([]appusage.GroupStats, error) {
	query := s.db.UsageLog.Query()
	if filter.UserID != nil {
		query = query.Where(entusagelog.HasUserWith(entuser.IDEQ(int(*filter.UserID))))
	}
	query = applyUsageStatsFilter(query, filter)

	var rows []struct {
		GroupID      int     `json:"group_usage_logs"`
		Count        int     `json:"count"`
		InputTokens  int64   `json:"input_tokens"`
		OutputTokens int64   `json:"output_tokens"`
		TotalCost    float64 `json:"total_cost"`
		ActualCost   float64 `json:"actual_cost"`
	}
	err := query.GroupBy("group_usage_logs").
		Aggregate(
			ent.Count(),
			ent.As(ent.Sum(entusagelog.FieldInputTokens), "input_tokens"),
			ent.As(ent.Sum(entusagelog.FieldOutputTokens), "output_tokens"),
			ent.As(ent.Sum(entusagelog.FieldTotalCost), "total_cost"),
			ent.As(ent.Sum(entusagelog.FieldActualCost), "actual_cost"),
		).
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}

	groupIDs := make([]int, 0, len(rows))
	for _, row := range rows {
		if row.GroupID > 0 {
			groupIDs = append(groupIDs, row.GroupID)
		}
	}
	nameMap := make(map[int]string)
	if len(groupIDs) > 0 {
		groups, err := s.db.Group.Query().Where(entgroup.IDIn(groupIDs...)).All(ctx)
		if err != nil {
			return nil, err
		}
		for _, item := range groups {
			nameMap[item.ID] = item.Name
		}
	}

	result := make([]appusage.GroupStats, 0, len(rows))
	for _, row := range rows {
		result = append(result, appusage.GroupStats{
			GroupID:    int64(row.GroupID),
			Name:       nameMap[row.GroupID],
			Requests:   int64(row.Count),
			Tokens:     row.InputTokens + row.OutputTokens,
			TotalCost:  row.TotalCost,
			ActualCost: row.ActualCost,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Requests == result[j].Requests {
			return result[i].GroupID < result[j].GroupID
		}
		return result[i].Requests > result[j].Requests
	})
	return result, nil
}

// TrendEntries 查询趋势原始记录。
func (s *UsageStore) TrendEntries(ctx context.Context, filter appusage.TrendFilter) ([]appusage.TrendEntry, error) {
	query := s.db.UsageLog.Query()
	if filter.UserID != nil {
		query = query.Where(entusagelog.HasUserWith(entuser.IDEQ(int(*filter.UserID))))
	}
	query = applyUsageStatsFilter(query, filter.StatsFilter)
	if filter.StartDate == "" && filter.EndDate == "" && filter.DefaultRecentHours > 0 {
		query = query.Where(entusagelog.CreatedAtGTE(time.Now().Add(-time.Duration(filter.DefaultRecentHours) * time.Hour)))
	}

	logs, err := query.
		Select(
			entusagelog.FieldInputTokens,
			entusagelog.FieldOutputTokens,
			entusagelog.FieldCachedInputTokens,
			entusagelog.FieldActualCost,
			entusagelog.FieldTotalCost,
			entusagelog.FieldCreatedAt,
		).
		All(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]appusage.TrendEntry, 0, len(logs))
	for _, item := range logs {
		result = append(result, appusage.TrendEntry{
			CreatedAt:         item.CreatedAt.Format(time.RFC3339),
			InputTokens:       int64(item.InputTokens),
			OutputTokens:      int64(item.OutputTokens),
			CachedInputTokens: int64(item.CachedInputTokens),
			ActualCost:        item.ActualCost,
			StandardCost:      item.TotalCost,
		})
	}
	return result, nil
}

func applyUsageListFilter(query *ent.UsageLogQuery, filter appusage.ListFilter) *ent.UsageLogQuery {
	if filter.APIKeyID != nil {
		query = query.Where(entusagelog.HasAPIKeyWith(entapikey.IDEQ(int(*filter.APIKeyID))))
	}
	if filter.AccountID != nil {
		query = query.Where(entusagelog.HasAccountWith(entaccount.IDEQ(int(*filter.AccountID))))
	}
	if filter.GroupID != nil {
		query = query.Where(entusagelog.HasGroupWith(entgroup.IDEQ(int(*filter.GroupID))))
	}
	return applyUsageStatsFilter(query, appusage.StatsFilter{
		Platform:  filter.Platform,
		Model:     filter.Model,
		StartDate: filter.StartDate,
		EndDate:   filter.EndDate,
	})
}

func applyUsageStatsFilter(query *ent.UsageLogQuery, filter appusage.StatsFilter) *ent.UsageLogQuery {
	if filter.Platform != "" {
		query = query.Where(entusagelog.PlatformEQ(filter.Platform))
	}
	if filter.Model != "" {
		query = query.Where(entusagelog.ModelContains(filter.Model))
	}
	if filter.StartDate != "" {
		if parsed, err := time.Parse("2006-01-02", filter.StartDate); err == nil {
			query = query.Where(entusagelog.CreatedAtGTE(parsed))
		}
	}
	if filter.EndDate != "" {
		if parsed, err := time.Parse("2006-01-02", filter.EndDate); err == nil {
			query = query.Where(entusagelog.CreatedAtLT(parsed.AddDate(0, 0, 1)))
		}
	}
	return query
}

func scanSummary(ctx context.Context, query *ent.UsageLogQuery) (appusage.Summary, error) {
	totalRequests, err := query.Clone().Count(ctx)
	if err != nil {
		return appusage.Summary{}, err
	}

	var rows []struct {
		InputTokens       int64   `json:"input_tokens"`
		OutputTokens      int64   `json:"output_tokens"`
		CachedInputTokens int64   `json:"cached_input_tokens"`
		TotalCost         float64 `json:"total_cost"`
		ActualCost        float64 `json:"actual_cost"`
	}
	err = query.Clone().
		Aggregate(
			ent.As(ent.Sum(entusagelog.FieldInputTokens), "input_tokens"),
			ent.As(ent.Sum(entusagelog.FieldOutputTokens), "output_tokens"),
			ent.As(ent.Sum(entusagelog.FieldCachedInputTokens), "cached_input_tokens"),
			ent.As(ent.Sum(entusagelog.FieldTotalCost), "total_cost"),
			ent.As(ent.Sum(entusagelog.FieldActualCost), "actual_cost"),
		).
		Scan(ctx, &rows)
	if err != nil {
		return appusage.Summary{}, err
	}

	summary := appusage.Summary{TotalRequests: int64(totalRequests)}
	if len(rows) > 0 {
		summary.TotalTokens = rows[0].InputTokens + rows[0].OutputTokens + rows[0].CachedInputTokens
		summary.TotalCost = rows[0].TotalCost
		summary.TotalActualCost = rows[0].ActualCost
	}
	return summary, nil
}

func mapUsageLog(item *ent.UsageLog) appusage.LogRecord {
	record := appusage.LogRecord{
		ID:                    int64(item.ID),
		Platform:              item.Platform,
		Model:                 item.Model,
		InputTokens:           item.InputTokens,
		OutputTokens:          item.OutputTokens,
		CachedInputTokens:     item.CachedInputTokens,
		ReasoningOutputTokens: item.ReasoningOutputTokens,
		InputPrice:            item.InputPrice,
		OutputPrice:           item.OutputPrice,
		CachedInputPrice:      item.CachedInputPrice,
		InputCost:             item.InputCost,
		OutputCost:            item.OutputCost,
		CachedInputCost:       item.CachedInputCost,
		TotalCost:             item.TotalCost,
		ActualCost:            item.ActualCost,
		RateMultiplier:        item.RateMultiplier,
		AccountRateMultiplier: item.AccountRateMultiplier,
		ServiceTier:           item.ServiceTier,
		Stream:                item.Stream,
		DurationMs:            item.DurationMs,
		FirstTokenMs:          item.FirstTokenMs,
		UserAgent:             item.UserAgent,
		IPAddress:             item.IPAddress,
		CreatedAt:             item.CreatedAt.Format(time.RFC3339),
	}

	if item.Edges.User != nil {
		record.UserID = int64(item.Edges.User.ID)
		record.UserEmail = item.Edges.User.Email
	}
	record.APIKeyDeleted = item.Edges.APIKey == nil
	if item.Edges.APIKey != nil {
		record.APIKeyID = int64(item.Edges.APIKey.ID)
		record.APIKeyName = item.Edges.APIKey.Name
		record.APIKeyHint = item.Edges.APIKey.KeyHint
	}
	if item.Edges.Account != nil {
		record.AccountID = int64(item.Edges.Account.ID)
		if email, ok := item.Edges.Account.Credentials["email"]; ok && email != "" {
			record.AccountName = email
		} else {
			record.AccountName = item.Edges.Account.Name
		}
	}
	if item.Edges.Group != nil {
		record.GroupID = int64(item.Edges.Group.ID)
	}

	return record
}

var _ appusage.Repository = (*UsageStore)(nil)
