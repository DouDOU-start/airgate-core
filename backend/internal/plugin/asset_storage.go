package plugin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/setting"
)

// AssetPurpose 是 core 内部定义的资产用途枚举。
// 插件通过 assets.store host RPC 只能传入这几个值之一 —— 插件不能自由控制
// object_key 的命名空间，避免泄漏调用方信息到存储层（破坏边界）。
type AssetPurpose string

const (
	AssetPurposeChat      AssetPurpose = "chat"       // 对话产物（AI 输出的图/音频）
	AssetPurposeUpload    AssetPurpose = "upload"     // 用户/管理员主动上传（头像、logo、附件）
	AssetPurposeGenerated AssetPurpose = "generated"  // task 系统的独立生成产物（生图、生视频任务）
	AssetPurposeTaskInput AssetPurpose = "task-input" // task 输入里被剥离的大 base64
	AssetPurposeTemp      AssetPurpose = "temp"       // 临时草稿/未保存内容（适合配短保留期）
)

func parseAssetPurpose(raw string) (AssetPurpose, bool) {
	switch AssetPurpose(raw) {
	case AssetPurposeChat, AssetPurposeUpload, AssetPurposeGenerated, AssetPurposeTaskInput, AssetPurposeTemp:
		return AssetPurpose(raw), true
	}
	return "", false
}

const (
	maxAssetDownloadSize = 50 << 20
	assetDownloadTimeout = 60 * time.Second
)

const DefaultAssetStorageDir = "data/assets"

type AssetStorage struct {
	client        *minio.Client
	bucket        string
	prefix        string
	publicBaseURL string
	presignTTL    time.Duration
	localDir      string
	useS3         bool
}

type StoredAsset struct {
	ID          string
	ObjectKey   string
	PublicURL   string
	ContentType string
	SizeBytes   int64
}

func NewAssetStorage(ctx context.Context, db *ent.Client) (*AssetStorage, error) {
	items, err := db.Setting.Query().Where(setting.GroupEQ("storage")).All(ctx)
	if err != nil {
		return nil, err
	}
	cfg := make(map[string]string, len(items))
	for _, item := range items {
		cfg[item.Key] = item.Value
	}

	storage := &AssetStorage{
		prefix:        cleanAssetPrefix(cfg["s3_path_prefix"]),
		publicBaseURL: strings.TrimRight(strings.TrimSpace(cfg["s3_public_base_url"]), "/"),
		localDir:      strings.TrimSpace(cfg["local_storage_dir"]),
	}
	if storage.localDir == "" {
		storage.localDir = strings.TrimSpace(os.Getenv("ASSETS_DIR"))
	}
	if storage.localDir == "" {
		storage.localDir = DefaultAssetStorageDir
	}

	ttl := parseInt(cfg["s3_presign_ttl_minutes"])
	if ttl <= 0 {
		ttl = 360
	}
	storage.presignTTL = time.Duration(ttl) * time.Minute

	endpoint := strings.TrimSpace(cfg["s3_endpoint"])
	bucket := strings.TrimSpace(cfg["s3_bucket"])
	accessKey := strings.TrimSpace(cfg["s3_access_key"])
	secretKey := strings.TrimSpace(cfg["s3_secret_key"])
	if endpoint == "" || bucket == "" || accessKey == "" || secretKey == "" {
		return storage, nil
	}

	useSSL := parseBool(cfg["s3_use_ssl"])
	endpoint, endpointUseSSL := normalizeAssetEndpoint(endpoint)
	if endpointUseSSL != nil {
		useSSL = *endpointUseSSL
	}
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
		Region: strings.TrimSpace(cfg["s3_region"]),
	})
	if err != nil {
		return nil, err
	}
	storage.client = client
	storage.bucket = bucket
	storage.useS3 = true
	return storage, nil
}

// store 把字节落到存储层（S3 或本地），按 <prefix>/<purpose>/<uid>/<yyyymm>/<id>.<ext>
// 的稳定规则生成 object_key。purpose 必须是 core 定义的枚举之一，调用方传非法值
// 应在 host RPC 边界就被拦下，这里二次校验仅作为内防御。
func (s *AssetStorage) Store(ctx context.Context, userID int64, purpose AssetPurpose, contentType, ext string, data []byte) (*StoredAsset, error) {
	if _, ok := parseAssetPurpose(string(purpose)); !ok {
		return nil, fmt.Errorf("invalid asset purpose: %q", purpose)
	}
	id, err := newAssetID()
	if err != nil {
		return nil, err
	}
	yyyymm := time.Now().UTC().Format("200601")
	objectKey := path.Join(s.prefix, string(purpose), strconv.FormatInt(userID, 10), yyyymm, id+cleanAssetExtension(ext))
	if s.useS3 {
		_, err = s.client.PutObject(ctx, s.bucket, objectKey, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{
			ContentType:  contentType,
			CacheControl: "private, max-age=31536000, immutable",
		})
		if err != nil {
			return nil, err
		}
	} else {
		localPath, err := s.localPath(objectKey)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(localPath, data, 0o644); err != nil {
			return nil, err
		}
	}
	publicURL, err := s.PublicURL(ctx, objectKey)
	if err != nil {
		return nil, err
	}
	return &StoredAsset{ID: id, ObjectKey: objectKey, PublicURL: publicURL, ContentType: contentType, SizeBytes: int64(len(data))}, nil
}

