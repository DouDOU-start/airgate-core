package healthmon

import (
	"context"
	"time"
)

// 用户渠道状态页系统总开关（settings 组 site）。
// 缺省视为开启；仅显式 "false" 关闭。分组级 status_visible 仍生效。
const (
	settingGroupSite               = "site"
	settingKeyChannelStatusEnabled = "channel_status_enabled"
)

// SettingItem 设置读取窄类型（避免依赖 settings 包）。
type SettingItem struct {
	Key   string
	Value string
}

// SettingsLister 读取系统设置（由 bootstrap 适配 settings.Service）。
type SettingsLister interface {
	List(ctx context.Context, group string) ([]SettingItem, error)
}

// Repository 健康监测数据访问（由 store 实现）。
type Repository interface {
	// AggregateSuccess 按 channel_key 聚合 relay 成功流量。
	AggregateSuccess(ctx context.Context, since time.Time) ([]SuccessAgg, error)
	// AggregateSuccessSummary 汇总全部 relay 成功流量；P95 直接基于全窗口样本计算。
	AggregateSuccessSummary(ctx context.Context, since time.Time) (SuccessAgg, error)
	// AggregateSuccessByGroup 按 group 聚合 relay 成功流量。
	AggregateSuccessByGroup(ctx context.Context, since time.Time) ([]SuccessAgg, error)
	// AggregateFailureRaws 按 channel_key + status + phase 聚合 relay 失败。
	AggregateFailureRaws(ctx context.Context, since time.Time) ([]FailureRaw, error)
	// AggregateFailureRawsByGroup 按 group + status + phase 聚合 relay 失败。
	AggregateFailureRawsByGroup(ctx context.Context, since time.Time) ([]FailureRaw, error)
	// ListKeyMeta 列出密钥端点元数据；ids 空=全部。
	ListKeyMeta(ctx context.Context, ids []int) ([]KeyMeta, error)
	// ListGroupMeta 列出分组元数据；ids 空=全部。
	ListGroupMeta(ctx context.Context, ids []int) ([]GroupMeta, error)
	// ListUserVisibleGroupMeta 列出用户有权访问且允许在状态页展示的分组。
	ListUserVisibleGroupMeta(ctx context.Context, userID int) ([]GroupMeta, error)
	// AggregateSuccessByGroupIDs 仅聚合指定分组的 relay 成功流量；ids 空返回空。
	AggregateSuccessByGroupIDs(ctx context.Context, since time.Time, ids []int) ([]SuccessAgg, error)
	// AggregateSuccessSummaryByGroupIDs 汇总指定分组的 relay 成功流量；ids 空返回零值。
	AggregateSuccessSummaryByGroupIDs(ctx context.Context, since time.Time, ids []int) (SuccessAgg, error)
	// AggregateFailureRawsByGroupIDs 仅聚合指定分组的 relay 失败；ids 空返回空。
	AggregateFailureRawsByGroupIDs(ctx context.Context, since time.Time, ids []int) ([]FailureRaw, error)
	// CountKeyAvailability 统计 enabled / 全部 key 数。
	CountKeyAvailability(ctx context.Context) (available, total int64, err error)
	// LatestErrors 每个 channel_key 最近一条 relay 失败。
	LatestErrors(ctx context.Context, since time.Time, keyIDs []int) ([]LastErrorRow, error)
	// LatestErrorsByGroup 每个 group 最近一条 relay 失败。
	LatestErrorsByGroup(ctx context.Context, since time.Time, groupIDs []int) ([]LastErrorRow, error)
}

// FailureRaw 失败原始聚合行（store 输出，service 负责分类）。
// ChannelKeyID / GroupID 按聚合维度二选一填充。
type FailureRaw struct {
	ChannelKeyID int
	GroupID      int
	StatusCode   int
	Phase        string
	ErrorCode    string
	Billed       bool
	Count        int64
}

// Service 健康监测用例。
type Service struct {
	repo      Repository
	settings  SettingsLister
	minSample int
	now       func() time.Time
}

// NewService 创建健康监测服务。
func NewService(repo Repository) *Service {
	return &Service{
		repo:      repo,
		minSample: DefaultMinSample,
		now:       time.Now,
	}
}

// SetSettingsLister 注入系统设置读取（渠道状态页总开关）。
func (s *Service) SetSettingsLister(sl SettingsLister) {
	s.settings = sl
}

