package plugin

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/setting"
)

const (
	assetCleanupInterval   = time.Hour
	assetCleanupRunTimeout = 10 * time.Minute

	settingAssetRetentionChatDays      = "asset_retention_chat_days"
	settingAssetRetentionUploadDays    = "asset_retention_upload_days"
	settingAssetRetentionGeneratedDays = "asset_retention_generated_days"
	settingAssetRetentionTaskInputDays = "asset_retention_task_input_days"
	settingAssetRetentionTempDays      = "asset_retention_temp_days"
)

const (
	defaultAssetRetentionGeneratedDays = 7
	defaultAssetRetentionTaskInputDays = 30
	defaultAssetRetentionTempDays      = 7
)

// AssetRetentionPolicy 表示每类资产的自动清理保留期；0 表示永久保留。
type AssetRetentionPolicy map[AssetPurpose]time.Duration

var assetRetentionSettings = []struct {
	purpose     AssetPurpose
	key         string
	defaultDays int
}{
	{AssetPurposeChat, settingAssetRetentionChatDays, 0},
	{AssetPurposeUpload, settingAssetRetentionUploadDays, 0},
	{AssetPurposeGenerated, settingAssetRetentionGeneratedDays, defaultAssetRetentionGeneratedDays},
	{AssetPurposeTaskInput, settingAssetRetentionTaskInputDays, defaultAssetRetentionTaskInputDays},
	{AssetPurposeTemp, settingAssetRetentionTempDays, defaultAssetRetentionTempDays},
}

func loadAssetRetentionPolicy(ctx context.Context, db *ent.Client) (AssetRetentionPolicy, error) {
	items, err := db.Setting.Query().Where(setting.GroupEQ("storage")).All(ctx)
	if err != nil {
		return nil, err
	}
	cfg := make(map[string]string, len(items))
	for _, item := range items {
		cfg[item.Key] = item.Value
	}

	policy := AssetRetentionPolicy{}
	for _, field := range assetRetentionSettings {
		days := field.defaultDays
		if raw, ok := cfg[field.key]; ok {
			raw = strings.TrimSpace(raw)
			if raw != "" {
				days = parseInt(raw)
			}
		}
		if days <= 0 {
			continue
		}
		policy[field.purpose] = time.Duration(days) * 24 * time.Hour
	}
	return policy, nil
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
	storage, err := NewAssetStorage(ctx, db)
	if err != nil {
		slog.Warn("asset_cleanup_storage_init_failed", "error", err)
		return
	}
	deleted, err := storage.CleanupExpired(ctx, policy)
	if err != nil {
		slog.Warn("asset_cleanup_failed", "deleted", deleted, "error", err)
		return
	}
	if deleted > 0 {
		slog.Info("asset_cleanup_completed", "deleted", deleted)
	}
}
