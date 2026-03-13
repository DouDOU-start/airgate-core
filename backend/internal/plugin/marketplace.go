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
	"sync"
)

// MarketplacePlugin 市场插件条目
type MarketplacePlugin struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Author      string `json:"author"`
	Type        string `json:"type"` // gateway / payment / extension
	DownloadURL string `json:"download_url,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
}

// RegistryJSON 插件源注册表结构
type RegistryJSON struct {
	Version string              `json:"version"`
	Plugins []MarketplacePlugin `json:"plugins"`
}

// Marketplace 插件市场
type Marketplace struct {
	pluginDir string
	mu        sync.RWMutex
	cache     []MarketplacePlugin // 缓存的插件列表
}

// NewMarketplace 创建插件市场
func NewMarketplace(pluginDir string) *Marketplace {
	return &Marketplace{
		pluginDir: pluginDir,
	}
}

// officialPlugins 官方插件列表（作为无源时的 fallback）
var officialPlugins = []MarketplacePlugin{
	{
		Name:        "gateway-openai",
		Version:     "0.1.0",
		Description: "OpenAI API 网关插件",
		Author:      "AirGate",
		Type:        "gateway",
	},
	{
		Name:        "gateway-claude",
		Version:     "0.1.0",
		Description: "Anthropic Claude API 网关插件",
		Author:      "AirGate",
		Type:        "gateway",
	},
	{
		Name:        "gateway-gemini",
		Version:     "0.1.0",
		Description: "Google Gemini API 网关插件",
		Author:      "AirGate",
		Type:        "gateway",
	},
	{
		Name:        "gateway-sora",
		Version:     "0.1.0",
		Description: "OpenAI Sora 视频生成网关插件",
		Author:      "AirGate",
		Type:        "gateway",
	},
	{
		Name:        "gateway-antigravity",
		Version:     "0.1.0",
		Description: "反重力 API 网关插件",
		Author:      "AirGate",
		Type:        "gateway",
	},
	{
		Name:        "payment-epay",
		Version:     "0.1.0",
		Description: "易支付接入插件",
		Author:      "AirGate",
		Type:        "payment",
	},
}

// ListAvailable 列出可用插件
func (m *Marketplace) ListAvailable(ctx context.Context) ([]MarketplacePlugin, error) {
	m.mu.RLock()
	cached := m.cache
	m.mu.RUnlock()

	if len(cached) > 0 {
		return cached, nil
	}

	// 无缓存时返回官方列表
	return officialPlugins, nil
}

// SyncFromURL 从指定 URL 同步插件列表
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
