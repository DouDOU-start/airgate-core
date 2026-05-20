package server

import (
	"bytes"
	"errors"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/image/draw"

	// Side-effect imports register WebP decoder so we can thumbnail webp inputs too.
	_ "golang.org/x/image/webp"
)

// allowedThumbWidths is a closed set to prevent cache-bloat attacks from
// arbitrary ?w= values. The set matches what the studio frontend actually
// requests via srcset.
var allowedThumbWidths = map[int]bool{256: true, 512: true}

// thumbnailableExt returns true when the file is an image format we know how to
// decode. Unknown formats fall through to the unmodified file served by the
// caller — keeping ?w= a non-destructive hint.
func thumbnailableExt(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	default:
		return false
	}
}

// resolveThumbWidth parses the ?w= query value; returns 0 if absent or not in
// the allowlist.
func resolveThumbWidth(raw string) int {
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	if !allowedThumbWidths[n] {
		return 0
	}
	return n
}

// thumbCachePath derives the on-disk path for a cached JPEG thumbnail of the
// given source file at the requested width. Co-locating the cache next to the
// source means asset deletion sweeps the variants too (no separate GC).
func thumbCachePath(srcPath string, width int) string {
	return srcPath + ".w" + strconv.Itoa(width) + ".jpg"
}

// generateThumbnail decodes srcPath, downscales to fit `width` (preserving
// aspect ratio), encodes as JPEG q=82, and writes to dstPath. Returns the
// encoded bytes for immediate serving so the caller doesn't re-read from disk.
func generateThumbnail(srcPath, dstPath string, width int) ([]byte, error) {
	src, err := os.Open(srcPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = src.Close() }()

	img, _, err := image.Decode(src)
	if err != nil {
		return nil, err
	}

	bounds := img.Bounds()
	srcW, srcH := bounds.Dx(), bounds.Dy()
	if srcW <= 0 || srcH <= 0 {
		return nil, errors.New("invalid image dimensions")
	}
	if srcW <= width {
		// Source is already smaller than the requested thumb; encoding a larger
		// JPEG would inflate bytes for no benefit. Signal the caller to fall
		// back to the original.
		return nil, errSkipThumb
	}

	dstW := width
	dstH := srcH * width / srcW
	if dstH < 1 {
		dstH = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	// CatmullRom gives noticeably better quality than ApproxBiLinear for
	// downscales of photographic content; thumbnail generation is one-shot per
	// asset+width so the extra cost is acceptable.
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 82}); err != nil {
		return nil, err
	}

	// Write atomically: tmp file then rename so concurrent readers never see a
	// partial JPEG.
	tmp := dstPath + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, dstPath); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}

	return buf.Bytes(), nil
}

// errSkipThumb signals that the request should fall through to the original
// file — used when the source is already smaller than the requested width.
var errSkipThumb = errors.New("thumb skipped: source smaller than target")

// Ensure decoders for the formats we serve are registered. image/png,
// image/jpeg, image/gif self-register via init() in their packages; we add
// blank imports so they're pulled in even if no other code references them.
var _ = png.Encode
var _ = jpeg.Encode
var _ = gif.Decode
