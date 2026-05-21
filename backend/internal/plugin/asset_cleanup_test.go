package plugin

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"entgo.io/ent/dialect/sql/schema"
	_ "github.com/mattn/go-sqlite3"

	"github.com/DouDOU-start/airgate-core/ent/enttest"
)

func TestAssetStorageCleanupExpiredLocal(t *testing.T) {
	ctx := context.Background()
	storage := &AssetStorage{
		localDir: t.TempDir(),
		prefix:   "airgate",
		useS3:    false,
	}

	oldGenerated := mustStoreTestAsset(t, storage, ctx, 42, AssetPurposeGenerated)
	newGenerated := mustStoreTestAsset(t, storage, ctx, 42, AssetPurposeGenerated)
	oldUpload := mustStoreTestAsset(t, storage, ctx, 42, AssetPurposeUpload)

	oldTime := time.Now().Add(-8 * 24 * time.Hour)
	mustSetAssetMTime(t, storage, oldGenerated.ObjectKey, oldTime)
	mustSetAssetMTime(t, storage, oldUpload.ObjectKey, oldTime)
	oldGeneratedPath, err := storage.localPath(oldGenerated.ObjectKey)
	if err != nil {
		t.Fatalf("解析本地路径失败: %v", err)
	}
	mustWriteFile(t, oldGeneratedPath+".w256.jpg", []byte("thumb-256"))
	mustWriteFile(t, oldGeneratedPath+".w512.jpg", []byte("thumb-512"))

	deleted, err := storage.CleanupExpired(ctx, AssetRetentionPolicy{
		AssetPurposeGenerated: 7 * 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("清理过期资产失败: %v", err)
	}
	if deleted <= 0 {
		t.Fatalf("删除数量 = %d，期望大于 0", deleted)
	}
	assertAssetMissing(t, storage, oldGenerated.ObjectKey)
	assertPathMissing(t, oldGeneratedPath+".w256.jpg")
	assertPathMissing(t, oldGeneratedPath+".w512.jpg")
	assertAssetExists(t, storage, newGenerated.ObjectKey)
	assertAssetExists(t, storage, oldUpload.ObjectKey)
}

func TestAssetStorageCleanupRemovesOrphanThumbnail(t *testing.T) {
	ctx := context.Background()
	storage := &AssetStorage{
		localDir: t.TempDir(),
		useS3:    false,
	}
	orphan := filepath.Join(storage.localDir, "generated", "42", "202605", "missing.png.w256.jpg")
	mustWriteFile(t, orphan, []byte("orphan"))

	deleted, err := storage.CleanupExpired(ctx, AssetRetentionPolicy{
		AssetPurposeGenerated: 7 * 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("清理孤立缩略图失败: %v", err)
	}
	if deleted <= 0 {
		t.Fatalf("删除数量 = %d，期望大于 0", deleted)
	}
	assertPathMissing(t, orphan)
}

func TestLoadAssetRetentionPolicyDefaultsAndOverrides(t *testing.T) {
	ctx := context.Background()
	db := enttest.Open(t, "sqlite3", "file:asset_retention_policy?mode=memory&cache=shared&_fk=1", enttest.WithMigrateOptions(schema.WithGlobalUniqueID(false)))
	t.Cleanup(func() { _ = db.Close() })

	policy, err := loadAssetRetentionPolicy(ctx, db)
	if err != nil {
		t.Fatalf("读取默认保留策略失败: %v", err)
	}
	if got := policy[AssetPurposeGenerated]; got != 7*24*time.Hour {
		t.Fatalf("generated 默认保留期 = %s，期望 168h", got)
	}
	if got := policy[AssetPurposeTaskInput]; got != 30*24*time.Hour {
		t.Fatalf("task-input 默认保留期 = %s，期望 720h", got)
	}
	if _, ok := policy[AssetPurposeUpload]; ok {
		t.Fatalf("upload 默认不应自动清理")
	}

	db.Setting.Create().SetGroup("storage").SetKey(settingAssetRetentionGeneratedDays).SetValue("3").SaveX(ctx)
	db.Setting.Create().SetGroup("storage").SetKey(settingAssetRetentionTaskInputDays).SetValue("0").SaveX(ctx)
	policy, err = loadAssetRetentionPolicy(ctx, db)
	if err != nil {
		t.Fatalf("读取覆盖保留策略失败: %v", err)
	}
	if got := policy[AssetPurposeGenerated]; got != 3*24*time.Hour {
		t.Fatalf("generated 覆盖保留期 = %s，期望 72h", got)
	}
	if _, ok := policy[AssetPurposeTaskInput]; ok {
		t.Fatalf("task-input 设置为 0 后不应自动清理")
	}
}

func mustStoreTestAsset(t *testing.T, storage *AssetStorage, ctx context.Context, userID int64, purpose AssetPurpose) *StoredAsset {
	t.Helper()
	asset, err := storage.Store(ctx, userID, purpose, "image/png", ".png", []byte("data"))
	if err != nil {
		t.Fatalf("写入测试资产失败: %v", err)
	}
	return asset
}

func mustSetAssetMTime(t *testing.T, storage *AssetStorage, objectKey string, at time.Time) {
	t.Helper()
	localPath, err := storage.localPath(objectKey)
	if err != nil {
		t.Fatalf("解析资产路径失败: %v", err)
	}
	if err := os.Chtimes(localPath, at, at); err != nil {
		t.Fatalf("设置资产时间失败: %v", err)
	}
}

func mustWriteFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("写入文件失败: %v", err)
	}
}

func assertAssetExists(t *testing.T, storage *AssetStorage, objectKey string) {
	t.Helper()
	localPath, err := storage.localPath(objectKey)
	if err != nil {
		t.Fatalf("解析资产路径失败: %v", err)
	}
	if _, err := os.Stat(localPath); err != nil {
		t.Fatalf("资产应存在: %s, err=%v", objectKey, err)
	}
}

func assertAssetMissing(t *testing.T, storage *AssetStorage, objectKey string) {
	t.Helper()
	localPath, err := storage.localPath(objectKey)
	if err != nil {
		t.Fatalf("解析资产路径失败: %v", err)
	}
	assertPathMissing(t, localPath)
}

func assertPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("路径应不存在: %s, err=%v", path, err)
	}
}