// ChannelStatusEnabled 用户渠道状态页是否对用户开放。
// 未配置 / 读取失败时默认开启，与历史行为一致。
func (s *Service) ChannelStatusEnabled(ctx context.Context) bool {
	if s.settings == nil {
		return true
	}
	items, err := s.settings.List(ctx, settingGroupSite)
	if err != nil {
		return true
	}
	for _, item := range items {
		if item.Key == settingKeyChannelStatusEnabled {
			return item.Value != "false"
		}
	}
	return true
}

// Overview 全局窗口总览。
func (s *Service) Overview(ctx context.Context, windowRaw string) (Overview, error) {
	window, dur := ParseWindow(windowRaw)
	since := s.now().Add(-dur)

	success, err := s.repo.AggregateSuccessSummary(ctx, since)
	if err != nil {
		return Overview{}, err
	}
	raws, err := s.repo.AggregateFailureRaws(ctx, since)
	if err != nil {
		return Overview{}, err
	}
	avail, total, err := s.repo.CountKeyAvailability(ctx)
	if err != nil {
		return Overview{}, err
	}

	metrics := successMetricsFromAgg(success)
	metrics.sCount = subtractBilledFailures(metrics.sCount, raws)
	counts := mergeFailureCounts(raws, func(r FailureRaw) int { return r.ChannelKeyID })
	eCount := SLAErrorCount(counts)
	sample := BuildSample(metrics.sCount, eCount, s.minSample)
	sr, er := Rates(sample)

	return Overview{
		Window:      window,
		Sample:      sample,
		SuccessRate: sr,
		ErrorRate:   er,
		Counts:      counts,
		Latency:     metrics.latency,
		TTFT:        metrics.ttft,
		HealthScore: ComputeHealthScore(sample, er, metrics.ttft.P95Ms, metrics.hasTTFT),
		Availability: Availability{
			ChannelKeysAvailable: avail,
			ChannelKeysTotal:     total,
		},
	}, nil
}

// ListChannelKeys 按密钥端点列出健康行。
func (s *Service) ListChannelKeys(ctx context.Context, filter ListFilter) ([]EntityRow, error) {
	window, since := s.windowSince(filter.Window)

	metas, err := s.repo.ListKeyMeta(ctx, filter.IDs)
	if err != nil {
		return nil, err
	}
	success, err := s.repo.AggregateSuccess(ctx, since)
	if err != nil {
		return nil, err
	}
	raws, err := s.repo.AggregateFailureRaws(ctx, since)
	if err != nil {
		return nil, err
	}

	successByID := indexSuccess(success)
	subtractBilledFailuresByID(successByID, raws, func(r FailureRaw) int { return r.ChannelKeyID })
	countsByID := indexFailureCounts(raws, func(r FailureRaw) int { return r.ChannelKeyID })

	keyIDs := make([]int, 0, len(metas))
	for _, m := range metas {
		keyIDs = append(keyIDs, m.ID)
	}
	lastErrs, err := s.repo.LatestErrors(ctx, since, keyIDs)
	if err != nil {
		return nil, err
	}
	lastByID := indexLastErrors(lastErrs)

	out := make([]EntityRow, 0, len(metas))
	for _, m := range metas {
		row := buildEntityRow(entityBuildInput{
			ID:           m.ID,
			Kind:         EntityChannelKey,
			Name:         m.Name,
			ChannelID:    m.ChannelID,
			ChannelName:  m.ChannelName,
			Type:         m.Type,
			SchedStatus:  m.Status,
			HealthStatus: m.HealthStatus,
			Window:       window,
			Success:      successByID[m.ID],
			Counts:       countsByID[m.ID],
			Last:         lastByID[m.ID],
			MinSample:    s.minSample,
		})
		if filter.OnlyUnhealthy && !isUnhealthy(row) {
			continue
		}
		out = append(out, row)
	}
	sortEntityRows(out)
	return out, nil
}

