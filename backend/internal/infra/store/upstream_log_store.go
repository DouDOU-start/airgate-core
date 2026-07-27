package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/DouDOU-start/airgate-core/ent"
	entchannel "github.com/DouDOU-start/airgate-core/ent/channel"
	entchannelkey "github.com/DouDOU-start/airgate-core/ent/channelkey"
	entupstreamrequestlog "github.com/DouDOU-start/airgate-core/ent/upstreamrequestlog"
	appupstreamlog "github.com/DouDOU-start/airgate-core/internal/app/upstreamlog"
	"github.com/DouDOU-start/airgate-core/internal/errlog"
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
	channelNames, err := s.missingChannelNames(ctx, items)
	if err != nil {
		return nil, err
	}
	if err := s.fillMissingAttemptKeyNames(ctx, items); err != nil {
		return nil, err
	}
	result := make([]appupstreamlog.Record, 0, len(items))
	for _, item := range items {
		record := mapUpstreamLog(item)
		if record.ChannelName == "" {
			record.ChannelName = channelNames[record.ChannelID]
		}
		result = append(result, record)
	}
	return result, nil
}

// fillMissingAttemptKeyNames 为旧版重试链中只有 key ID、没有 key 名称的记录补充当前名称。
// 已写入的名称快照不会被覆盖，因此渠道密钥改名后仍保留请求发生时的名称。
func (s *UpstreamLogStore) fillMissingAttemptKeyNames(ctx context.Context, items []*ent.UpstreamRequestLog) error {
	chains := make(map[*ent.UpstreamRequestLog][]errlog.AttemptHop)
	missing := make(map[int]struct{})
	for _, item := range items {
		if len(item.AttemptChain) == 0 {
			continue
		}
		var hops []errlog.AttemptHop
		if err := json.Unmarshal(item.AttemptChain, &hops); err != nil {
			continue
		}
		chains[item] = hops
		for _, hop := range hops {
			if hop.KeyID > 0 && hop.KeyName == "" {
				missing[hop.KeyID] = struct{}{}
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}

	ids := make([]int, 0, len(missing))
	for id := range missing {
		ids = append(ids, id)
	}
	keys, err := s.db.ChannelKey.Query().Where(entchannelkey.IDIn(ids...)).All(ctx)
	if err != nil {
		return err
	}
	names := make(map[int]string, len(keys))
	for _, key := range keys {
		names[key.ID] = key.Name
	}
	for item, hops := range chains {
		changed := false
		for i := range hops {
			if hops[i].KeyName == "" && names[hops[i].KeyID] != "" {
				hops[i].KeyName = names[hops[i].KeyID]
				changed = true
			}
		}
		if changed {
			data, err := json.Marshal(hops)
			if err != nil {
				return err
			}
			item.AttemptChain = data
		}
	}
	return nil
}

// missingChannelNames 为旧版漏写渠道名称的失败记录补充当前渠道名。
// 新记录仍以落库快照为准，确保渠道改名后保留请求发生时的名称。
func (s *UpstreamLogStore) missingChannelNames(ctx context.Context, items []*ent.UpstreamRequestLog) (map[int]string, error) {
	missing := make(map[int]struct{})
	for _, item := range items {
		if item.ChannelID > 0 && item.ChannelName == "" {
			missing[item.ChannelID] = struct{}{}
		}
	}
	if len(missing) == 0 {
		return nil, nil
	}

	ids := make([]int, 0, len(missing))
	for id := range missing {
		ids = append(ids, id)
	}
	channels, err := s.db.Channel.Query().
		Where(entchannel.IDIn(ids...)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	names := make(map[int]string, len(channels))
	for _, channel := range channels {
		names[channel.ID] = channel.Name
	}
	return names, nil
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
