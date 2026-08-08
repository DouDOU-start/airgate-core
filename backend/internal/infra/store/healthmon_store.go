package store

import (
	"context"
	"sort"
	"time"

	"entgo.io/ent/dialect"
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
	db         *ent.Client
	sqlDialect string
}

// NewHealthmonStore 创建健康监测仓储。
// 生产装配显式传 dialect.Postgres 以使用数据库原生 percentile_disc；
// SQLite 等测试环境不传时使用同口径的 Go 精确排序回退。
func NewHealthmonStore(db *ent.Client, sqlDialect ...string) *HealthmonStore {
	s := &HealthmonStore{db: db}
	if len(sqlDialect) > 0 {
		s.sqlDialect = sqlDialect[0]
	}
	return s
}

// AggregateSuccess 按 channel_key 聚合 relay 成功流量（排除 test/task）。
func (s *HealthmonStore) AggregateSuccess(ctx context.Context, since time.Time) ([]apphealthmon.SuccessAgg, error) {
	return s.aggregateSuccessBy(ctx, since, entusagelog.FieldChannelKeyID, true, nil)
}

// AggregateSuccessSummary 汇总全部 channel_key relay 成功流量。
// P95 必须直接基于全窗口样本计算，不能由各 key 的 P95 再合并。
func (s *HealthmonStore) AggregateSuccessSummary(ctx context.Context, since time.Time) (apphealthmon.SuccessAgg, error) {
	return s.aggregateSuccessSummary(ctx, since, entusagelog.FieldChannelKeyID, true, nil)
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

// AggregateSuccessSummaryByGroupIDs 汇总指定分组的 relay 成功流量。
// 空 allow-list 必须返回零值，避免权限过滤失效后退化为全量查询。
func (s *HealthmonStore) AggregateSuccessSummaryByGroupIDs(ctx context.Context, since time.Time, ids []int) (apphealthmon.SuccessAgg, error) {
	if len(ids) == 0 {
		return apphealthmon.SuccessAgg{}, nil
	}
	return s.aggregateSuccessSummary(ctx, since, entusagelog.FieldGroupID, true, ids)
}

type healthmonSuccessAggregateRow struct {
	DimID       int     `json:"dim_id"`
	Count       int64   `json:"count"`
	AvgDuration float64 `json:"avg_duration"`
	P95Duration int64   `json:"p95_duration"`
	MaxDuration float64 `json:"max_duration"`
	TTFTCount   int64   `json:"ttft_count"`
	AvgTTFT     float64 `json:"avg_ttft"`
	P95TTFT     int64   `json:"p95_ttft"`
	MaxTTFT     float64 `json:"max_ttft"`
}

func (s *HealthmonStore) aggregateSuccessBy(ctx context.Context, since time.Time, dimField string, requirePositiveDim bool, dimIDs []int) ([]apphealthmon.SuccessAgg, error) {
	var rows []healthmonSuccessAggregateRow
	q := s.healthmonSuccessQuery(since, dimField, requirePositiveDim, dimIDs)
	err := q.Modify(func(sel *entsql.Selector) {
		dimCol := sel.C(dimField)
		durCol := sel.C(entusagelog.FieldDurationMs)
		ttftCol := sel.C(entusagelog.FieldFirstTokenMs)
		p95Duration, p95TTFT := s.healthmonP95Expressions(durCol, ttftCol)
		sel.Select(
			entsql.As(dimCol, "dim_id"),
			entsql.As("COUNT(*)", "count"),
			entsql.As("COALESCE(AVG("+durCol+"),0)", "avg_duration"),
			entsql.As(p95Duration, "p95_duration"),
			entsql.As("COALESCE(MAX("+durCol+"),0)", "max_duration"),
			entsql.As("COUNT(CASE WHEN "+ttftCol+" > 0 THEN 1 END)", "ttft_count"),
			entsql.As("COALESCE(AVG(CASE WHEN "+ttftCol+" > 0 THEN "+ttftCol+" END),0)", "avg_ttft"),
			entsql.As(p95TTFT, "p95_ttft"),
			entsql.As("COALESCE(MAX(CASE WHEN "+ttftCol+" > 0 THEN "+ttftCol+" END),0)", "max_ttft"),
		).GroupBy(dimCol)
	}).Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	if s.sqlDialect != dialect.Postgres {
		p95ByDim, err := s.healthmonP95ByDimension(ctx, since, dimField, requirePositiveDim, dimIDs)
		if err != nil {
			return nil, err
		}
		for i := range rows {
			rows[i].P95Duration = p95ByDim[rows[i].DimID].duration
			rows[i].P95TTFT = p95ByDim[rows[i].DimID].ttft
		}
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
			P95Duration: row.P95Duration,
			MaxDuration: row.MaxDuration,
			TTFTCount:   row.TTFTCount,
			AvgTTFT:     row.AvgTTFT,
			P95TTFT:     row.P95TTFT,
			MaxTTFT:     row.MaxTTFT,
		})
	}
	return out, nil
}

