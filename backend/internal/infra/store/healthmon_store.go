package store

import (
	"context"
	"time"

	entsql "entgo.io/ent/dialect/sql"

	"github.com/DouDOU-start/airgate-core/ent"
	entchannelkey "github.com/DouDOU-start/airgate-core/ent/channelkey"
	entgroup "github.com/DouDOU-start/airgate-core/ent/group"
	entupstreamrequestlog "github.com/DouDOU-start/airgate-core/ent/upstreamrequestlog"
	entusagelog "github.com/DouDOU-start/airgate-core/ent/usagelog"
	entuser "github.com/DouDOU-start/airgate-core/ent/user"
	apphealthmon "github.com/DouDOU-start/airgate-core/internal/app/healthmon"
)

// HealthmonStore 健康监测仓储（实现 app/healthmon.Repository）。
type HealthmonStore struct {
	db *ent.Client
}

// NewHealthmonStore 创建健康监测仓储。
func NewHealthmonStore(db *ent.Client) *HealthmonStore {
	return &HealthmonStore{db: db}
}

// AggregateSuccess 按 channel_key 聚合 relay 成功流量（排除 test/task）。
//
// 当前直接聚合窗口平均值与最大值；精确分位和历史趋势留给后续预聚合。
func (s *HealthmonStore) AggregateSuccess(ctx context.Context, since time.Time) ([]apphealthmon.SuccessAgg, error) {
	return s.aggregateSuccessBy(ctx, since, entusagelog.FieldChannelKeyID, true, nil)
}

// AggregateSuccessByGroup 按 group 聚合 relay 成功流量。
func (s *HealthmonStore) AggregateSuccessByGroup(ctx context.Context, since time.Time) ([]apphealthmon.SuccessAgg, error) {
	return s.aggregateSuccessBy(ctx, since, entusagelog.FieldGroupID, true, nil)
}

// AggregateSuccessByGroupIDs 仅聚合指定分组；空 ids 必须返回空，避免权限过滤失效后退化为全量查询。
func (s *HealthmonStore) AggregateSuccessByGroupIDs(ctx context.Context, since time.Time, ids []int) ([]apphealthmon.SuccessAgg, error) {
	if len(ids) == 0 {
		return []apphealthmon.SuccessAgg{}, nil
	}
	return s.aggregateSuccessBy(ctx, since, entusagelog.FieldGroupID, true, ids)
}

func (s *HealthmonStore) aggregateSuccessBy(ctx context.Context, since time.Time, dimField string, requirePositiveDim bool, dimIDs []int) ([]apphealthmon.SuccessAgg, error) {
	var rows []struct {
		DimID       int     `json:"dim_id"`
		Count       int64   `json:"count"`
		AvgDuration float64 `json:"avg_duration"`
		MaxDuration float64 `json:"max_duration"`
		AvgTTFT     float64 `json:"avg_ttft"`
		MaxTTFT     float64 `json:"max_ttft"`
	}
	q := s.db.UsageLog.Query().Where(
		entusagelog.CreatedAtGTE(since),
		entusagelog.SourceEQ("relay"),
	)
	if requirePositiveDim {
		switch dimField {
		case entusagelog.FieldChannelKeyID:
			q = q.Where(entusagelog.ChannelKeyIDNotNil())
		case entusagelog.FieldGroupID:
			q = q.Where(entusagelog.GroupIDNotNil())
		}
	}
	if len(dimIDs) > 0 {
		switch dimField {
		case entusagelog.FieldChannelKeyID:
			q = q.Where(entusagelog.ChannelKeyIDIn(dimIDs...))
		case entusagelog.FieldGroupID:
			q = q.Where(entusagelog.GroupIDIn(dimIDs...))
		}
	}
	err := q.Modify(func(sel *entsql.Selector) {
		dimCol := sel.C(dimField)
		durCol := sel.C(entusagelog.FieldDurationMs)
		ttftCol := sel.C(entusagelog.FieldFirstTokenMs)
		sel.Select(
			entsql.As(dimCol, "dim_id"),
			entsql.As("COUNT(*)", "count"),
			entsql.As("COALESCE(AVG("+durCol+"),0)", "avg_duration"),
			entsql.As("COALESCE(MAX("+durCol+"),0)", "max_duration"),
			entsql.As("COALESCE(AVG(CASE WHEN "+ttftCol+" > 0 THEN "+ttftCol+" END),0)", "avg_ttft"),
			entsql.As("COALESCE(MAX(CASE WHEN "+ttftCol+" > 0 THEN "+ttftCol+" END),0)", "max_ttft"),
		).GroupBy(dimCol)
	}).Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	out := make([]apphealthmon.SuccessAgg, 0, len(rows))
	for _, row := range rows {
		if row.DimID <= 0 {
			continue
		}
		out = append(out, apphealthmon.SuccessAgg{
			DimID:       row.DimID,
			Count:       row.Count,
			AvgDuration: row.AvgDuration,
			MaxDuration: row.MaxDuration,
			AvgTTFT:     row.AvgTTFT,
			MaxTTFT:     row.MaxTTFT,
		})
	}
	return out, nil
}