// ListGroups 按分组列出健康行（请求量 / 成功率 / 首字 / 耗时）。
func (s *Service) ListGroups(ctx context.Context, filter ListFilter) ([]EntityRow, error) {
	window, since := s.windowSince(filter.Window)

	metas, err := s.repo.ListGroupMeta(ctx, filter.IDs)
	if err != nil {
		return nil, err
	}
	success, err := s.repo.AggregateSuccessByGroup(ctx, since)
	if err != nil {
		return nil, err
	}
	raws, err := s.repo.AggregateFailureRawsByGroup(ctx, since)
	if err != nil {
		return nil, err
	}

	successByID := indexSuccess(success)
	subtractBilledFailuresByID(successByID, raws, func(r FailureRaw) int { return r.GroupID })
	countsByID := indexFailureCounts(raws, func(r FailureRaw) int { return r.GroupID })

	groupIDs := make([]int, 0, len(metas))
	for _, m := range metas {
		groupIDs = append(groupIDs, m.ID)
	}
	lastErrs, err := s.repo.LatestErrorsByGroup(ctx, since, groupIDs)
	if err != nil {
		return nil, err
	}
	lastByID := indexLastErrors(lastErrs)

	out := make([]EntityRow, 0, len(metas))
	for _, m := range metas {
		row := buildEntityRow(entityBuildInput{
			ID:        m.ID,
			Kind:      EntityGroup,
			Name:      m.Name,
			Platform:  m.Platform,
			Window:    window,
			Success:   successByID[m.ID],
			Counts:    countsByID[m.ID],
			Last:      lastByID[m.ID],
			MinSample: s.minSample,
		})
		if filter.OnlyUnhealthy && !isUnhealthy(row) {
			continue
		}
		out = append(out, row)
	}
	// 分组默认按请求量 desc，再按错误率 desc（用户更关心“谁在打、打得怎样”）
	sortGroupRows(out)
	return out, nil
}

// ErrChannelStatusDisabled 用户渠道状态页被系统设置关闭。
var ErrChannelStatusDisabled = errChannelStatusDisabled{}

type errChannelStatusDisabled struct{}

func (errChannelStatusDisabled) Error() string { return "渠道状态页未开启" }

// UserOverview 汇总当前用户有权访问且允许展示的分组健康状态。
func (s *Service) UserOverview(ctx context.Context, userID int, windowRaw string) (UserOverview, error) {
	if !s.ChannelStatusEnabled(ctx) {
		return UserOverview{}, ErrChannelStatusDisabled
	}
	window, dur := ParseWindow(windowRaw)
	now := s.now()
	metas, err := s.repo.ListUserVisibleGroupMeta(ctx, userID)
	if err != nil {
		return UserOverview{}, err
	}
	ids := groupMetaIDs(metas)
	success, err := s.repo.AggregateSuccessSummaryByGroupIDs(ctx, now.Add(-dur), ids)
	if err != nil {
		return UserOverview{}, err
	}
	raws, err := s.repo.AggregateFailureRawsByGroupIDs(ctx, now.Add(-dur), ids)
	if err != nil {
		return UserOverview{}, err
	}

	metrics := successMetricsFromAgg(success)
	metrics.sCount = subtractBilledFailures(metrics.sCount, raws)
	counts := mergeFailureCounts(raws, func(r FailureRaw) int { return r.GroupID })
	sample := BuildSample(metrics.sCount, SLAErrorCount(counts), s.minSample)
	successRate, errorRate := Rates(sample)
	return UserOverview{
		Window:      window,
		Sample:      sample,
		SuccessRate: successRate,
		ErrorRate:   errorRate,
		Latency:     metrics.latency,
		TTFT:        metrics.ttft,
		HealthScore: ComputeHealthScore(sample, errorRate, metrics.ttft.P95Ms, metrics.hasTTFT),
		UpdatedAt:   now,
	}, nil
}

