package plugin

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
)

const (
	// taskInputAssetThreshold 是 data URI 触发落盘的字节阈值。小于这个的占位图、
	// 图标等保留 inline，避免无谓的磁盘 IO 和 setting 表查询。
	taskInputAssetThreshold = 16 << 10

	// taskInputAssetMaxBytes 是单张输入图的硬上限，超出直接拒绝。与
	// storeFromURL 的 maxAssetDownloadSize 对齐，避免被恶意请求打爆磁盘。
	taskInputAssetMaxBytes = 50 << 20
)

// normalizeTaskInputAssets 扫描 task.input，把内嵌的 data:image/* base64 URI 落盘到
// asset storage，原地替换为 /assets-runtime/... 形式的 publicURL。
//
// 设计意图：让 DB 里 task.input 恒小（< 1KB），同时让后续 dispatch RPC 不会因为
// input.images/mask 携带 multi-MB base64 而击穿 64MB 的 gRPC message limit。
//
// 只处理 data:image/* 前缀的字符串值，且字节数 >= taskInputAssetThreshold；
// 其它字符串（prompt、URL、小图标）原样保留。递归遍历 map 与 []any 内部的字符串。
//
// 出错（解码失败、单张超限、storage 写盘失败）直接返回 — 不静默回退，因为静默
// 会把大 data URI 重新写回 DB，等于没修。
//
// pluginID 决定落盘目录：<prefix>/<pluginID>/task-inputs/user-{userID}/<id>.<ext>。
func normalizeTaskInputAssets(ctx context.Context, storage *assetStorage, pluginID string, userID int64, input map[string]any) error {
	if storage == nil || len(input) == 0 || userID <= 0 {
		return nil
	}
	scope := taskInputAssetScope(pluginID)
	return walkAndNormalize(ctx, storage, scope, userID, input)
}

func taskInputAssetScope(pluginID string) string {
	if pluginID == "" {
		return "core/task-inputs"
	}
	return pluginID + "/task-inputs"
}

func walkAndNormalize(ctx context.Context, storage *assetStorage, scope string, userID int64, node any) error {
	switch v := node.(type) {
	case map[string]any:
		for k, child := range v {
			if s, ok := child.(string); ok {
				replaced, err := maybeStoreDataURI(ctx, storage, scope, userID, s)
				if err != nil {
					return fmt.Errorf("input[%s]: %w", k, err)
				}
				if replaced != s {
					v[k] = replaced
				}
				continue
			}
			if err := walkAndNormalize(ctx, storage, scope, userID, child); err != nil {
				return err
			}
		}
	case []any:
		for i, child := range v {
			if s, ok := child.(string); ok {
				replaced, err := maybeStoreDataURI(ctx, storage, scope, userID, s)
				if err != nil {
					return fmt.Errorf("input[%d]: %w", i, err)
				}
				if replaced != s {
					v[i] = replaced
				}
				continue
			}
			if err := walkAndNormalize(ctx, storage, scope, userID, child); err != nil {
				return err
			}
		}
	}
	return nil
}

// maybeStoreDataURI 检测字符串是不是 data:image/* base64 URI 且足够大；如果是，
// 解码后通过 storage.store 落盘，返回新的 publicURL；否则原样返回。
func maybeStoreDataURI(ctx context.Context, storage *assetStorage, scope string, userID int64, ref string) (string, error) {
	if len(ref) < taskInputAssetThreshold {
		return ref, nil
	}
	if !strings.HasPrefix(ref, "data:image/") {
		return ref, nil
	}
	commaIdx := strings.IndexByte(ref, ',')
	if commaIdx < 0 {
		return ref, nil
	}
	header := ref[:commaIdx]
	if !strings.Contains(header, ";base64") {
		// 非 base64 编码的 data URI（百分号编码之类），现状不常见，保留原样。
		return ref, nil
	}
	mime := strings.TrimPrefix(header[:strings.Index(header, ";")], "data:")
	if mime == "" {
		return ref, nil
	}

	encoded := ref[commaIdx+1:]
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(encoded)
		if err != nil {
			return "", fmt.Errorf("decode data URI base64: %w", err)
		}
	}
	if len(data) < taskInputAssetThreshold {
		// base64 膨胀比 ~1.33，解码后还小，不值得落盘。
		return ref, nil
	}
	if len(data) > taskInputAssetMaxBytes {
		return "", fmt.Errorf("input asset exceeds %d bytes limit (got %d)", taskInputAssetMaxBytes, len(data))
	}

	asset, err := storage.store(ctx, userID, scope, mime, extensionForContentType(mime), data)
	if err != nil {
		return "", fmt.Errorf("store input asset: %w", err)
	}
	return asset.PublicURL, nil
}
