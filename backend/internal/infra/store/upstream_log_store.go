package store

import (
	"context"
	"time"

	"github.com/DouDOU-start/airgate-core/ent"
	entupstreamrequestlog "github.com/DouDOU-start/airgate-core/ent/upstreamrequestlog"
	appupstreamlog "github.com/DouDOU-start/airgate-core/internal/app/upstreamlog"
	"github.com/DouDOU-start/airgate-core/internal/pkg/timezone"
)

// UpstreamLogStore 使用 Ent 实现上游请求日志仓储。
type UpstreamLogStore struct {
	db *ent.Client
}

// NewUpstreamLogStore 创建上游请求日志仓储。
func NewUpstreamLogStore(db *ent.Client) *UpstreamLogStore {
	return &UpstreamLogStore{db: db}
}

// List 分页查询（时间倒序，ID 同刻回退保证稳定分页）。
func (s *UpstreamLogStore) List(ctx context.Context, filter appupstreamlog.ListFilter) ([]appupstreamlog.Record, error) {
	items, err := applyUpstreamLogFilter(s.db.UpstreamRequestLog.Query(), filter).
		Order(ent.Desc(entupstreamrequestlog.FieldCreatedAt), ent.Desc(entupstreamrequestlog.FieldID)).
		Offset((filter.Page - 1) * filter.PageSize).
		Limit(filter.PageSize).
		All(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]appupstreamlog.Record, 0, len(items))
	for _, item := range items {
		result = append(result, mapUpstreamLog(item))
	}
	return result, nil
}

// Count 统计总数。
func (s *UpstreamLogStore) Count(ctx context.Context, filter appupstreamlog.ListFilter) (int64, error) {
	total, err := applyUpstreamLogFilter(s.db.UpstreamRequestLog.Query(), filter).Count(ctx)
	return int64(total), err
}

func applyUpstreamLogFilter(query *ent.UpstreamRequestLogQuery, filter appupstreamlog.ListFilter) *ent.UpstreamRequestLogQuery {
	if filter.Source != "" {
		query = query.Where(entupstreamrequestlog.SourceEQ(entupstreamrequestlog.Source(filter.Source)))
	}
	if filter.Phase != "" {
		query = query.Where(entupstreamrequestlog.PhaseEQ(filter.Phase))
	}
	if filter.UserID != nil {
		query = query.Where(entupstreamrequestlog.UserIDEQ(int(*filter.UserID)))
	}
	if filter.APIKeyID != nil {
		query = query.Where(entupstreamrequestlog.APIKeyIDEQ(int(*filter.APIKeyID)))
	}
	if filter.ChannelID != nil {
		query = query.Where(entupstreamrequestlog.ChannelIDEQ(int(*filter.ChannelID)))
	}
	if filter.RequestID != "" {
		query = query.Where(entupstreamrequestlog.RequestIDEQ(filter.RequestID))
	}
	if filter.Model != "" {
		query = query.Where(entupstreamrequestlog.ModelContains(filter.Model))
	}
	loc := timezone.Resolve(filter.TZ)
	if filter.StartDate != "" {
		if parsed, err := timezone.ParseDate(filter.StartDate, loc); err == nil {
			query = query.Where(entupstreamrequestlog.CreatedAtGTE(parsed))
		}
	}
	if filter.EndDate != "" {
		if parsed, err := timezone.ParseDate(filter.EndDate, loc); err == nil {
			query = query.Where(entupstreamrequestlog.CreatedAtLT(parsed.AddDate(0, 0, 1)))
		}
	}
	return query
}

func mapUpstreamLog(item *ent.UpstreamRequestLog) appupstreamlog.Record {
	return appupstreamlog.Record{
		ID:           int64(item.ID),
		RequestID:    item.RequestID,
		Source:       string(item.Source),
		Phase:        item.Phase,
		StatusCode:   item.StatusCode,
		ErrorType:    item.ErrorType,
		ErrorCode:    item.ErrorCode,
		Message:      item.Message,
		Attempts:     item.Attempts,
		AttemptChain: item.AttemptChain,
		Billed:       item.Billed,
		Model:        item.Model,
		Endpoint:     item.Endpoint,
		Stream:       item.Stream,
		UserID:       item.UserID,
		UserEmail:    item.UserEmailSnapshot,
		APIKeyID:     item.APIKeyID,
		GroupID:      item.GroupID,
		ChannelID:    item.ChannelID,
		ChannelName:  item.ChannelName,
		IPAddress:    item.IPAddress,
		UserAgent:    item.UserAgent,
		DurationMs:   item.DurationMs,
		RepeatCount:  item.RepeatCount,
		CreatedAt:    item.CreatedAt.Format(time.RFC3339),
	}
}

var _ appupstreamlog.Repository = (*UpstreamLogStore)(nil)
