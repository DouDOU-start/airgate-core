package plugin

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
)

// newTestAssetStorage 在临时目录起一个本地（非 S3）asset storage 用于单测。
func newTestAssetStorage(t *testing.T) *assetStorage {
	t.Helper()
	return &assetStorage{
		localDir: t.TempDir(),
		useS3:    false,
	}
}

func bigDataURI(t *testing.T, mime string, size int) string {
	t.Helper()
	data := make([]byte, size)
	// 字节不全 0 — 触发不到任何短路逻辑（虽然现在也没有，防御性）
	for i := range data {
		data[i] = byte(i % 251)
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func TestNormalizeTaskInputAssets_ReplacesLargeDataURI(t *testing.T) {
	ctx := context.Background()
	storage := newTestAssetStorage(t)
	bigPNG := bigDataURI(t, "image/png", 32<<10)
	input := map[string]any{
		"prompt": "make it blue",
		"images": []any{bigPNG, bigPNG},
		"mask":   bigPNG,
	}

	if err := normalizeTaskInputAssets(ctx, storage, "gateway-openai", 42, input); err != nil {
		t.Fatalf("normalize: %v", err)
	}

	if input["prompt"] != "make it blue" {
		t.Fatalf("prompt mutated: %+v", input["prompt"])
	}
	images, ok := input["images"].([]any)
	if !ok || len(images) != 2 {
		t.Fatalf("images shape changed: %+v", input["images"])
	}
	for i, img := range images {
		s, ok := img.(string)
		if !ok {
			t.Fatalf("images[%d] type = %T", i, img)
		}
		if !strings.HasPrefix(s, "/assets-runtime/") {
			t.Fatalf("images[%d] not replaced: %s", i, s[:40])
		}
		if !strings.Contains(s, "gateway-openai/task-inputs/user-42/") {
			t.Fatalf("images[%d] wrong scope: %s", i, s)
		}
	}
	mask, ok := input["mask"].(string)
	if !ok || !strings.HasPrefix(mask, "/assets-runtime/") {
		t.Fatalf("mask not replaced: %+v", input["mask"])
	}
}

func TestNormalizeTaskInputAssets_LeavesSmallDataURI(t *testing.T) {
	ctx := context.Background()
	storage := newTestAssetStorage(t)
	smallPNG := bigDataURI(t, "image/png", 4<<10) // 4 KB < 16 KB 阈值
	input := map[string]any{
		"images": []any{smallPNG},
	}
	if err := normalizeTaskInputAssets(ctx, storage, "gateway-openai", 1, input); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	images := input["images"].([]any)
	if got := images[0].(string); got != smallPNG {
		t.Fatalf("small data URI was replaced; should stay inline")
	}
}

func TestNormalizeTaskInputAssets_LeavesNonImageDataURI(t *testing.T) {
	ctx := context.Background()
	storage := newTestAssetStorage(t)
	// 大字体 data URI — 类型不是 image/* 不该动
	body := strings.Repeat("A", 32<<10)
	fontDataURI := "data:font/woff2;base64," + base64.StdEncoding.EncodeToString([]byte(body))
	input := map[string]any{
		"font": fontDataURI,
	}
	if err := normalizeTaskInputAssets(ctx, storage, "any", 1, input); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if input["font"].(string) != fontDataURI {
		t.Fatalf("non-image data URI was touched")
	}
}

func TestNormalizeTaskInputAssets_LeavesHTTPURL(t *testing.T) {
	ctx := context.Background()
	storage := newTestAssetStorage(t)
	url := "https://cdn.example.com/" + strings.Repeat("a", 20<<10) + ".png"
	input := map[string]any{"images": []any{url}}
	if err := normalizeTaskInputAssets(ctx, storage, "any", 1, input); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if input["images"].([]any)[0].(string) != url {
		t.Fatalf("http URL was touched")
	}
}

func TestNormalizeTaskInputAssets_IdempotentOnPublicURL(t *testing.T) {
	ctx := context.Background()
	storage := newTestAssetStorage(t)
	already := "/assets-runtime/gateway-openai/task-inputs/user-1/abcdef.png"
	input := map[string]any{"images": []any{already}}
	if err := normalizeTaskInputAssets(ctx, storage, "gateway-openai", 1, input); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if got := input["images"].([]any)[0].(string); got != already {
		t.Fatalf("public URL got rewritten: %s", got)
	}
}

func TestNormalizeTaskInputAssets_RejectsOversizedInput(t *testing.T) {
	ctx := context.Background()
	storage := newTestAssetStorage(t)
	// 51 MB > 50 MB 上限
	huge := bigDataURI(t, "image/png", 51<<20)
	input := map[string]any{"images": []any{huge}}
	err := normalizeTaskInputAssets(ctx, storage, "any", 1, input)
	if err == nil {
		t.Fatalf("expected error for oversized input")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error should mention size limit: %v", err)
	}
}

func TestNormalizeTaskInputAssets_NestedMap(t *testing.T) {
	ctx := context.Background()
	storage := newTestAssetStorage(t)
	bigPNG := bigDataURI(t, "image/jpeg", 32<<10)
	input := map[string]any{
		"reference": map[string]any{
			"primary": bigPNG,
		},
	}
	if err := normalizeTaskInputAssets(ctx, storage, "any", 1, input); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	nested := input["reference"].(map[string]any)
	if got := nested["primary"].(string); !strings.HasPrefix(got, "/assets-runtime/") {
		t.Fatalf("nested data URI not replaced: %s", got[:40])
	}
}

func TestNormalizeTaskInputAssets_NoopOnEmpty(t *testing.T) {
	ctx := context.Background()
	if err := normalizeTaskInputAssets(ctx, nil, "any", 1, nil); err != nil {
		t.Fatalf("nil storage + nil input should noop, got: %v", err)
	}
	if err := normalizeTaskInputAssets(ctx, newTestAssetStorage(t), "any", 0, map[string]any{"a": "b"}); err != nil {
		t.Fatalf("userID=0 should noop, got: %v", err)
	}
}