func (s *AssetStorage) PublicURL(ctx context.Context, objectKey string) (string, error) {
	if !s.useS3 {
		return "/assets-runtime/" + escapeAssetKey(objectKey), nil
	}
	if s.publicBaseURL != "" {
		return strings.TrimRight(s.publicBaseURL, "/") + "/" + strings.TrimLeft(objectKey, "/"), nil
	}
	u, err := s.client.PresignedGetObject(ctx, s.bucket, objectKey, s.presignTTL, nil)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func (s *AssetStorage) GetBytes(ctx context.Context, objectKey string) ([]byte, string, error) {
	if !s.useS3 {
		localPath, err := s.localPath(objectKey)
		if err != nil {
			return nil, "", err
		}
		data, err := os.ReadFile(localPath)
		if err != nil {
			return nil, "", err
		}
		return data, contentTypeForAssetKey(objectKey), nil
	}
	obj, err := s.client.GetObject(ctx, s.bucket, objectKey, minio.GetObjectOptions{})
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = obj.Close() }()
	info, err := obj.Stat()
	if err != nil {
		return nil, "", err
	}
	data, err := io.ReadAll(obj)
	if err != nil {
		return nil, "", err
	}
	return data, info.ContentType, nil
}

// Delete 删除一个资产对象；本地模式会顺带清掉同路径下的缩略图缓存。
func (s *AssetStorage) Delete(ctx context.Context, objectKey string) error {
	if s == nil {
		return nil
	}
	if s.useS3 {
		return s.client.RemoveObject(ctx, s.bucket, objectKey, minio.RemoveObjectOptions{})
	}

	localPath, err := s.localPath(objectKey)
	if err != nil {
		return err
	}
	if err := os.Remove(localPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return removeLocalThumbVariants(localPath)
}

func (s *AssetStorage) localPath(objectKey string) (string, error) {
	clean := strings.TrimPrefix(path.Clean("/"+objectKey), "/")
	if clean == "" || clean == "." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
		return "", fmt.Errorf("invalid object key")
	}
	return filepath.Join(s.localDir, filepath.FromSlash(clean)), nil
}

func newAssetID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func normalizeAssetEndpoint(endpoint string) (string, *bool) {
	if parsed, err := url.Parse(endpoint); err == nil && parsed.Host != "" {
		useSSL := parsed.Scheme == "https"
		return parsed.Host, &useSSL
	}
	return strings.TrimRight(strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://"), "/"), nil
}

func cleanAssetPrefix(prefix string) string {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if prefix == "." {
		return ""
	}
	return prefix
}

func cleanAssetExtension(ext string) string {
	ext = strings.ToLower(strings.TrimSpace(ext))
	if ext == "" {
		return ".bin"
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	for _, r := range ext[1:] {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return ".bin"
		}
	}
	return ext
}

func (s *AssetStorage) StoreFromURL(ctx context.Context, userID int64, purpose AssetPurpose, sourceURL string) (*StoredAsset, error) {
	parsed, err := url.Parse(sourceURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("invalid source URL: must be http or https")
	}

	dlCtx, cancel := context.WithTimeout(ctx, assetDownloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(dlCtx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build download request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download asset: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download asset: HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAssetDownloadSize+1))
	if err != nil {
		return nil, fmt.Errorf("read asset body: %w", err)
	}
	if int64(len(data)) > maxAssetDownloadSize {
		return nil, fmt.Errorf("asset exceeds %d bytes size limit", maxAssetDownloadSize)
	}

	ct := resp.Header.Get("Content-Type")
	if ct == "" || !strings.Contains(ct, "/") {
		ct = "application/octet-stream"
	}
	if i := strings.Index(ct, ";"); i > 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	ext := extensionForContentType(ct)

	return s.Store(ctx, userID, purpose, ct, ext, data)
}

func extensionForContentType(ct string) string {
	switch strings.ToLower(ct) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "image/svg+xml":
		return ".svg"
	case "video/mp4":
		return ".mp4"
	case "audio/mpeg":
		return ".mp3"
	default:
		return ".bin"
	}
}

func contentTypeForAssetKey(objectKey string) string {
	switch strings.ToLower(path.Ext(objectKey)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	default:
		return "application/octet-stream"
	}
}

func escapeAssetKey(objectKey string) string {
	parts := strings.Split(strings.TrimLeft(objectKey, "/"), "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func parseInt(raw string) int {
	var out int
	_, _ = fmt.Sscanf(strings.TrimSpace(raw), "%d", &out)
	return out
}

func parseBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "t", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func removeLocalThumbVariants(localPath string) error {
	patterns := []string{
		localPath + ".w*.jpg",
		localPath + ".w*.jpg.tmp",
	}
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return err
		}
		for _, match := range matches {
			_ = os.Remove(match)
		}
	}
	return nil
}