// AggregateFailureRaws 按 channel_key + status + phase 聚合 relay 失败。
func (s *HealthmonStore) AggregateFailureRaws(ctx context.Context, since time.Time) ([]apphealthmon.FailureRaw, error) {
	return s.aggregateFailureRawsBy(ctx, since, entupstreamrequestlog.FieldChannelKeyID, true, nil)
}

// AggregateFailureRawsByGroup 按 group + status + phase 聚合 relay 失败。
func (s *HealthmonStore) AggregateFailureRawsByGroup(ctx context.Context, since time.Time) ([]apphealthmon.FailureRaw, error) {
	return s.aggregateFailureRawsBy(ctx, since, entupstreamrequestlog.FieldGroupID, true, nil)
}

// AggregateFailureRawsByGroupIDs 仅聚合指定分组；空 ids 必须返回空。
func (s *HealthmonStore) AggregateFailureRawsByGroupIDs(ctx context.Context, since time.Time, ids []int) ([]apphealthmon.FailureRaw, error) {
	if len(ids) == 0 {
		return []apphealthmon.FailureRaw{}, nil
	}
	return s.aggregateFailureRawsBy(ctx, since, entupstreamrequestlog.FieldGroupID, true, ids)
}

func (s *HealthmonStore) aggregateFailureRawsBy(ctx context.Context, since time.Time, dimField string, requirePositiveDim bool, dimIDs []int) ([]apphealthmon.FailureRaw, error) {
	var rows []struct {
		DimID      int    `json:"dim_id"`
		StatusCode int    `json:"status_code"`
		Phase      string `json:"phase"`
		Billed     bool   `json:"billed"`
		Count      int64  `json:"count"`
	}
	q := s.db.UpstreamRequestLog.Query().Where(
		entupstreamrequestlog.CreatedAtGTE(since),
		entupstreamrequestlog.SourceEQ(entupstreamrequestlog.SourceRelay),
	)
	if requirePositiveDim {
		switch dimField {
		case entupstreamrequestlog.FieldChannelKeyID:
			q = q.Where(entupstreamrequestlog.ChannelKeyIDGT(0))
		case entupstreamrequestlog.FieldGroupID:
			q = q.Where(entupstreamrequestlog.GroupIDGT(0))
		}
	}
	if len(dimIDs) > 0 {
		switch dimField {
		case entupstreamrequestlog.FieldChannelKeyID:
			q = q.Where(entupstreamrequestlog.ChannelKeyIDIn(dimIDs...))
		case entupstreamrequestlog.FieldGroupID:
			q = q.Where(entupstreamrequestlog.GroupIDIn(dimIDs...))
		}
	}
	err := q.Modify(func(sel *entsql.Selector) {
		dimCol := sel.C(dimField)
		statusCol := sel.C(entupstreamrequestlog.FieldStatusCode)
		phaseCol := sel.C(entupstreamrequestlog.FieldPhase)
		billedCol := sel.C(entupstreamrequestlog.FieldBilled)
		repeatCol := sel.C(entupstreamrequestlog.FieldRepeatCount)
		sel.Select(
			entsql.As(dimCol, "dim_id"),
			entsql.As(statusCol, "status_code"),
			entsql.As(phaseCol, "phase"),
			entsql.As(billedCol, "billed"),
			entsql.As("COALESCE(SUM("+repeatCol+"),0)", "count"),
		).GroupBy(dimCol, statusCol, phaseCol, billedCol)
	}).Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	out := make([]apphealthmon.FailureRaw, 0, len(rows))
	for _, row := range rows {
		if row.DimID <= 0 {
			continue
		}
		raw := apphealthmon.FailureRaw{
			StatusCode: row.StatusCode,
			Phase:      row.Phase,
			Billed:     row.Billed,
			Count:      row.Count,
		}
		switch dimField {
		case entupstreamrequestlog.FieldChannelKeyID:
			raw.ChannelKeyID = row.DimID
		case entupstreamrequestlog.FieldGroupID:
			raw.GroupID = row.DimID
		}
		out = append(out, raw)
	}
	return out, nil
}

// ListKeyMeta 列出密钥端点元数据。
func (s *HealthmonStore) ListKeyMeta(ctx context.Context, ids []int) ([]apphealthmon.KeyMeta, error) {
	q := s.db.ChannelKey.Query().WithChannel()
	if len(ids) > 0 {
		q = q.Where(entchannelkey.IDIn(ids...))
	}
	keys, err := q.All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]apphealthmon.KeyMeta, 0, len(keys))
	for _, k := range keys {
		meta := apphealthmon.KeyMeta{
			ID:           k.ID,
			Name:         k.Name,
			Type:         string(k.Type),
			Status:       string(k.Status),
			HealthStatus: string(k.HealthStatus),
		}
		if ch, err := k.Edges.ChannelOrErr(); err == nil && ch != nil {
			meta.ChannelID = ch.ID
			meta.ChannelName = ch.Name
		}
		out = append(out, meta)
	}
	return out, nil
}

