package server

import (
	"bytes"
	"context"
	"sync"
	"time"

	appsettings "github.com/DouDOU-start/airgate-core/internal/app/settings"
)

// defaultOGImagePath 是构建产物里内置的分享卡片封面图路径，同时也是
// index.html 模板里 og:image / twitter:image 的原始值，用作替换锚点。
const defaultOGImagePath = "/og-cover.png"

// ogImageRefreshInterval 惰性刷新周期：index.html 在 NoRoute 里几乎每次
// 页面加载都会被吐出，若每次都查 Settings 表等于给每次页面加载都加一次
// DB 查询。og:image 极少变更，容忍这个周期内的短暂陈旧是划算的。
const ogImageRefreshInterval = 30 * time.Second

// indexHTMLRenderer 按管理员配置的 site.og_image 动态改写嵌入的 index.html
// 里的分享卡片封面图，其余内容原样透传。
type indexHTMLRenderer struct {
	mu       sync.Mutex
	raw      []byte // 嵌入的原始 index.html（不可变）
	rendered []byte // 当前对外吐出的字节（raw 或替换过封面图之后的版本）
	current  string // rendered 里当前生效的封面图 URL
	lastLoad time.Time
	settings *appsettings.Service
}

func newIndexHTMLRenderer(raw []byte, settingsSvc *appsettings.Service) *indexHTMLRenderer {
	return &indexHTMLRenderer{
		raw:      raw,
		rendered: raw,
		current:  defaultOGImagePath,
		settings: settingsSvc,
	}
}

// Bytes 返回当前应下发的 index.html 字节。TTL 内直接命中缓存；
// 过期后查一次 Settings，值变化时才重新替换并缓存。
func (r *indexHTMLRenderer) Bytes(ctx context.Context) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	if time.Since(r.lastLoad) < ogImageRefreshInterval {
		return r.rendered
	}
	r.lastLoad = time.Now()

	url := r.settings.SiteOGImage(ctx)
	if url == "" {
		url = defaultOGImagePath
	}
	if url == r.current {
		return r.rendered
	}
	r.current = url
	r.rendered = bytes.ReplaceAll(r.raw, []byte(defaultOGImagePath), []byte(url))
	return r.rendered
}