func (s *HealthmonStore) aggregateSuccessSummary(ctx context.Context, since time.Time, filterDimField string, requirePositiveDim bool, dimIDs []int) (apphealthmon.SuccessAgg, error) {
	var rows []healthmonSuccessAggregateRow
	q := s.healthmonSuccessQuery(since, filterDimField, requirePositiveDim, dimIDs)
	err := q.Modify(func(sel *entsql.Selector) {
		durCol := sel.C(entusagelog.FieldDurationMs)
		ttftCol := sel.C(entusagelog.FieldFirstTokenMs)
		p95Duration, p95TTFT := s.healthmonP95Expressions(durCol, ttftCol)
		sel.Select(
			entsql.As("COUNT(*)", "count"),
			entsql.As("COALESCE(AVG("+durCol+"),0)", "avg_duration"),
			entsql.As(p95Duration, "p95_duration"),
			entsql.As("COALESCE(MAX("+durCol+"),0)", "max_duration"),
			entsql.As("COUNT(CASE WHEN "+ttftCol+" > 0 THEN 1 END)", "ttft_count"),
			entsql.As("COALESCE(AVG(CASE WHEN "+ttftCol+" > 0 THEN "+ttftCol+" END),0)", "avg_ttft"),
			entsql.As(p95TTFT, "p95_ttft"),
			entsql.As("COALESCE(MAX(CASE WHEN "+ttftCol+" > 0 THEN "+ttftCol+" END),0)", "max_ttft"),
		)
	}).Scan(ctx, &rows)
	if err != nil {
		return apphealthmon.SuccessAgg{}, err
	}
	if len(rows) == 0 {
		return apphealthmon.SuccessAgg{}, nil
	}
	row := rows[0]
	if s.sqlDialect != dialect.Postgres {
		p95, err := s.healthmonP95Summary(ctx, since, filterDimField, requirePositiveDim, dimIDs)
		if err != nil {
			return apphealthmon.SuccessAgg{}, err
		}
		row.P95Duration = p95.duration
		row.P95TTFT = p95.ttft
	}
	return apphealthmon.SuccessAgg{
		Count:       row.Count,
		AvgDuration: row.AvgDuration,
		P95Duration: row.P95Duration,
		MaxDuration: row.MaxDuration,
		TTFTCount:   row.TTFTCount,
		AvgTTFT:     row.AvgTTFT,
		P95TTFT:     row.P95TTFT,
		MaxTTFT:     row.MaxTTFT,
	}, nil
}

func (s *HealthmonStore) healthmonSuccessQuery(since time.Time, dimField string, requirePositiveDim bool, dimIDs []int) *ent.UsageLogQuery {
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
	return q
}

// healthmonP95Expressions 返回与 nearest-rank 定义一致的 PostgreSQL P95 表达式。
// 非 PostgreSQL 方言返回常量占位，随后由 Go 精确排序回填。
func (s *HealthmonStore) healthmonP95Expressions(durationCol, ttftCol string) (string, string) {
	if s.sqlDialect != dialect.Postgres {
		return "COALESCE(MAX(" + durationCol + "*0),0)",
			"COALESCE(MAX(" + ttftCol + "*0),0)"
	}
	return "COALESCE(PERCENTILE_DISC(0.95) WITHIN GROUP (ORDER BY " + durationCol + "),0)",
		"COALESCE(PERCENTILE_DISC(0.95) WITHIN GROUP (ORDER BY " + ttftCol + ") FILTER (WHERE " + ttftCol + " > 0),0)"
}