// ListGroupMeta 列出分组元数据。
func (s *HealthmonStore) ListGroupMeta(ctx context.Context, ids []int) ([]apphealthmon.GroupMeta, error) {
	q := s.db.Group.Query()
	if len(ids) > 0 {
		q = q.Where(entgroup.IDIn(ids...))
	}
	groups, err := q.Order(ent.Desc(entgroup.FieldSortWeight), ent.Desc(entgroup.FieldCreatedAt)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]apphealthmon.GroupMeta, 0, len(groups))
	for _, g := range groups {
		out = append(out, apphealthmon.GroupMeta{
			ID:       g.ID,
			Name:     g.Name,
			Platform: g.Platform,
		})
	}
	return out, nil
}

// ListUserVisibleGroupMeta 同时执行访问控制与状态页展示开关过滤。
func (s *HealthmonStore) ListUserVisibleGroupMeta(ctx context.Context, userID int) ([]apphealthmon.GroupMeta, error) {
	if userID <= 0 {
		return []apphealthmon.GroupMeta{}, nil
	}
	groups, err := s.db.Group.Query().Where(
		entgroup.StatusVisibleEQ(true),
		entgroup.Or(
			entgroup.IsExclusiveEQ(false),
			entgroup.And(
				entgroup.IsExclusiveEQ(true),
				entgroup.HasAllowedUsersWith(entuser.IDEQ(userID)),
			),
		),
	).Order(ent.Desc(entgroup.FieldSortWeight), ent.Desc(entgroup.FieldCreatedAt)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]apphealthmon.GroupMeta, 0, len(groups))
	for _, group := range groups {
		out = append(out, apphealthmon.GroupMeta{ID: group.ID, Name: group.Name, Platform: group.Platform})
	}
	return out, nil
}

// CountKeyAvailability 可调度 key 数。
func (s *HealthmonStore) CountKeyAvailability(ctx context.Context) (available, total int64, err error) {
	totalN, err := s.db.ChannelKey.Query().Count(ctx)
	if err != nil {
		return 0, 0, err
	}
	availN, err := s.db.ChannelKey.Query().
		Where(entchannelkey.StatusEQ(entchannelkey.StatusEnabled)).
		Count(ctx)
	if err != nil {
		return 0, 0, err
	}
	return int64(availN), int64(totalN), nil
}

// LatestErrors 每个 key 最近一条 relay 失败。
func (s *HealthmonStore) LatestErrors(ctx context.Context, since time.Time, keyIDs []int) ([]apphealthmon.LastErrorRow, error) {
	return s.latestErrorsBy(ctx, since, keyIDs, entupstreamrequestlog.FieldChannelKeyID)
}

// LatestErrorsByGroup 每个 group 最近一条 relay 失败。
func (s *HealthmonStore) LatestErrorsByGroup(ctx context.Context, since time.Time, groupIDs []int) ([]apphealthmon.LastErrorRow, error) {
	return s.latestErrorsBy(ctx, since, groupIDs, entupstreamrequestlog.FieldGroupID)
}

func (s *HealthmonStore) latestErrorsBy(ctx context.Context, since time.Time, ids []int, dimField string) ([]apphealthmon.LastErrorRow, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	limit := len(ids) * 3
	if limit < 50 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	q := s.db.UpstreamRequestLog.Query().Where(
		entupstreamrequestlog.CreatedAtGTE(since),
		entupstreamrequestlog.SourceEQ(entupstreamrequestlog.SourceRelay),
	)
	switch dimField {
	case entupstreamrequestlog.FieldChannelKeyID:
		q = q.Where(entupstreamrequestlog.ChannelKeyIDIn(ids...))
	case entupstreamrequestlog.FieldGroupID:
		q = q.Where(entupstreamrequestlog.GroupIDIn(ids...))
	}
	rows, err := q.Order(ent.Desc(entupstreamrequestlog.FieldCreatedAt)).Limit(limit).All(ctx)
	if err != nil {
		return nil, err
	}
	seen := make(map[int]struct{}, len(ids))
	out := make([]apphealthmon.LastErrorRow, 0, len(ids))
	for _, row := range rows {
		dimID := 0
		switch dimField {
		case entupstreamrequestlog.FieldChannelKeyID:
			dimID = row.ChannelKeyID
		case entupstreamrequestlog.FieldGroupID:
			dimID = row.GroupID
		}
		if dimID <= 0 {
			continue
		}
		if _, ok := seen[dimID]; ok {
			continue
		}
		seen[dimID] = struct{}{}
		out = append(out, apphealthmon.LastErrorRow{
			DimID:   dimID,
			At:      row.CreatedAt,
			Phase:   row.Phase,
			Message: row.Message,
		})
	}
	return out, nil
}
