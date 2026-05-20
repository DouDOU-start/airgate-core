package plugin

import (
	"context"
	"log/slog"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/DouDOU-start/airgate-core/ent"
	enttask "github.com/DouDOU-start/airgate-core/ent/task"
	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

// task_service.go 收敛 Core 内部任务状态机的持久化入口。
//
// Host.Invoke 只负责 method 分发，具体状态迁移和任务字段处理放在这里。
// 新增图片、视频、音乐等异步任务类型时，只扩展插件处理逻辑和 input/output/execution，
// 不在 Core 增加类型专属字段。

var taskStateTransitions = map[enttask.Status]map[enttask.Status]struct{}{
	enttask.StatusPending: taskTransitionSet(
		enttask.StatusProcessing,
		enttask.StatusFailed,
		enttask.StatusCancelled,
	),
	enttask.StatusProcessing: taskTransitionSet(
		enttask.StatusCompleted,
		enttask.StatusFailed,
		enttask.StatusRetrying,
		enttask.StatusCancelling,
		enttask.StatusCancelled,
	),
	enttask.StatusRetrying: taskTransitionSet(
		enttask.StatusPending,
		enttask.StatusFailed,
	),
	enttask.StatusCancelling: taskTransitionSet(
		enttask.StatusCancelled,
		enttask.StatusFailed,
	),
}

var taskTerminalStatuses = map[enttask.Status]struct{}{
	enttask.StatusCompleted: {},
	enttask.StatusFailed:    {},
	enttask.StatusCancelled: {},
}

func taskTransitionSet(statuses ...enttask.Status) map[enttask.Status]struct{} {
	out := make(map[enttask.Status]struct{}, len(statuses))
	for _, st := range statuses {
		out[st] = struct{}{}
	}
	return out
}

func validateTaskStatus(raw string) (enttask.Status, error) {
	st := enttask.Status(raw)
	if err := enttask.StatusValidator(st); err != nil {
		return "", status.Errorf(codes.InvalidArgument, "invalid status: %s", raw)
	}
	return st, nil
}

func validateTaskTransition(from, to enttask.Status) error {
	if from == to {
		return nil
	}
	if _, ok := taskTerminalStatuses[from]; ok {
		return status.Errorf(codes.FailedPrecondition, "task is terminal: %s", from)
	}
	if allowed, ok := taskStateTransitions[from]; ok {
		if _, ok := allowed[to]; ok {
			return nil
		}
	}
	return status.Errorf(codes.FailedPrecondition, "invalid task transition: %s -> %s", from, to)
}

func taskToPayload(t *ent.Task) map[string]interface{} {
	resp := map[string]interface{}{
		"id":             int64(t.ID),
		"task_id":        int64(t.ID),
		"public_task_id": publicTaskID(t),
		"plugin_id":      t.PluginID,
		"task_type":      t.TaskType,
		"status":         string(t.Status),
		"stage":          t.Stage,
		"user_id":        int64(t.UserID),
		"error_type":     t.ErrorType,
		"error_code":     t.ErrorCode,
		"error_message":  t.ErrorMessage,
		"progress":       t.Progress,
		"attempts":       t.Attempts,
		"max_attempts":   t.MaxAttempts,
		"created_at":     t.CreatedAt.Format(time.RFC3339Nano),
		"updated_at":     t.UpdatedAt.Format(time.RFC3339Nano),
	}
	if t.StartedAt != nil {
		resp["started_at"] = t.StartedAt.Format(time.RFC3339Nano)
	}
	if t.CompletedAt != nil {
		resp["completed_at"] = t.CompletedAt.Format(time.RFC3339Nano)
	}
	if t.Input != nil {
		resp["input"] = t.Input
	}
	if t.Output != nil {
		resp["output"] = t.Output
	}
	if t.Attributes != nil {
		resp["attributes"] = t.Attributes
	}
	if t.Execution != nil {
		resp["execution"] = t.Execution
	}
	if t.UsageID != nil {
		resp["usage_id"] = *t.UsageID
	}
	if t.IdempotencyKey != nil {
		resp["idempotency_key"] = *t.IdempotencyKey
	}
	if t.CancelRequestedAt != nil {
		resp["cancel_requested_at"] = t.CancelRequestedAt.Format(time.RFC3339Nano)
	}
	if t.ExpiresAt != nil {
		resp["expires_at"] = t.ExpiresAt.Format(time.RFC3339Nano)
	}
	return resp
}

// taskToListPayload returns a slim version of taskToPayload suitable for list
// responses. It drops heavy input fields (img2img source images, inpaint masks)
// that may carry multi-MB base64 data URIs and would otherwise blow past the
// 64 MB gRPC message limit once a user accumulates a handful of edit tasks.
// Callers that need the full input must use tasks.get on the specific task.
func taskToListPayload(t *ent.Task) map[string]interface{} {
	payload := taskToPayload(t)
	if input, ok := payload["input"].(map[string]interface{}); ok && len(input) > 0 {
		slim := make(map[string]interface{}, len(input))
		for k, v := range input {
			if k == "images" || k == "mask" {
				continue
			}
			slim[k] = v
		}
		payload["input"] = slim
	}
	return payload
}

func publicTaskID(t *ent.Task) string {
	if t == nil || t.PublicTaskID == nil {
		return ""
	}
	return *t.PublicTaskID
}

func (h *HostService) createTask(ctx context.Context, pluginID string, req hostCreateTaskRequest) (map[string]interface{}, error) {
	taskType := req.TaskType
	if taskType == "" {
		return nil, status.Error(codes.InvalidArgument, "task_type is required")
	}
	if req.PluginID != "" {
		pluginID = req.PluginID
	}
	if req.UserID <= 0 {
		return nil, status.Error(codes.InvalidArgument, "user_id must be > 0")
	}

	maxAttempts := req.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}

	if req.IdempotencyKey != "" {
		existing, err := h.db.Task.Query().
			Where(
				enttask.PluginIDEQ(pluginID),
				enttask.UserIDEQ(int(req.UserID)),
				enttask.TaskTypeEQ(taskType),
				enttask.IdempotencyKeyEQ(req.IdempotencyKey),
			).
			Only(ctx)
		if err == nil {
			return map[string]interface{}{"task": taskToPayload(existing)}, nil
		}
		if !ent.IsNotFound(err) {
			return nil, status.Errorf(codes.Internal, "find idempotent task: %v", err)
		}
	}

	// 把 input 里内嵌的 data:image/* base64 大图落盘换成 /assets-runtime/... URL。
	// 这一步必须在持久化和后续 dispatch 之前完成 —— 任何 base64 大图留在 input 里
	// 都会让 task.list/get/idempotency 查询以及 ProcessTask 派发 RPC 撞 64MB gRPC 上限。
	if len(req.Input) > 0 {
		storage, err := newAssetStorage(ctx, h.db)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "init asset storage: %v", err)
		}
		if err := normalizeTaskInputAssets(ctx, storage, pluginID, req.UserID, req.Input); err != nil {
			return nil, status.Errorf(codes.Internal, "normalize task input assets: %v", err)
		}
	}

	create := h.db.Task.Create().
		SetPluginID(pluginID).
		SetTaskType(taskType).
		SetUserID(int(req.UserID)).
		SetInput(req.Input).
		SetPriority(req.Priority).
		SetMaxAttempts(maxAttempts)
	if req.IdempotencyKey != "" {
		create.SetIdempotencyKey(req.IdempotencyKey)
	}
	if req.PublicTaskID != "" {
		create.SetPublicTaskID(req.PublicTaskID)
	}
	if req.Attributes != nil {
		create.SetAttributes(req.Attributes)
	}
	if req.Execution != nil {
		create.SetExecution(req.Execution)
	}
	t, err := create.Save(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create task: %v", err)
	}

	slog.Info("task_created",
		sdk.LogFieldPluginID, pluginID,
		"task_id", t.ID,
		"task_type", taskType,
		sdk.LogFieldUserID, req.UserID,
	)
	return map[string]interface{}{"task": taskToPayload(t)}, nil
}

