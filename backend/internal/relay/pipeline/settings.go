package pipeline

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// settingsGroupGateway relay 管线运行时开关所在的 settings 分组。
const settingsGroupGateway = "gateway"

// settingsCacheTTL 热路径设置的缓存时长：写后最迟 3s 生效（单实例部署，无需广播失效）。
const settingsCacheTTL = 3 * time.Second

// settingsRefreshTimeout 刷新读 DB 的超时：刷新用独立后台 ctx，不受触发请求断连影响。
const settingsRefreshTimeout = 3 * time.Second

// settingsStaleTTL 刷新失败后的短续期：serve-stale 期间尽快重试，同时避免连环打挂的 DB。
const settingsStaleTTL = 1 * time.Second

// Setting 设置项（窄结构，避免 pipeline 依赖 app/settings 包）。
type Setting struct {
	Key   string
	Value string
}

// SettingsLister 按分组读取设置的窄接口（由 app/settings.Service 经适配器实现）。
type SettingsLister interface {
	List(ctx context.Context, group string) ([]Setting, error)
}

// GatewaySettings relay 管线运行时开关快照。
type GatewaySettings struct {
	// AutoBanEnabled 401/403/关键词自动禁用总开关（channel_auto_ban_enabled，默认 true）。
	AutoBanEnabled bool
	// BanKeywords 错误体关键词表（channel_ban_keywords，JSON 数组；已统一小写）。
	BanKeywords []string
	// UnpricedModelAllow 缺价放行开关（unpriced_model_allow，默认 false）。
	UnpricedModelAllow bool
}

// defaultBanKeywords 关键词表默认种子（与契约 §5 一致，全小写）。
var defaultBanKeywords = []string{
	"invalid_api_key",
	"incorrect api key",
	"account deactivated",
	"insufficient_quota",
	"api key not valid",
}

// defaultGatewaySettings 返回默认开关（lister 缺失或读失败时的兜底）。
func defaultGatewaySettings() GatewaySettings {
	return GatewaySettings{
		AutoBanEnabled:     true,
		BanKeywords:        defaultBanKeywords,
		UnpricedModelAllow: false,
	}
}

// SettingsReader gateway 设置的 TTL 缓存读取器（转发热路径专用，避免每请求打 DB）。
type SettingsReader struct {
	lister SettingsLister

	mu     sync.Mutex
	cached GatewaySettings
	// loadedOnce 是否成功加载过：serve-stale 只在有过成功快照时生效。
	loadedOnce bool
	expiresAt  time.Time
	// refreshing 刷新单飞标志：刷新进行中其余请求直接用现值，不排队等 DB。
	refreshing bool
}

// NewSettingsReader 创建设置读取器；lister 可为 nil（恒返回默认值，测试/降级友好）。
func NewSettingsReader(lister SettingsLister) *SettingsReader {
	return &SettingsReader{lister: lister}
}

// Get 返回当前生效的 gateway 设置：TTL 内走缓存，过期惰性重读（锁外执行，单飞合并）。
//
// 刷新用独立后台 ctx + 短超时，不用触发请求的 ctx——避免客户端断连把
// context.Canceled 放大成缓存污染。刷新失败 serve-stale：曾成功加载过则
// 沿用旧值并短续期；仅从未成功加载过才回退默认值。
func (r *SettingsReader) Get(_ context.Context) GatewaySettings {
	if r.lister == nil {
		return defaultGatewaySettings()
	}

	r.mu.Lock()
	if time.Now().Before(r.expiresAt) || r.refreshing {
		settings := r.currentLocked()
		r.mu.Unlock()
		return settings
	}
	r.refreshing = true
	r.mu.Unlock()

	// 刷新在锁外执行：DB 慢时其余请求不在 Get 上头阻塞。
	refreshCtx, cancel := context.WithTimeout(context.Background(), settingsRefreshTimeout)
	items, err := r.lister.List(refreshCtx, settingsGroupGateway)
	cancel()

	r.mu.Lock()
	defer r.mu.Unlock()
	r.refreshing = false
	if err != nil {
		slog.Warn("gateway_settings_load_failed", "error", err)
		r.expiresAt = time.Now().Add(settingsStaleTTL)
		return r.currentLocked()
	}
	settings := defaultGatewaySettings()
	applySettings(&settings, items)
	r.cached = settings
	r.loadedOnce = true
	r.expiresAt = time.Now().Add(settingsCacheTTL)
	return settings
}

// currentLocked 返回当前生效值：成功加载过用缓存快照，否则用默认值。须持锁调用。
func (r *SettingsReader) currentLocked() GatewaySettings {
	if r.loadedOnce {
		return r.cached
	}
	return defaultGatewaySettings()
}

// applySettings 把设置项解析进快照；单项解析失败保留该项默认值。
func applySettings(s *GatewaySettings, items []Setting) {
	for _, item := range items {
		value := strings.TrimSpace(item.Value)
		if value == "" {
			continue
		}
		switch item.Key {
		case "channel_auto_ban_enabled":
			s.AutoBanEnabled = value != "false"
		case "unpriced_model_allow":
			s.UnpricedModelAllow = value == "true"
		case "channel_ban_keywords":
			var keywords []string
			if err := json.Unmarshal([]byte(value), &keywords); err != nil {
				slog.Warn("gateway_settings_ban_keywords_invalid", "error", err)
				continue
			}
			normalized := make([]string, 0, len(keywords))
			for _, kw := range keywords {
				kw = strings.ToLower(strings.TrimSpace(kw))
				if kw != "" {
					normalized = append(normalized, kw)
				}
			}
			s.BanKeywords = normalized
		}
	}
}