// ListUserGroups 返回当前用户有权访问且允许展示的分组健康快照。
func (s *Service) ListUserGroups(ctx context.Context, userID int, windowRaw string) ([]UserGroupStatus, error) {
	if !s.ChannelStatusEnabled(ctx) {
		return nil, ErrChannelStatusDisabled
	}
	window, dur := ParseWindow(windowRaw)
	metas, err := s.repo.ListUserVisibleGroupMeta(ctx, userID)
	if err != nil {
		return nil, err
	}
	ids := groupMetaIDs(metas)
	since := s.now().Add(-dur)
	success, err := s.repo.AggregateSuccessByGroupIDs(ctx, since, ids)
	if err != nil {
		return nil, err
	}
	raws, err := s.repo.AggregateFailureRawsByGroupIDs(ctx, since, ids)
	if err != nil {
		return nil, err
	}

	successByID := indexSuccess(success)
	subtractBilledFailuresByID(successByID, raws, func(r FailureRaw) int { return r.GroupID })
	countsByID := indexFailureCounts(raws, func(r FailureRaw) int { return r.GroupID })
	rows := make([]EntityRow, 0, len(metas))
	for _, meta := range metas {
		rows = append(rows, buildEntityRow(entityBuildInput{
			ID:        meta.ID,
			Kind:      EntityGroup,
			Name:      meta.Name,
			Platform:  meta.Platform,
			Window:    window,
			Success:   successByID[meta.ID],
			Counts:    countsByID[meta.ID],
			MinSample: s.minSample,
		}))
	}
	out := make([]UserGroupStatus, 0, len(rows))
	for _, row := range rows {
		out = append(out, UserGroupStatus{
			ID:          row.ID,
			Name:        row.Name,
			Platform:    row.Platform,
			Status:      userHealthStatus(row.Sample, row.HealthScore),
			Window:      row.Window,
			Sample:      row.Sample,
			SuccessRate: row.SuccessRate,
			ErrorRate:   row.ErrorRate,
			Latency:     row.Latency,
			TTFT:        row.TTFT,
			HealthScore: row.HealthScore,
		})
	}
	return out, nil
}

func groupMetaIDs(metas []GroupMeta) []int {
	ids := make([]int, 0, len(metas))
	for _, meta := range metas {
		ids = append(ids, meta.ID)
	}
	return ids
}

func userHealthStatus(sample Sample, score *int) UserHealthStatus {
	if sample.Idle {
		return UserHealthIdle
	}
	if sample.LowSample {
		return UserHealthLowSample
	}
	if score == nil || *score < 70 {
		return UserHealthUnhealthy
	}
	if *score < 90 {
		return UserHealthDegraded
	}
	return UserHealthHealthy
}

func (s *Service) windowSince(window Window) (Window, time.Time) {
	w, dur := ParseWindow(string(window))
	if window == "" {
		w, dur = ParseWindow("1h")
	}
	return w, s.now().Add(-dur)
}

type successMetrics struct {
	sCount  int64
	latency Latency
	ttft    Latency
	hasTTFT bool
}

func successMetricsFromAgg(row SuccessAgg) successMetrics {
	return successMetrics{
		sCount: row.Count,
		latency: Latency{
			AvgMs: int64(row.AvgDuration),
			P95Ms: row.P95Duration,
			MaxMs: int64(row.MaxDuration),
		},
		ttft: Latency{
			AvgMs: int64(row.AvgTTFT),
			P95Ms: row.P95TTFT,
			MaxMs: int64(row.MaxTTFT),
		},
		hasTTFT: row.TTFTCount > 0 || row.P95TTFT > 0,
	}
}

func mergeFailureCounts(raws []FailureRaw, dim func(FailureRaw) int) Counts {
	var counts Counts
	for _, raw := range raws {
		if dim(raw) <= 0 {
			continue
		}
		MergeCounts(&counts, ClassifyFailure(raw.StatusCode, raw.Phase, raw.ErrorCode), raw.Count)
	}
	return counts
}

func indexSuccess(rows []SuccessAgg) map[int]SuccessAgg {
	out := make(map[int]SuccessAgg, len(rows))
	for _, row := range rows {
		out[row.DimID] = row
	}
	return out
}

func subtractBilledFailures(successCount int64, raws []FailureRaw) int64 {
	for _, raw := range raws {
		if raw.Billed && raw.Count > 0 {
			successCount -= raw.Count
		}
	}
	if successCount < 0 {
		return 0
	}
	return successCount
}

func subtractBilledFailuresByID(successByID map[int]SuccessAgg, raws []FailureRaw, dim func(FailureRaw) int) {
	billedByID := make(map[int]int64)
	for _, raw := range raws {
		id := dim(raw)
		if id > 0 && raw.Billed && raw.Count > 0 {
			billedByID[id] += raw.Count
		}
	}
	for id, billed := range billedByID {
		row := successByID[id]
		row.Count -= billed
		if row.Count < 0 {
			row.Count = 0
		}
		successByID[id] = row
	}
}