func isLocalThumbVariantPath(localPath string) bool {
	_, ok := sourcePathFromThumbPath(localPath)
	return ok
}

func sourcePathFromThumbPath(localPath string) (string, bool) {
	base := filepath.Base(localPath)
	for _, suffix := range []string{".jpg.tmp", ".jpg"} {
		if !strings.HasSuffix(base, suffix) {
			continue
		}
		prefix := strings.TrimSuffix(base, suffix)
		lastDot := strings.LastIndex(prefix, ".w")
		if lastDot < 0 || lastDot == len(prefix)-2 {
			continue
		}
		width := prefix[lastDot+2:]
		validWidth := true
		for _, r := range width {
			if r < '0' || r > '9' {
				validWidth = false
				break
			}
		}
		if validWidth {
			dir := filepath.Dir(localPath)
			return filepath.Join(dir, prefix[:lastDot]), true
		}
	}
	return "", false
}

func (s *AssetStorage) localObjectKey(localPath string) (string, error) {
	base, err := filepath.Abs(s.localDir)
	if err != nil {
		return "", err
	}
	full, err := filepath.Abs(localPath)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(base, full)
	if err != nil {
		return "", err
	}
	if rel == "." || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("invalid local asset path")
	}
	return filepath.ToSlash(rel), nil
}

func (s *AssetStorage) cleanupLocalPurpose(ctx context.Context, purpose AssetPurpose, retention time.Duration) (int, error) {
	root := filepath.Join(s.localDir, filepath.FromSlash(path.Join(s.prefix, string(purpose))))
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	if !info.IsDir() {
		return 0, nil
	}

	now := time.Now()
	deleted := 0
	err = filepath.WalkDir(root, func(localPath string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			slog.Warn("asset_cleanup_walk_failed", "path", localPath, "error", walkErr)
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if isLocalThumbVariantPath(localPath) {
			sourcePath, ok := sourcePathFromThumbPath(localPath)
			if !ok {
				return nil
			}
			if _, statErr := os.Stat(sourcePath); statErr == nil {
				return nil
			} else if !os.IsNotExist(statErr) {
				slog.Warn("asset_cleanup_thumb_source_stat_failed", "path", sourcePath, "error", statErr)
				return nil
			}
			if removeErr := os.Remove(localPath); removeErr == nil {
				deleted++
			} else if !os.IsNotExist(removeErr) {
				slog.Warn("asset_cleanup_thumb_remove_failed", "path", localPath, "error", removeErr)
			}
			return nil
		}

		meta, infoErr := entry.Info()
		if infoErr != nil {
			slog.Warn("asset_cleanup_info_failed", "path", localPath, "error", infoErr)
			return nil
		}
		if now.Sub(meta.ModTime()) < retention {
			return nil
		}
		objectKey, keyErr := s.localObjectKey(localPath)
		if keyErr != nil {
			slog.Warn("asset_cleanup_key_failed", "path", localPath, "error", keyErr)
			return nil
		}
		if err := s.Delete(ctx, objectKey); err != nil {
			slog.Warn("asset_cleanup_delete_failed", "object_key", objectKey, "error", err)
			return nil
		}
		deleted++
		return nil
	})
	if err != nil {
		return deleted, err
	}
	return deleted, nil
}

func (s *AssetStorage) cleanupS3Purpose(ctx context.Context, purpose AssetPurpose, retention time.Duration) (int, error) {
	prefix := path.Join(s.prefix, string(purpose))
	opts := minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	}
	deleted := 0
	now := time.Now()
	for obj := range s.client.ListObjects(ctx, s.bucket, opts) {
		if obj.Err != nil {
			slog.Warn("asset_cleanup_list_failed", "prefix", prefix, "error", obj.Err)
			continue
		}
		if obj.LastModified.IsZero() || now.Sub(obj.LastModified) < retention {
			continue
		}
		if err := s.client.RemoveObject(ctx, s.bucket, obj.Key, minio.RemoveObjectOptions{}); err != nil {
			slog.Warn("asset_cleanup_remove_failed", "object_key", obj.Key, "error", err)
			continue
		}
		deleted++
	}
	return deleted, nil
}

// CleanupExpired 按用途保留期清理过期对象。本地模式删除文件和缩略图缓存，S3 模式删除对象。
func (s *AssetStorage) CleanupExpired(ctx context.Context, policy AssetRetentionPolicy) (int, error) {
	if s == nil || len(policy) == 0 {
		return 0, nil
	}
	total := 0
	for purpose, retention := range policy {
		if retention <= 0 {
			continue
		}
		var (
			deleted int
			err     error
		)
		if s.useS3 {
			deleted, err = s.cleanupS3Purpose(ctx, purpose, retention)
		} else {
			deleted, err = s.cleanupLocalPurpose(ctx, purpose, retention)
		}
		total += deleted
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
