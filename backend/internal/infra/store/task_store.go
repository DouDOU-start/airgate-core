package store

import (
	"context"

	"github.com/DouDOU-start/airgate-core/ent"
	enttask "github.com/DouDOU-start/airgate-core/ent/task"
	relaytask "github.com/DouDOU-start/airgate-core/internal/relay/task"
)

// TaskStore 异步任务持久化，实现 relay/task.Store 接口
// （task 子系统禁止 import ent，经本 store 桥接）。
type TaskStore struct {
	db *ent.Client
}

// NewTaskStore 创建任务存储。
func NewTaskStore(db *ent.Client) *TaskStore {
	return &TaskStore{db: db}
}

// unfinishedStatuses 未终态集合（ent 枚举形态）。
var unfinishedStatuses = []enttask.Status{
	enttask.StatusSubmitted,
	enttask.StatusQueued,
	enttask.StatusInProgress,
}

// Insert 落库新任务，返回自增 ID。
func (s *TaskStore) Insert(ctx context.Context, t *relaytask.Task) (int, error) {
	create := s.db.Task.Create().
		SetTaskID(t.TaskID).
		SetPlatform(t.Platform).
		SetAction(t.Action).
		SetStatus(enttask.Status(t.Status)).
		SetProgress(t.Progress).
		SetFailReason(t.FailReason).
		SetRequestModel(t.RequestModel).
		SetUpstreamModel(t.UpstreamModel).
		SetHoldAmount(t.HoldAmount).
		SetEstTotal(t.EstTotal).
		SetRateMultiplier(t.RateMultiplier).
		SetSellRate(t.SellRate).
		SetAccountRateMultiplier(t.AccountRateMultiplier).
		SetSeconds(t.Seconds).
		SetSubmitTime(t.SubmitTime).
		SetRequestID(t.RequestID).
		SetUserID(t.UserID).
		SetUserEmailSnapshot(t.UserEmail).
		SetAPIKeyID(t.APIKeyID).
		SetGroupID(t.GroupID).
		SetChannelID(t.ChannelID).
		SetChannelKeyID(t.ChannelKeyID)
	if len(t.Data) > 0 {
		create.SetData(t.Data)
	}
	item, err := create.Save(ctx)
	if err != nil {
		return 0, err
	}
	return item.ID, nil
}

// GetForUser 按 (platform, task_id, user) 查任务，多条命中取最新；未命中返回 (nil, nil)。
func (s *TaskStore) GetForUser(ctx context.Context, platform, taskID string, userID int) (*relaytask.Task, error) {
	item, err := s.db.Task.Query().
		Where(
			enttask.PlatformEQ(platform),
			enttask.TaskIDEQ(taskID),
			enttask.UserIDEQ(userID),
		).
		Order(ent.Desc(enttask.FieldID)).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return mapTask(item), nil
}

// ListForUser 批量按 task_id 查（未命中的 ID 静默缺席；同 ID 多条时全部返回，按 ID 升序）。
func (s *TaskStore) ListForUser(ctx context.Context, platform string, taskIDs []string, userID int) ([]*relaytask.Task, error) {
	items, err := s.db.Task.Query().
		Where(
			enttask.PlatformEQ(platform),
			enttask.TaskIDIn(taskIDs...),
			enttask.UserIDEQ(userID),
		).
		Order(ent.Asc(enttask.FieldID)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	return mapTasks(items), nil
}

// ListUnfinished 未终态任务按 updated_at 升序（轮询扫描，长任务不饿死）。
func (s *TaskStore) ListUnfinished(ctx context.Context, limit int) ([]*relaytask.Task, error) {
	items, err := s.db.Task.Query().
		Where(enttask.StatusIn(unfinishedStatuses...)).
		Order(ent.Asc(enttask.FieldUpdatedAt)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, err
	}
	return mapTasks(items), nil
}

// CountUnfinished 未终态任务数（轮询空转短路）。
func (s *TaskStore) CountUnfinished(ctx context.Context) (int, error) {
	return s.db.Task.Query().
		Where(enttask.StatusIn(unfinishedStatuses...)).
		Count(ctx)
}

// UpdateStatusCAS 条件更新：仅当行仍处未终态时应用（防轮询/并发覆盖已终态行）。
// 返回是否更新到行。
func (s *TaskStore) UpdateStatusCAS(ctx context.Context, id int, upd relaytask.StatusUpdate) (bool, error) {
	q := s.db.Task.Update().
		Where(
			enttask.IDEQ(id),
			enttask.StatusIn(unfinishedStatuses...),
		).
		SetStatus(enttask.Status(upd.Status)).
		SetProgress(upd.Progress).
		SetFailReason(upd.FailReason)
	if upd.Seconds > 0 {
		q.SetSeconds(upd.Seconds)
	}
	if len(upd.Data) > 0 {
		q.SetData(upd.Data)
	}
	if upd.FinishTime != nil {
		q.SetFinishTime(*upd.FinishTime)
	}
	n, err := q.Save(ctx)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// MarkSettled 结算幂等闸：settled=false → true 的 CAS，返回是否置位成功。
func (s *TaskStore) MarkSettled(ctx context.Context, id int) (bool, error) {
	n, err := s.db.Task.Update().
		Where(
			enttask.IDEQ(id),
			enttask.SettledEQ(false),
		).
		SetSettled(true).
		Save(ctx)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// mapTask ent → 领域对象。
func mapTask(item *ent.Task) *relaytask.Task {
	return &relaytask.Task{
		ID:                    item.ID,
		TaskID:                item.TaskID,
		Platform:              item.Platform,
		Action:                item.Action,
		Status:                string(item.Status),
		Progress:              item.Progress,
		FailReason:            item.FailReason,
		RequestModel:          item.RequestModel,
		UpstreamModel:         item.UpstreamModel,
		HoldAmount:            item.HoldAmount,
		EstTotal:              item.EstTotal,
		RateMultiplier:        item.RateMultiplier,
		SellRate:              item.SellRate,
		AccountRateMultiplier: item.AccountRateMultiplier,
		Settled:               item.Settled,
		Seconds:               item.Seconds,
		Data:                  item.Data,
		SubmitTime:            item.SubmitTime,
		FinishTime:            item.FinishTime,
		RequestID:             item.RequestID,
		UserID:                item.UserID,
		UserEmail:             item.UserEmailSnapshot,
		APIKeyID:              item.APIKeyID,
		GroupID:               item.GroupID,
		ChannelID:             item.ChannelID,
		ChannelKeyID:          item.ChannelKeyID,
		CreatedAt:             item.CreatedAt,
		UpdatedAt:             item.UpdatedAt,
	}
}

func mapTasks(items []*ent.Task) []*relaytask.Task {
	out := make([]*relaytask.Task, len(items))
	for i, item := range items {
		out[i] = mapTask(item)
	}
	return out
}