func indexFailureCounts(raws []FailureRaw, dim func(FailureRaw) int) map[int]Counts {
	tmp := make(map[int]*Counts)
	for _, raw := range raws {
		id := dim(raw)
		if id <= 0 {
			continue
		}
		c := tmp[id]
		if c == nil {
			c = &Counts{}
			tmp[id] = c
		}
		MergeCounts(c, ClassifyFailure(raw.StatusCode, raw.Phase, raw.ErrorCode), raw.Count)
	}
	out := make(map[int]Counts, len(tmp))
	for id, c := range tmp {
		out[id] = *c
	}
	return out
}

func indexLastErrors(rows []LastErrorRow) map[int]LastErrorRow {
	out := make(map[int]LastErrorRow, len(rows))
	for _, row := range rows {
		out[row.DimID] = row
	}
	return out
}

type entityBuildInput struct {
	ID           int
	Kind         EntityKind
	Name         string
	ChannelID    int
	ChannelName  string
	Platform     string
	Type         string
	SchedStatus  string
	HealthStatus string
	Window       Window
	Success      SuccessAgg
	Counts       Counts
	Last         LastErrorRow
	MinSample    int
}

func buildEntityRow(in entityBuildInput) EntityRow {
	eCount := SLAErrorCount(in.Counts)
	sample := BuildSample(in.Success.Count, eCount, in.MinSample)
	sr, er := Rates(sample)
	hasTTFT := in.Success.TTFTCount > 0 || in.Success.P95TTFT > 0
	latency := Latency{
		AvgMs: int64(in.Success.AvgDuration),
		P95Ms: in.Success.P95Duration,
		MaxMs: int64(in.Success.MaxDuration),
	}
	ttft := Latency{
		AvgMs: int64(in.Success.AvgTTFT),
		P95Ms: in.Success.P95TTFT,
		MaxMs: int64(in.Success.MaxTTFT),
	}
	score := ComputeHealthScore(sample, er, ttft.P95Ms, hasTTFT)
	row := EntityRow{
		ID:           in.ID,
		Kind:         in.Kind,
		Name:         in.Name,
		ChannelID:    in.ChannelID,
		ChannelName:  in.ChannelName,
		Platform:     in.Platform,
		Type:         in.Type,
		SchedStatus:  in.SchedStatus,
		HealthStatus: in.HealthStatus,
		Window:       in.Window,
		Sample:       sample,
		SuccessRate:  sr,
		ErrorRate:    er,
		Counts:       in.Counts,
		Latency:      latency,
		TTFT:         ttft,
		HealthScore:  score,
	}
	if !in.Last.At.IsZero() {
		row.LastError = &LastError{At: in.Last.At, Phase: in.Last.Phase, Message: in.Last.Message}
	}
	return row
}

func isUnhealthy(row EntityRow) bool {
	if row.Sample.Idle {
		return false
	}
	return row.ErrorRate > 0 ||
		(row.HealthStatus != "" && row.HealthStatus != "healthy") ||
		(row.HealthScore != nil && *row.HealthScore < 90)
}

func sortEntityRows(rows []EntityRow) {
	for i := 0; i < len(rows); i++ {
		for j := i + 1; j < len(rows); j++ {
			if lessEntity(rows[j], rows[i]) {
				rows[i], rows[j] = rows[j], rows[i]
			}
		}
	}
}

func lessEntity(a, b EntityRow) bool {
	if a.ErrorRate != b.ErrorRate {
		return a.ErrorRate > b.ErrorRate
	}
	if a.Sample.N != b.Sample.N {
		return a.Sample.N > b.Sample.N
	}
	return a.ID < b.ID
}

func sortGroupRows(rows []EntityRow) {
	for i := 0; i < len(rows); i++ {
		for j := i + 1; j < len(rows); j++ {
			if lessGroup(rows[j], rows[i]) {
				rows[i], rows[j] = rows[j], rows[i]
			}
		}
	}
}

func lessGroup(a, b EntityRow) bool {
	// 有流量的分组优先，再按请求量、错误率。
	if a.Sample.Idle != b.Sample.Idle {
		return !a.Sample.Idle && b.Sample.Idle
	}
	if a.Sample.N != b.Sample.N {
		return a.Sample.N > b.Sample.N
	}
	if a.ErrorRate != b.ErrorRate {
		return a.ErrorRate > b.ErrorRate
	}
	return a.ID < b.ID
}
