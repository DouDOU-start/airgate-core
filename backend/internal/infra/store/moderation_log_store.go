package store

import (
	"context"
	"time"

	"github.com/DouDOU-start/airgate-core/ent"
	entmoderationlog "github.com/DouDOU-start/airgate-core/ent/moderationlog"
	entuser "github.com/DouDOU-start/airgate-core/ent/user"
	appriskcontrol "github.com/DouDOU-start/airgate-core/internal/app/riskcontrol"
	"github.com/DouDOU-start/airgate-core/internal/moderation"
)

// ModerationLogStore 使用 Ent 实现审核日志仓储：
// 写入侧实现 moderation.LogStore（引擎异步落库），查询侧实现 riskcontrol.Repository。
type ModerationLogStore struct {
	db *ent.Client
}

// NewModerationLogStore 创建审核日志仓储。
func NewModerationLogStore(db *ent.Client) *ModerationLogStore {
	return &ModerationLogStore{db: db}
}

// Create 写入一条审核日志。
func (s *ModerationLogStore) Create(ctx context.Context, e moderation.LogEntry) error {
	return s.db.ModerationLog.Create().
		SetRequestID(e.RequestID).
		SetUserID(e.UserID).
		SetUserEmailSnapshot(e.UserEmail).
		SetAPIKeyID(e.APIKeyID).
		SetGroupID(e.GroupID).
		SetGroupNameSnapshot(e.GroupName).
		SetEndpoint(e.Endpoint).
		SetProtocol(e.Protocol).
		SetModel(e.Model).
		SetMode(e.Mode).
		SetAction(e.Action).
		SetFlagged(e.Flagged).
		SetHighestCategory(e.HighestCategory).
		SetHighestScore(e.HighestScore).
		SetMatchedKeyword(e.MatchedKeyword).
		SetCategoryScores(e.CategoryScores).
		SetThresholdSnapshot(e.ThresholdSnapshot).
		SetInputExcerpt(e.InputExcerpt).
		SetInputHash(e.InputHash).
		SetUpstreamLatencyMs(e.UpstreamLatencyMS).
		SetQueueDelayMs(e.QueueDelayMS).
		SetError(e.Error).
		SetViolationCount(e.ViolationCount).
		SetAutoBanned(e.AutoBanned).
		SetEmailSent(e.EmailSent).
		Exec(ctx)
}

// CountFlaggedSince 统计滑窗内计入封号的违规数：flagged 且 action 非 hash_block
// （重复内容不重复计数）；且晚于该用户最近一次 auto_banned 行——每次封禁后计数
// 从零重算，解封后不会因旧账立即再触发封禁。
func (s *ModerationLogStore) CountFlaggedSince(ctx context.Context, userID int, since time.Time) (int, error) {
	lastBan, err := s.db.ModerationLog.Query().
		Where(
			entmoderationlog.UserIDEQ(userID),
			entmoderationlog.AutoBannedEQ(true),
		).
		Order(ent.Desc(entmoderationlog.FieldCreatedAt)).
		First(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return 0, err
	}
	query := s.db.ModerationLog.Query().
		Where(
			entmoderationlog.UserIDEQ(userID),
			entmoderationlog.FlaggedEQ(true),
			entmoderationlog.ActionNEQ(moderation.ActionHashBlock),
			entmoderationlog.CreatedAtGTE(since),
		)
	if lastBan != nil {
		query = query.Where(entmoderationlog.CreatedAtGT(lastBan.CreatedAt))
	}
	return query.Count(ctx)
}

// Cleanup 双保留期 TTL 清理：命中行删 hitBefore 之前的，未命中行删 nonHitBefore 之前的。
func (s *ModerationLogStore) Cleanup(ctx context.Context, hitBefore, nonHitBefore time.Time) (moderation.CleanupResult, error) {
	hit, err := s.db.ModerationLog.Delete().
		Where(
			entmoderationlog.FlaggedEQ(true),
			entmoderationlog.CreatedAtLT(hitBefore),
		).
		Exec(ctx)
	if err != nil {
		return moderation.CleanupResult{}, err
	}
	nonHit, err := s.db.ModerationLog.Delete().
		Where(
			entmoderationlog.FlaggedEQ(false),
			entmoderationlog.CreatedAtLT(nonHitBefore),
		).
		Exec(ctx)
	if err != nil {
		return moderation.CleanupResult{}, err
	}
	return moderation.CleanupResult{
		DeletedHit:    int64(hit),
		DeletedNonHit: int64(nonHit),
		FinishedAt:    time.Now(),
	}, nil
}