func (h *HostService) updateTask(ctx context.Context, pluginID string, req hostUpdateTaskRequest) (map[string]interface{}, error) {
	if req.TaskID <= 0 {
		return nil, status.Error(codes.InvalidArgument, "task_id is required")
	}

	t, err := h.db.Task.Get(ctx, int(req.TaskID))
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, status.Error(codes.NotFound, "task not found")
		}
		return nil, status.Errorf(codes.Internal, "get task: %v", err)
	}

	update := h.db.Task.UpdateOneID(int(req.TaskID))
	if req.Status != "" {
		st, err := validateTaskStatus(req.Status)
		if err != nil {
			return nil, err
		}
		if err := validateTaskTransition(t.Status, st); err != nil {
			return nil, err
		}
		update.SetStatus(st)
	}
	if req.Progress != nil {
		update.SetProgress(*req.Progress)
	}
	if req.Stage != nil {
		update.SetStage(*req.Stage)
	}
	if req.Output != nil {
		update.SetOutput(req.Output)
	}
	if req.Attributes != nil {
		update.SetAttributes(req.Attributes)
	}
	if req.Execution != nil {
		update.SetExecution(req.Execution)
	}
	if req.ErrorType != "" {
		update.SetErrorType(req.ErrorType)
	}
	if req.ErrorCode != "" {
		update.SetErrorCode(req.ErrorCode)
	}
	if req.ErrorMessage != "" {
		update.SetErrorMessage(req.ErrorMessage)
	}
	if req.UsageID != nil {
		update.SetUsageID(*req.UsageID)
	}
	if req.Status == enttask.StatusCompleted.String() || req.Status == enttask.StatusFailed.String() {
		now := time.Now()
		update.SetCompletedAt(now)
	}
	if req.Status == enttask.StatusProcessing.String() && t.StartedAt == nil {
		now := time.Now()
		update.SetStartedAt(now)
	}

	updated, err := update.Save(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "update task: %v", err)
	}
	return map[string]interface{}{"task": taskToPayload(updated)}, nil
}

