package server

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write png: %v", err)
	}
}

func TestResolveThumbWidth(t *testing.T) {
	cases := map[string]int{
		"":     0,
		"abc":  0,
		"100":  0,
		"256":  256,
		"512":  512,
		"1024": 0,
	}
	for in, want := range cases {
		if got := resolveThumbWidth(in); got != want {
			t.Errorf("resolveThumbWidth(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestGenerateThumbnail_DownscalesAndCaches(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.png")
	writePNG(t, src, 1024, 1024)

	dst := thumbCachePath(src, 512)
	data, err := generateThumbnail(src, dst, 512)
	if err != nil {
		t.Fatalf("generateThumbnail: %v", err)
	}

	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("cache file not written: %v", err)
	}

	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode jpeg: %v", err)
	}
	if got := img.Bounds().Dx(); got != 512 {
		t.Errorf("thumb width = %d, want 512", got)
	}
	if got := img.Bounds().Dy(); got != 512 {
		t.Errorf("thumb height = %d, want 512", got)
	}
}

func TestGenerateThumbnail_SkipsWhenSourceSmaller(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "small.png")
	writePNG(t, src, 200, 200)

	dst := thumbCachePath(src, 512)
	_, err := generateThumbnail(src, dst, 512)
	if err != errSkipThumb {
		t.Errorf("expected errSkipThumb, got %v", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Error("cache file should not exist when skip signaled")
	}
}

func TestThumbnailableExt(t *testing.T) {
	cases := map[string]bool{
		"foo.png":  true,
		"foo.PNG":  true,
		"foo.jpg":  true,
		"foo.jpeg": true,
		"foo.webp": true,
		"foo.gif":  true,
		"foo.svg":  false,
		"foo.txt":  false,
		"foo":      false,
	}
	for in, want := range cases {
		if got := thumbnailableExt(in); got != want {
			t.Errorf("thumbnailableExt(%q) = %v, want %v", in, got, want)
		}
	}
}