// List 分页查询（时间倒序，ID 同刻回退保证稳定分页）；附带查询关联用户当前状态。
func (s *ModerationLogStore) List(ctx context.Context, filter appriskcontrol.ListFilter) ([]appriskcontrol.Record, int64, error) {
	base := applyModerationLogFilter(s.db.ModerationLog.Query(), filter)
	total, err := base.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	items, err := base.
		Order(ent.Desc(entmoderationlog.FieldCreatedAt), ent.Desc(entmoderationlog.FieldID)).
		Offset((filter.Page - 1) * filter.PageSize).
		Limit(filter.PageSize).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}
	statuses, err := s.userStatuses(ctx, items)
	if err != nil {
		return nil, 0, err
	}
	result := make([]appriskcontrol.Record, 0, len(items))
	for _, item := range items {
		result = append(result, mapModerationLog(item, statuses[item.UserID]))
	}
	return result, int64(total), nil
}

// userStatuses 批量查询涉及用户的当前状态（零 FK 表，手动补关联）。
func (s *ModerationLogStore) userStatuses(ctx context.Context, items []*ent.ModerationLog) (map[int]string, error) {
	idSet := make(map[int]struct{}, len(items))
	for _, item := range items {
		if item.UserID > 0 {
			idSet[item.UserID] = struct{}{}
		}
	}
	if len(idSet) == 0 {
		return nil, nil
	}
	ids := make([]int, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	users, err := s.db.User.Query().
		Where(entuser.IDIn(ids...)).
		Select(entuser.FieldID, entuser.FieldStatus).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[int]string, len(users))
	for _, u := range users {
		out[u.ID] = string(u.Status)
	}
	return out, nil
}

func applyModerationLogFilter(query *ent.ModerationLogQuery, filter appriskcontrol.ListFilter) *ent.ModerationLogQuery {
	switch filter.Result {
	case appriskcontrol.ResultHit:
		query = query.Where(entmoderationlog.FlaggedEQ(true))
	case appriskcontrol.ResultBlocked:
		query = query.Where(entmoderationlog.ActionIn(
			moderation.ActionBlock, moderation.ActionKeywordBlock, moderation.ActionHashBlock,
		))
	case appriskcontrol.ResultPass:
		query = query.Where(entmoderationlog.FlaggedEQ(false), entmoderationlog.ErrorEQ(""))
	case appriskcontrol.ResultError:
		query = query.Where(entmoderationlog.ErrorNEQ(""))
	}
	if filter.GroupID != nil {
		query = query.Where(entmoderationlog.GroupIDEQ(*filter.GroupID))
	}
	if filter.Endpoint != "" {
		query = query.Where(entmoderationlog.EndpointEQ(filter.Endpoint))
	}
	if filter.Search != "" {
		query = query.Where(entmoderationlog.Or(
			entmoderationlog.RequestIDContainsFold(filter.Search),
			entmoderationlog.UserEmailSnapshotContainsFold(filter.Search),
			entmoderationlog.ModelContainsFold(filter.Search),
			entmoderationlog.MatchedKeywordContainsFold(filter.Search),
			entmoderationlog.InputExcerptContainsFold(filter.Search),
		))
	}
	if filter.From != nil {
		query = query.Where(entmoderationlog.CreatedAtGTE(*filter.From))
	}
	if filter.To != nil {
		query = query.Where(entmoderationlog.CreatedAtLT(*filter.To))
	}
	return query
}

func mapModerationLog(item *ent.ModerationLog, userStatus string) appriskcontrol.Record {
	return appriskcontrol.Record{
		ID:                int64(item.ID),
		RequestID:         item.RequestID,
		UserID:            item.UserID,
		UserEmail:         item.UserEmailSnapshot,
		APIKeyID:          item.APIKeyID,
		GroupID:           item.GroupID,
		GroupName:         item.GroupNameSnapshot,
		Endpoint:          item.Endpoint,
		Protocol:          item.Protocol,
		Model:             item.Model,
		Mode:              item.Mode,
		Action:            item.Action,
		Flagged:           item.Flagged,
		HighestCategory:   item.HighestCategory,
		HighestScore:      item.HighestScore,
		MatchedKeyword:    item.MatchedKeyword,
		CategoryScores:    item.CategoryScores,
		ThresholdSnapshot: item.ThresholdSnapshot,
		InputExcerpt:      item.InputExcerpt,
		InputHash:         item.InputHash,
		UpstreamLatencyMS: item.UpstreamLatencyMs,
		QueueDelayMS:      item.QueueDelayMs,
		Error:             item.Error,
		ViolationCount:    item.ViolationCount,
		AutoBanned:        item.AutoBanned,
		EmailSent:         item.EmailSent,
		UserStatus:        userStatus,
		CreatedAt:         item.CreatedAt.Format(time.RFC3339),
	}
}

var (
	_ moderation.LogStore       = (*ModerationLogStore)(nil)
	_ appriskcontrol.Repository = (*ModerationLogStore)(nil)
)