type healthmonP95 struct {
	duration int64
	ttft     int64
}

type healthmonLatencySamples struct {
	duration []int64
	ttft     []int64
}

func (s *HealthmonStore) healthmonP95ByDimension(ctx context.Context, since time.Time, dimField string, requirePositiveDim bool, dimIDs []int) (map[int]healthmonP95, error) {
	fields := []string{dimField, entusagelog.FieldDurationMs, entusagelog.FieldFirstTokenMs}
	logs, err := s.healthmonSuccessQuery(since, dimField, requirePositiveDim, dimIDs).
		Select(fields...).All(ctx)
	if err != nil {
		return nil, err
	}
	samplesByDim := make(map[int]*healthmonLatencySamples)
	for _, log := range logs {
		dimID := healthmonUsageLogDimID(log, dimField)
		if dimID <= 0 {
			continue
		}
		samples := samplesByDim[dimID]
		if samples == nil {
			samples = &healthmonLatencySamples{}
			samplesByDim[dimID] = samples
		}
		samples.duration = append(samples.duration, log.DurationMs)
		if log.FirstTokenMs > 0 {
			samples.ttft = append(samples.ttft, log.FirstTokenMs)
		}
	}
	out := make(map[int]healthmonP95, len(samplesByDim))
	for dimID, samples := range samplesByDim {
		out[dimID] = healthmonP95{
			duration: healthmonNearestRankP95(samples.duration),
			ttft:     healthmonNearestRankP95(samples.ttft),
		}
	}
	return out, nil
}

func (s *HealthmonStore) healthmonP95Summary(ctx context.Context, since time.Time, filterDimField string, requirePositiveDim bool, dimIDs []int) (healthmonP95, error) {
	logs, err := s.healthmonSuccessQuery(since, filterDimField, requirePositiveDim, dimIDs).
		Select(entusagelog.FieldDurationMs, entusagelog.FieldFirstTokenMs).All(ctx)
	if err != nil {
		return healthmonP95{}, err
	}
	durations := make([]int64, 0, len(logs))
	ttfts := make([]int64, 0, len(logs))
	for _, log := range logs {
		durations = append(durations, log.DurationMs)
		if log.FirstTokenMs > 0 {
			ttfts = append(ttfts, log.FirstTokenMs)
		}
	}
	return healthmonP95{
		duration: healthmonNearestRankP95(durations),
		ttft:     healthmonNearestRankP95(ttfts),
	}, nil
}

func healthmonUsageLogDimID(log *ent.UsageLog, dimField string) int {
	switch dimField {
	case entusagelog.FieldChannelKeyID:
		return log.ChannelKeyID
	case entusagelog.FieldGroupID:
		return log.GroupID
	default:
		return 0
	}
}

// healthmonNearestRankP95 与 PostgreSQL percentile_disc(0.95) 同口径：
// 排序后取 rank=ceil(0.95*N) 的观测值。
func healthmonNearestRankP95(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	rank := (95*len(values) + 99) / 100
	return values[rank-1]
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
		ErrorCode  string `json:"error_code"`
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
		codeCol := sel.C(entupstreamrequestlog.FieldErrorCode)
		billedCol := sel.C(entupstreamrequestlog.FieldBilled)
		repeatCol := sel.C(entupstreamrequestlog.FieldRepeatCount)
		sel.Select(
			entsql.As(dimCol, "dim_id"),
			entsql.As(statusCol, "status_code"),
			entsql.As(phaseCol, "phase"),
			entsql.As(codeCol, "error_code"),
			entsql.As(billedCol, "billed"),
			entsql.As("COALESCE(SUM("+repeatCol+"),0)", "count"),
		).GroupBy(dimCol, statusCol, phaseCol, codeCol, billedCol)
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
			ErrorCode:  row.ErrorCode,
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
