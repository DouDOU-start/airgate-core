package server

import (
	"bytes"
	"context"
	"testing"
	"time"

	appsettings "github.com/DouDOU-start/airgate-core/internal/app/settings"
)

func TestIndexHTMLRenderer(t *testing.T) {
	raw := []byte(`<meta property="og:image" content="/og-cover.png" /><meta name="twitter:image" content="/og-cover.png" />`)

	t.Run("无自定义配置时原样透传", func(t *testing.T) {
		svc := appsettings.NewService(stubRepo{list: func(context.Context, string) ([]appsettings.Setting, error) {
			return nil, nil
		}}, "")
		r := newIndexHTMLRenderer(raw, svc)

		got := r.Bytes(context.Background())
		if !bytes.Equal(got, raw) {
			t.Fatalf("Bytes() = %q, want raw unchanged", got)
		}
	})

	t.Run("配置了 og_image 时替换两处 meta", func(t *testing.T) {
		svc := appsettings.NewService(stubRepo{list: func(context.Context, string) ([]appsettings.Setting, error) {
			return []appsettings.Setting{{Key: "og_image", Value: "/uploads/cover.png", Group: "site"}}, nil
		}}, "")
		r := newIndexHTMLRenderer(raw, svc)
		r.lastLoad = time.Time{} // 强制立即刷新，不等 TTL

		got := r.Bytes(context.Background())
		want := []byte(`<meta property="og:image" content="/uploads/cover.png" /><meta name="twitter:image" content="/uploads/cover.png" />`)
		if !bytes.Equal(got, want) {
			t.Fatalf("Bytes() = %q, want %q", got, want)
		}
	})

	t.Run("TTL 内不重新查库", func(t *testing.T) {
		calls := 0
		svc := appsettings.NewService(stubRepo{list: func(context.Context, string) ([]appsettings.Setting, error) {
			calls++
			return nil, nil
		}}, "")
		r := newIndexHTMLRenderer(raw, svc)

		r.Bytes(context.Background())
		r.Bytes(context.Background())
		r.Bytes(context.Background())
		if calls != 1 {
			t.Fatalf("Settings 查询次数 = %d, want 1（TTL 内应命中缓存）", calls)
		}
	})
}

type stubRepo struct {
	list func(context.Context, string) ([]appsettings.Setting, error)
}

func (s stubRepo) List(ctx context.Context, group string) ([]appsettings.Setting, error) {
	return s.list(ctx, group)
}

func (s stubRepo) UpsertMany(context.Context, []appsettings.ItemInput) error { return nil }
