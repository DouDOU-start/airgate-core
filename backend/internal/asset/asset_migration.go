package asset

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DouDOU-start/airgate-core/ent"
)

const (
	assetMigrationInterval   = time.Hour
	assetMigrationRunTimeout = 30 * time.Minute
)

type AssetMigrationResult struct {
	Scanned  int
	Migrated int
	Skipped  int
	Failed   int
}

// StartAssetMigrationLoop 持续把本地 data/assets 中还没有进入 S3/R2 的对象补传到远端。
//
// 这个循环只在 S3/R2 配置完整时工作；它不会删除本地文件，避免迁移期丢失回退能力。
func StartAssetMigrationLoop(ctx context.Context, db *ent.Client) {
	if db == nil {
		return
	}
	runAssetMigrationOnce(ctx, db)

	ticker := time.NewTicker(assetMigrationInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runAssetMigrationOnce(ctx, db)
		}
	}
}

func runAssetMigrationOnce(parent context.Context, db *ent.Client) {
	if err := parent.Err(); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, assetMigrationRunTimeout)
	defer cancel()

	storage, err := NewAssetStorage(ctx, db)
	if err != nil {
		slog.Warn("asset_migration_storage_init_failed", "error", err)
		return
	}
	if !storage.useS3 {
		return
	}
	result, err := storage.SyncLocalToS3(ctx)
	if err != nil {
		slog.Warn("asset_migration_failed",
			"scanned", result.Scanned,
			"migrated", result.Migrated,
			"skipped", result.Skipped,
			"failed", result.Failed,
			"error", err)
		return
	}
	if result.Migrated > 0 || result.Failed > 0 {
		slog.Info("asset_migration_completed",
			"scanned", result.Scanned,
			"migrated", result.Migrated,
			"skipped", result.Skipped,
			"failed", result.Failed)
	}
}

func (s *AssetStorage) SyncLocalToS3(ctx context.Context) (AssetMigrationResult, error) {
	var result AssetMigrationResult
	if s == nil || !s.useS3 {
		return result, nil
	}
	info, err := os.Stat(s.localDir)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return result, err
	}
	if !info.IsDir() {
		return result, nil
	}

	err = filepath.WalkDir(s.localDir, func(localPath string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			result.Failed++
			slog.Warn("asset_migration_walk_failed", "path", localPath, "error", walkErr)
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		result.Scanned++
		if shouldSkipAssetMigrationPath(localPath) {
			result.Skipped++
			return nil
		}
		objectKey, err := s.localObjectKey(localPath)
		if err != nil {
			result.Failed++
			slog.Warn("asset_migration_key_failed", "path", localPath, "error", err)
			return nil
		}
		exists, err := s.objectExistsOnS3(ctx, objectKey)
		if err != nil {
			result.Failed++
			slog.Warn("asset_migration_stat_failed", "object_key", objectKey, "error", err)
			return nil
		}
		if exists {
			result.Skipped++
			return nil
		}
		if err := s.putS3File(ctx, objectKey, contentTypeForAssetKey(objectKey), localPath); err != nil {
			result.Failed++
			slog.Warn("asset_migration_upload_failed", "object_key", objectKey, "error", err)
			return nil
		}
		result.Migrated++
		return nil
	})
	return result, err
}

func shouldSkipAssetMigrationPath(localPath string) bool {
	if isLocalThumbVariantPath(localPath) {
		return true
	}
	return strings.HasSuffix(localPath, ".tmp")
}
