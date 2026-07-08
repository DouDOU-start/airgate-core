package asset

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/setting"
	enttask "github.com/DouDOU-start/airgate-core/ent/task"
)

const (
	assetCleanupInterval   = time.Hour
	assetCleanupRunTimeout = 10 * time.Minute
	taskCleanupBatchSize   = 200

	generatedTaskExecutorPluginID = "gateway-openai"

	settingAssetRetentionGeneratedDays = "asset_retention_generated_days"
)

const (
	defaultAssetRetentionGeneratedDays = 7
)

var generatedTaskTypes = []string{"image.generate", "image.edit"}

var terminalTaskStatuses = []enttask.Status{
	enttask.StatusCompleted,
	enttask.StatusFailed,
	enttask.StatusCancelled,
}

// AssetRetentionPolicy 表示每类资产的自动清理保留期；0 表示永久保留。
type AssetRetentionPolicy map[AssetPurpose]time.Duration

func loadAssetRetentionPolicy(ctx context.Context, db *ent.Client) (AssetRetentionPolicy, error) {
	items, err := db.Setting.Query().Where(setting.GroupEQ("storage")).All(ctx)
	if err != nil {
		return nil, err
	}
	cfg := make(map[string]string, len(items))
	for _, item := range items {
		cfg[item.Key] = item.Value
	}

	days := defaultAssetRetentionGeneratedDays
	if raw, ok := cfg[settingAssetRetentionGeneratedDays]; ok {
		raw = strings.TrimSpace(raw)
		if raw != "" {
			days = parseInt(raw)
		}
	}
	if days <= 0 {
		return AssetRetentionPolicy{}, nil
	}
	retention := time.Duration(days) * 24 * time.Hour
	return AssetRetentionPolicy{
		AssetPurposeGenerated: retention,
		AssetPurposeTaskInput: retention,
	}, nil
}

// StartAssetCleanupLoop 启动 Core 侧资产清理循环。
//
// 清理策略每轮从 settings.storage 重新读取，因此管理员改保留天数后无需重启。
func StartAssetCleanupLoop(ctx context.Context, db *ent.Client) {
	if db == nil {
		return
	}
	runAssetCleanupOnce(ctx, db)

	ticker := time.NewTicker(assetCleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runAssetCleanupOnce(ctx, db)
		}
	}
}

func runAssetCleanupOnce(parent context.Context, db *ent.Client) {
	if err := parent.Err(); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, assetCleanupRunTimeout)
	defer cancel()

	policy, err := loadAssetRetentionPolicy(ctx, db)
	if err != nil {
		slog.Warn("asset_cleanup_policy_load_failed", "error", err)
		return
	}
	if len(policy) == 0 {
		return
	}
	generatedRetention := policy[AssetPurposeGenerated]
	storage, err := NewAssetStorage(ctx, db)
	if err != nil {
		slog.Warn("asset_cleanup_storage_init_failed", "error", err)
		return
	}
	deleted, err := storage.CleanupExpired(ctx, policy)
	if err != nil {
		slog.Warn("asset_cleanup_failed", "deleted", deleted, "error", err)
	}
	if deleted > 0 {
		slog.Info("asset_cleanup_completed", "deleted", deleted)
	}

	if generatedRetention <= 0 {
		return
	}
	taskDeleted, err := cleanupExpiredGeneratedTasks(ctx, db, storage, generatedRetention)
	if err != nil {
		slog.Warn("task_cleanup_failed", "deleted", taskDeleted, "error", err)
		return
	}
	if taskDeleted > 0 {
		slog.Info("task_cleanup_completed", "deleted", taskDeleted)
	}
}

func cleanupExpiredGeneratedTasks(ctx context.Context, db *ent.Client, storage *AssetStorage, retention time.Duration) (int, error) {
	if db == nil || storage == nil || retention <= 0 {
		return 0, nil
	}
	cutoff := time.Now().Add(-retention)
	total := 0

	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		tasks, err := db.Task.Query().
			Where(
				enttask.PluginIDEQ(generatedTaskExecutorPluginID),
				enttask.TaskTypeIn(generatedTaskTypes...),
				enttask.StatusIn(terminalTaskStatuses...),
				enttask.CompletedAtLTE(cutoff),
			).
			Limit(taskCleanupBatchSize).
			All(ctx)
		if err != nil {
			return total, err
		}
		if len(tasks) == 0 {
			break
		}
		total += deleteExpiredGeneratedTaskBatch(ctx, db, storage, tasks)
	}

	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		tasks, err := db.Task.Query().
			Where(
				enttask.PluginIDEQ(generatedTaskExecutorPluginID),
				enttask.TaskTypeIn(generatedTaskTypes...),
				enttask.StatusIn(terminalTaskStatuses...),
				enttask.CompletedAtIsNil(),
				enttask.CreatedAtLTE(cutoff),
			).
			Limit(taskCleanupBatchSize).
			All(ctx)
		if err != nil {
			return total, err
		}
		if len(tasks) == 0 {
			break
		}
		total += deleteExpiredGeneratedTaskBatch(ctx, db, storage, tasks)
	}

	return total, nil
}

func deleteExpiredGeneratedTaskBatch(ctx context.Context, db *ent.Client, storage *AssetStorage, tasks []*ent.Task) int {
	deleted := 0
	for _, t := range tasks {
		if err := deleteExpiredGeneratedTask(ctx, db, storage, t); err != nil {
			slog.Warn("task_cleanup_delete_failed", "task_id", t.ID, "error", err)
			continue
		}
		deleted++
	}
	return deleted
}

func deleteExpiredGeneratedTask(ctx context.Context, db *ent.Client, storage *AssetStorage, t *ent.Task) error {
	if t == nil {
		return nil
	}
	for _, objectKey := range collectTaskAssetObjectKeys(t) {
		if objectKey == "" {
			continue
		}
		if err := storage.Delete(ctx, objectKey); err != nil {
			return fmt.Errorf("delete task asset %s: %w", objectKey, err)
		}
	}
	if err := db.Task.DeleteOneID(t.ID).Exec(ctx); err != nil && !ent.IsNotFound(err) {
		return fmt.Errorf("delete task %d: %w", t.ID, err)
	}
	return nil
}