func (h *HostService) getTask(ctx context.Context, pluginID string, req hostGetTaskRequest) (map[string]interface{}, error) {
	if req.PluginID != "" {
		pluginID = req.PluginID
	}
	query := h.db.Task.Query()
	if req.PublicTaskID != "" {
		query.Where(enttask.PublicTaskIDEQ(req.PublicTaskID))
	} else {
		if req.TaskID <= 0 {
			return nil, status.Error(codes.InvalidArgument, "task_id is required")
		}
		query.Where(enttask.IDEQ(int(req.TaskID)))
	}
	if pluginID != "" {
		query.Where(enttask.PluginIDEQ(pluginID))
	}
	if req.UserID > 0 {
		query.Where(enttask.UserIDEQ(int(req.UserID)))
	}
	t, err := query.Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, status.Error(codes.NotFound, "task not found")
		}
		return nil, status.Errorf(codes.Internal, "get task: %v", err)
	}
	return map[string]interface{}{"task": taskToPayload(t)}, nil
}

func (h *HostService) listTasks(ctx context.Context, pluginID string, req hostListTasksRequest) (map[string]interface{}, error) {
	query := h.db.Task.Query()

	if req.PluginID != "" {
		pluginID = req.PluginID
	}
	if pluginID != "" {
		query.Where(enttask.PluginIDEQ(pluginID))
	}
	if req.UserID > 0 {
		query.Where(enttask.UserIDEQ(int(req.UserID)))
	}
	taskType := req.TaskType
	if taskType != "" {
		query.Where(enttask.TaskTypeEQ(taskType))
	}
	if req.Status != "" {
		st, err := validateTaskStatus(req.Status)
		if err != nil {
			return nil, err
		}
		query.Where(enttask.StatusEQ(st))
	}

	total, err := query.Clone().Count(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "count tasks: %v", err)
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	tasks, err := query.
		Order(ent.Desc(enttask.FieldCreatedAt)).
		Limit(limit).
		Offset(req.Offset).
		All(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list tasks: %v", err)
	}

	items := make([]map[string]interface{}, 0, len(tasks))
	for _, t := range tasks {
		items = append(items, taskToListPayload(t))
	}
	return map[string]interface{}{"tasks": items, "total": total}, nil
}

func (h *HostService) deleteTask(ctx context.Context, pluginID string, req hostDeleteTaskRequest) (map[string]interface{}, error) {
	if req.PluginID != "" {
		pluginID = req.PluginID
	}
	query := h.db.Task.Query().Where(
		enttask.IDEQ(int(req.TaskID)),
		enttask.PluginIDEQ(pluginID),
	)
	if req.UserID > 0 {
		query.Where(enttask.UserIDEQ(int(req.UserID)))
	}
	t, err := query.Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, status.Error(codes.NotFound, "task not found")
		}
		return nil, status.Errorf(codes.Internal, "get task: %v", err)
	}
	if t.Status == enttask.StatusProcessing || t.Status == enttask.StatusPending {
		return nil, status.Error(codes.FailedPrecondition, "cannot delete a running task")
	}
	if err := h.db.Task.DeleteOneID(t.ID).Exec(ctx); err != nil {
		return nil, status.Errorf(codes.Internal, "delete task: %v", err)
	}
	return map[string]interface{}{"deleted": true}, nil
}
