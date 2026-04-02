package plugin

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

type webAssetsProvider interface {
	GetWebAssets() (map[string][]byte, error)
}

func (m *Manager) extractPluginWebAssets(pluginName string, provider webAssetsProvider) {
	assets, err := provider.GetWebAssets()
	if err != nil {
		slog.Warn("获取插件前端资源失败", "plugin", pluginName, "error", err)
		return
	}
	if len(assets) == 0 {
		return
	}

	assetsDir := filepath.Join(m.pluginDir, pluginName, "assets")
	if err := extractWebAssets(assetsDir, assets); err != nil {
		slog.Warn("写入插件前端资源失败", "plugin", pluginName, "error", err)
	} else {
		slog.Info("已提取插件前端资源", "plugin", pluginName, "files", len(assets))
	}
}

func extractWebAssets(dir string, assets map[string][]byte) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}
	for path, content := range assets {
		fullPath := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			return fmt.Errorf("创建子目录失败: %w", err)
		}
		if err := os.WriteFile(fullPath, content, 0644); err != nil {
			return fmt.Errorf("写入文件 %s 失败: %w", path, err)
		}
	}
	return nil
}
