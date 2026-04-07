package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// MarketplacePlugin 市场插件条目
type MarketplacePlugin struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Author      string `json:"author"`
	Type        string `json:"type"` // gateway / payment / extension
	GithubRepo  string `json:"github_repo,omitempty"`
	DownloadURL string `json:"download_url,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
}

// RegistryJSON 插件源注册表结构
type RegistryJSON struct {
	Version string              `json:"version"`
	Plugins []MarketplacePlugin `json:"plugins"`
}

// MarketplaceOption 配置选项
type MarketplaceOption func(*Marketplace)

// WithGithubToken 设置 GitHub Token
func WithGithubToken(token string) MarketplaceOption {
	return func(m *Marketplace) {
		m.githubToken = token
	}
}

// WithEntries 用配置文件中的条目覆盖默认列表
func WithEntries(entries []MarketplacePlugin) MarketplaceOption {
	return func(m *Marketplace) {
		if len(entries) > 0 {
			m.entries = entries
		}
	}
}

// WithRefreshInterval 设置后台同步间隔
func WithRefreshInterval(d time.Duration) MarketplaceOption {
	return func(m *Marketplace) {
		if d > 0 {
			m.refreshInterval = d
		}
	}
}

// Marketplace 插件市场
type Marketplace struct {
	pluginDir       string
	githubToken     string
	refreshInterval time.Duration

	mu      sync.RWMutex
	entries []MarketplacePlugin // 静态条目（含 github_repo）
	cache   []MarketplacePlugin // 已同步的最新数据
	etags   map[string]string   // repo -> ETag，用于条件请求避免消耗配额

	stopCh  chan struct{}
	stopped chan struct{}
	once    sync.Once
}

// 默认刷新间隔：6 小时
// 6 个插件 × 4 次/天 = 24 次/天，未认证 IP 配额 60/h 也绰绰有余；
// 配合 ETag 条件请求，未变更时返回 304 不计配额。
const defaultRefreshInterval = 6 * time.Hour

// NewMarketplace 创建插件市场
func NewMarketplace(pluginDir string, opts ...MarketplaceOption) *Marketplace {
	m := &Marketplace{
		pluginDir:       pluginDir,
		refreshInterval: defaultRefreshInterval,
		entries:         append([]MarketplacePlugin(nil), officialPlugins...),
		etags:           make(map[string]string),
		stopCh:          make(chan struct{}),
		stopped:         make(chan struct{}),
	}
	for _, opt := range opts {
		opt(m)
	}
	// 初始 cache 用静态 entries 兜底，避免首次同步前列表为空
	m.cache = append([]MarketplacePlugin(nil), m.entries...)
	return m
}

// officialPlugins 官方插件列表（作为无源时的 fallback，绑定 GitHub 仓库）
var officialPlugins = []MarketplacePlugin{
	{
		Name:        "gateway-openai",
		Version:     "0.1.0",
		Description: "OpenAI API 网关插件",
		Author:      "AirGate",
		Type:        "gateway",
		GithubRepo:  "DouDOU-start/airgate-openai",
	},
	{
		Name:        "gateway-claude",
		Version:     "0.1.0",
		Description: "Anthropic Claude API 网关插件",
		Author:      "AirGate",
		Type:        "gateway",
		GithubRepo:  "DouDOU-start/airgate-claude",
	},
	{
		Name:        "payment-epay",
		Version:     "0.1.0",
		Description: "易支付接入插件",
		Author:      "AirGate",
		Type:        "payment",
		GithubRepo:  "DouDOU-start/airgate-epay",
	},
}

// ListAvailable 列出可用插件（返回缓存或同步后的数据）
func (m *Marketplace) ListAvailable(ctx context.Context) ([]MarketplacePlugin, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]MarketplacePlugin, len(m.cache))
	copy(out, m.cache)
	return out, nil
}

// Start 启动后台同步 goroutine。若 entries 中没有任何 GithubRepo 则跳过。
func (m *Marketplace) Start(ctx context.Context) {
	go m.run(ctx)
}

// Stop 停止后台同步
func (m *Marketplace) Stop() {
	m.once.Do(func() {
		close(m.stopCh)
		<-m.stopped
	})
}

// run 后台运行循环：启动时同步一次，之后按 refreshInterval 定时同步
func (m *Marketplace) run(ctx context.Context) {
	defer close(m.stopped)

	// 启动后立即同步一次（异步，不阻塞 server 启动）
	if err := m.SyncFromGithub(ctx); err != nil {
		slog.Warn("插件市场首次同步失败", "error", err)
	}

	ticker := time.NewTicker(m.refreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			if err := m.SyncFromGithub(ctx); err != nil {
				slog.Warn("插件市场同步失败", "error", err)
			}
		}
	}
}

// SyncFromGithub 遍历 entries，串行调用 GitHub API 拉取 latest release。
// 使用 ETag 条件请求：上游 release 未变更时 GitHub 返回 304，**不消耗 API 配额**。
func (m *Marketplace) SyncFromGithub(ctx context.Context) error {
	m.mu.RLock()
	entries := append([]MarketplacePlugin(nil), m.entries...)
	prevCache := append([]MarketplacePlugin(nil), m.cache...)
	token := m.githubToken
	etagSnapshot := make(map[string]string, len(m.etags))
	for k, v := range m.etags {
		etagSnapshot[k] = v
	}
	m.mu.RUnlock()

	if len(entries) == 0 {
		return nil
	}

	// 把上次缓存按 name 索引，方便 304 时复用旧数据
	prevByName := make(map[string]MarketplacePlugin, len(prevCache))
	for _, p := range prevCache {
		prevByName[p.Name] = p
	}

	updated := make([]MarketplacePlugin, 0, len(entries))
	newEtags := make(map[string]string, len(entries))
	var lastErr error
	notModified := 0
	fetched := 0

	for _, entry := range entries {
		if entry.GithubRepo == "" {
			updated = append(updated, entry)
			continue
		}

		release, etag, status, err := fetchLatestRelease(ctx, entry.GithubRepo, token, etagSnapshot[entry.GithubRepo])
		if err != nil {
			slog.Debug("拉取插件 release 失败", "repo", entry.GithubRepo, "error", err)
			lastErr = err
			// 失败时保留上次缓存条目
			if prev, ok := prevByName[entry.Name]; ok {
				updated = append(updated, prev)
			} else {
				updated = append(updated, entry)
			}
			// 保留旧 etag 以便下次仍走条件请求
			if old := etagSnapshot[entry.GithubRepo]; old != "" {
				newEtags[entry.GithubRepo] = old
			}
			continue
		}

		if status == http.StatusNotModified {
			notModified++
			// 复用上次结果，etag 保留
			if prev, ok := prevByName[entry.Name]; ok {
				updated = append(updated, prev)
			} else {
				updated = append(updated, entry)
			}
			newEtags[entry.GithubRepo] = etagSnapshot[entry.GithubRepo]
			continue
		}

		fetched++
		merged := entry
		if release.TagName != "" {
			merged.Version = strings.TrimPrefix(release.TagName, "v")
		}
		// 描述保持静态值（来自 officialPlugins 或 config），不用 release body：
		// release notes 描述的是"这一版改了什么"，与"插件是干嘛的"是两回事，
		// GitHub generate_release_notes 还会自动塞入 "## What's Changed" 标题。
		updated = append(updated, merged)
		if etag != "" {
			newEtags[entry.GithubRepo] = etag
		}
	}

	m.mu.Lock()
	m.cache = updated
	m.etags = newEtags
	m.mu.Unlock()

	slog.Info("插件市场同步完成",
		"count", len(updated),
		"fetched", fetched,
		"not_modified", notModified,
	)
	return lastErr
}

// SyncFromURL 从指定 URL 同步插件列表（保留兼容旧接口）
func (m *Marketplace) SyncFromURL(ctx context.Context, registryURL string) error {
	resp, err := http.Get(registryURL)
	if err != nil {
		return fmt.Errorf("请求插件源失败: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Warn("关闭插件源响应失败", "url", registryURL, "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("插件源返回状态码 %d", resp.StatusCode)
	}

	var registry RegistryJSON
	if err := json.NewDecoder(resp.Body).Decode(&registry); err != nil {
		return fmt.Errorf("解析插件源数据失败: %w", err)
	}

	m.mu.Lock()
	m.cache = registry.Plugins
	m.mu.Unlock()

	slog.Info("插件源同步完成", "url", registryURL, "count", len(registry.Plugins))
	return nil
}

// githubReleaseInfo GitHub release API 简化结构
type githubReleaseInfo struct {
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
	Body    string `json:"body"`
}

// fetchLatestRelease 调用 GitHub API 获取仓库最新 release。
// 若提供 etag，会发送 If-None-Match 条件请求；上游未变更时返回 (nil, "", 304, nil)，**不消耗配额**。
// 返回值：release 信息、新的 ETag、HTTP 状态码、错误
func fetchLatestRelease(ctx context.Context, repo, token, etag string) (*githubReleaseInfo, string, int, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, "", 0, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", 0, fmt.Errorf("请求 GitHub API 失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// 304 Not Modified：上游未变更，直接复用缓存（不消耗 GitHub 配额）
	if resp.StatusCode == http.StatusNotModified {
		return nil, etag, http.StatusNotModified, nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, "", resp.StatusCode, fmt.Errorf("仓库 %s 没有 release", repo)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", resp.StatusCode, fmt.Errorf("GitHub API 状态码 %d", resp.StatusCode)
	}

	var info githubReleaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, "", resp.StatusCode, fmt.Errorf("解析 release 失败: %w", err)
	}
	return &info, resp.Header.Get("ETag"), resp.StatusCode, nil
}

// Download 下载插件二进制到本地
func (m *Marketplace) Download(ctx context.Context, pluginName, version, downloadURL, expectedSHA256 string) (string, error) {
	// 创建目标目录
	targetDir := filepath.Join(m.pluginDir, pluginName)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return "", fmt.Errorf("创建插件目录失败: %w", err)
	}

	// 下载文件
	resp, err := http.Get(downloadURL)
	if err != nil {
		return "", fmt.Errorf("下载插件失败: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Warn("关闭插件下载响应失败", "url", downloadURL, "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载返回状态码 %d", resp.StatusCode)
	}

	// 写入临时文件
	tmpFile := filepath.Join(targetDir, pluginName+".tmp")
	f, err := os.Create(tmpFile)
	if err != nil {
		return "", fmt.Errorf("创建临时文件失败: %w", err)
	}
	closeTempFile := func() error {
		if err := f.Close(); err != nil {
			return fmt.Errorf("关闭临时文件失败: %w", err)
		}
		return nil
	}
	removeTempFile := func() {
		if err := os.Remove(tmpFile); err != nil && !os.IsNotExist(err) {
			slog.Warn("删除临时插件文件失败", "path", tmpFile, "error", err)
		}
	}

	hasher := sha256.New()
	writer := io.MultiWriter(f, hasher)

	if _, err := io.Copy(writer, resp.Body); err != nil {
		if closeErr := closeTempFile(); closeErr != nil {
			slog.Warn("写入失败后关闭临时文件失败", "path", tmpFile, "error", closeErr)
		}
		removeTempFile()
		return "", fmt.Errorf("写入文件失败: %w", err)
	}
	if err := closeTempFile(); err != nil {
		removeTempFile()
		return "", err
	}

	// SHA256 校验
	if expectedSHA256 != "" {
		actualHash := hex.EncodeToString(hasher.Sum(nil))
		if actualHash != expectedSHA256 {
			removeTempFile()
			return "", fmt.Errorf("SHA256 校验失败: 期望 %s，实际 %s", expectedSHA256, actualHash)
		}
	}

	// 重命名为最终文件
	finalPath := filepath.Join(targetDir, pluginName)
	if err := os.Rename(tmpFile, finalPath); err != nil {
		removeTempFile()
		return "", fmt.Errorf("移动文件失败: %w", err)
	}

	// 设置可执行权限
	if err := os.Chmod(finalPath, 0755); err != nil {
		return "", fmt.Errorf("设置执行权限失败: %w", err)
	}

	slog.Info("插件下载完成", "name", pluginName, "version", version, "path", finalPath)
	return finalPath, nil
}
