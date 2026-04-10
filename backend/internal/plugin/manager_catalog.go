package plugin

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	sdk "github.com/DouDOU-start/airgate-sdk"
	sdkgrpc "github.com/DouDOU-start/airgate-sdk/grpc"

	"github.com/DouDOU-start/airgate-core/ent"
	pluginent "github.com/DouDOU-start/airgate-core/ent/plugin"
)

// GetExtensionByName 根据插件名查找 extension 类型插件。
func (m *Manager) GetExtensionByName(name string) *sdkgrpc.ExtensionGRPCClient {
	m.mu.RLock()
	defer m.mu.RUnlock()
	inst := m.instances[m.resolveNameLocked(name)]
	if inst == nil {
		return nil
	}
	return inst.Extension
}

// GetPluginByPlatform 根据平台查找运行中的插件实例。
func (m *Manager) GetPluginByPlatform(platform string) *PluginInstance {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, inst := range m.instances {
		if inst.Platform == platform {
			return inst
		}
	}
	return nil
}

// GetInstance 获取插件实例。
func (m *Manager) GetInstance(name string) *PluginInstance {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.instances[m.resolveNameLocked(name)]
}

// GetCredentialFields 获取指定平台的凭证字段声明。
func (m *Manager) GetCredentialFields(platform string) []sdk.CredentialField {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneCredentialFields(m.credCache[platform])
}

// GetAccountTypes 获取指定平台的账号类型声明。
func (m *Manager) GetAccountTypes(platform string) []sdk.AccountType {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneAccountTypes(m.accountTypeCache[platform])
}

// GetModels 获取指定平台的模型列表。
func (m *Manager) GetModels(platform string) []sdk.ModelInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneModels(m.modelCache[platform])
}

// GetRoutes 获取指定插件的路由声明。
func (m *Manager) GetRoutes(pluginName string) []sdk.RouteDefinition {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneRoutes(m.routeCache[m.resolveNameLocked(pluginName)])
}

// GetAllRoutes 获取所有运行中插件的路由。
func (m *Manager) GetAllRoutes() map[string][]sdk.RouteDefinition {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string][]sdk.RouteDefinition, len(m.routeCache))
	for key, routes := range m.routeCache {
		result[key] = cloneRoutes(routes)
	}
	return result
}

// MatchPluginByRoute 根据请求方法和路径匹配插件。
func (m *Manager) MatchPluginByRoute(method, path string) *PluginInstance {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for pluginName, routes := range m.routeCache {
		for _, route := range routes {
			if route.Method == method && route.Path == path {
				if inst, ok := m.instances[pluginName]; ok {
					return inst
				}
			}
		}
	}
	return nil
}

// MatchPluginByPathPrefix 根据路径前缀匹配插件。
func (m *Manager) MatchPluginByPathPrefix(path string) *PluginInstance {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for pluginName, routes := range m.routeCache {
		for _, route := range routes {
			if matchRoutePath(route.Path, path) {
				if inst, ok := m.instances[pluginName]; ok {
					return inst
				}
			}
		}
	}
	return nil
}

// MatchPluginByPlatformAndPath 根据平台和路径匹配插件。
func (m *Manager) MatchPluginByPlatformAndPath(platform, path string) *PluginInstance {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for pluginName, inst := range m.instances {
		if inst.Platform != platform {
			continue
		}
		for _, route := range m.routeCache[pluginName] {
			if matchRoutePath(route.Path, path) {
				return inst
			}
		}
	}
	return nil
}

func matchRoutePath(routePath, path string) bool {
	return path == routePath || len(path) > len(routePath) && strings.HasPrefix(path, routePath)
}

// IsRunning 检查插件是否正在运行。
func (m *Manager) IsRunning(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.instances[m.resolveNameLocked(name)]
	return ok
}

// RunningCount 获取运行中的插件数量。
func (m *Manager) RunningCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.instances)
}

// GetFrontendPages 获取插件声明的前端页面。
func (m *Manager) GetFrontendPages(pluginName string) []sdk.FrontendPage {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneFrontendPages(m.frontendPageCache[m.resolveNameLocked(pluginName)])
}

// DevWebDistPath 返回某个 dev 模式插件前端构建产物的本地路径。
//
// 约定：插件的源码 root 都遵循 `<plugin>/backend` + `<plugin>/web` 平级布局，
// 因此 web/dist 在 srcPath 的父目录下的 web/dist 子目录。
//
// 用途：core router 的 plugin assets handler 在 dev 模式下从这个路径直读
// vite 实时构建的产物，让"改插件前端 → 浏览器刷新立即生效"成为可能，
// 不必再让 vite watch 写到 core 的 data/plugins/<id>/assets/。
//
// 返回 (path, true) 表示该插件是 dev 模式且 web/dist 路径已计算好（不保证目录存在）。
// 返回 ("", false) 表示该插件是 production 模式，调用方应 fallback 到
// data/plugins/<id>/assets。
func (m *Manager) DevWebDistPath(pluginName string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	src, ok := m.devPaths[m.resolveNameLocked(pluginName)]
	if !ok || src == "" {
		return "", false
	}
	// srcPath 例如 /path/to/airgate-health/backend
	// web/dist 例如 /path/to/airgate-health/web/dist
	return filepath.Join(filepath.Dir(src), "web", "dist"), true
}

// GetAllPluginMeta 获取所有运行中插件的元信息。
func (m *Manager) GetAllPluginMeta() []PluginMeta {
	m.mu.RLock()
	defer m.mu.RUnlock()

	metas := make([]PluginMeta, 0, len(m.instances))
	for _, inst := range m.instances {
		_, isDev := m.devPaths[inst.Name]
		meta := PluginMeta{
			Name:               inst.Name,
			DisplayName:        inst.DisplayName,
			Version:            inst.Version,
			Author:             inst.Author,
			Type:               inst.Type,
			Platform:           inst.Platform,
			InstructionPresets: inst.InstructionPresets,
			ConfigSchema:       cloneConfigSchema(inst.ConfigSchema),
			IsDev:              isDev,
		}
		if types, ok := m.accountTypeCache[inst.Platform]; ok {
			meta.AccountTypes = cloneAccountTypes(types)
		}
		if pages, ok := m.frontendPageCache[inst.Name]; ok {
			meta.FrontendPages = cloneFrontendPages(pages)
		}
		assetsDir := filepath.Join(m.pluginDir, inst.Name, "assets")
		if _, err := os.Stat(assetsDir); err == nil {
			meta.HasWebAssets = true
		}
		metas = append(metas, meta)
	}
	return metas
}

// GetPluginConfig 读取插件的当前配置（来自 DB 持久化）。
// 用于「编辑配置」UI 展示当前值。
func (m *Manager) GetPluginConfig(ctx context.Context, name string) (map[string]string, error) {
	if m.db == nil {
		return map[string]string{}, nil
	}
	resolved := m.resolveName(name)
	row, err := m.db.Plugin.Query().Where(pluginent.NameEQ(resolved)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("查询插件配置失败: %w", err)
	}
	out := make(map[string]string, len(row.Config))
	for k, v := range row.Config {
		out[k] = fmt.Sprintf("%v", v)
	}
	return out, nil
}

// UpdatePluginConfig 把用户提交的配置写入 DB。
// 仅写入；调用方负责后续 reload 让插件生效。
// 当 Plugin 行不存在时（dev 插件场景）会自动创建一条占位记录。
func (m *Manager) UpdatePluginConfig(ctx context.Context, name string, config map[string]string) error {
	if m.db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	resolved := m.resolveName(name)
	cfgJSON := make(map[string]interface{}, len(config))
	for k, v := range config {
		cfgJSON[k] = v
	}

	row, err := m.db.Plugin.Query().Where(pluginent.NameEQ(resolved)).Only(ctx)
	if err != nil {
		if !ent.IsNotFound(err) {
			return fmt.Errorf("查询插件失败: %w", err)
		}
		// 不存在则创建一条记录（dev / 未持久化的插件）
		inst := m.GetInstance(resolved)
		create := m.db.Plugin.Create().SetName(resolved).SetConfig(cfgJSON)
		if inst != nil {
			create = create.SetVersion(inst.Version).SetPlatform(inst.Platform)
			if inst.Type == "extension" || inst.Type == "gateway" {
				create = create.SetType(pluginent.Type(inst.Type))
			}
		}
		if _, err := create.Save(ctx); err != nil {
			return fmt.Errorf("创建插件配置失败: %w", err)
		}
		return nil
	}
	if _, err := row.Update().SetConfig(cfgJSON).Save(ctx); err != nil {
		return fmt.Errorf("更新插件配置失败: %w", err)
	}
	return nil
}

// ReloadInstance 用最新 DB 配置重启一个已加载的插件实例（dev 与正式都支持）。
// dev 插件走 ReloadDev（重新 go run）；正式插件走 stopPlugin + 重新启动二进制。
func (m *Manager) ReloadInstance(ctx context.Context, name string) error {
	resolved := m.resolveName(name)
	if m.IsDev(resolved) {
		return m.ReloadDev(ctx, resolved)
	}
	inst := m.GetInstance(resolved)
	if inst == nil {
		return fmt.Errorf("插件 %s 不存在或未运行", name)
	}
	binaryDir := inst.BinaryDir
	if binaryDir == "" {
		binaryDir = resolved
	}
	binaryPath := filepath.Join(m.pluginDir, binaryDir, binaryDir)
	if _, err := os.Stat(binaryPath); err != nil {
		return fmt.Errorf("插件二进制不存在: %s", binaryPath)
	}
	m.stopPlugin(resolved)
	if _, err := m.startPlugin(ctx, binaryDir, exec.Command(binaryPath), binaryDir); err != nil {
		return fmt.Errorf("重启插件失败: %w", err)
	}
	return nil
}

// HasWebAssets 检查插件是否有前端资源。
func (m *Manager) HasWebAssets(pluginName string) bool {
	assetsDir := filepath.Join(m.pluginDir, m.resolveName(pluginName), "assets")
	_, err := os.Stat(assetsDir)
	return err == nil
}

func (m *Manager) resolveName(name string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.resolveNameLocked(name)
}

func (m *Manager) resolveNameLocked(name string) string {
	name = normalizePluginName(name)
	if name == "" {
		return ""
	}
	if canonical, ok := m.aliases[name]; ok && canonical != "" {
		return canonical
	}
	return name
}

func (m *Manager) registerAliasesLocked(canonical string, aliases ...string) {
	canonical = normalizePluginName(canonical)
	if canonical == "" {
		return
	}
	m.aliases[canonical] = canonical
	for _, alias := range aliases {
		alias = normalizePluginName(alias)
		if alias == "" {
			continue
		}
		m.aliases[alias] = canonical
	}
}

func (m *Manager) unregisterAliasesLocked(canonical string, aliases ...string) {
	canonical = normalizePluginName(canonical)
	if canonical != "" {
		delete(m.aliases, canonical)
	}
	for _, alias := range aliases {
		alias = normalizePluginName(alias)
		if alias == "" {
			continue
		}
		delete(m.aliases, alias)
	}
}

func cloneModels(input []sdk.ModelInfo) []sdk.ModelInfo {
	return append([]sdk.ModelInfo(nil), input...)
}

func cloneRoutes(input []sdk.RouteDefinition) []sdk.RouteDefinition {
	return append([]sdk.RouteDefinition(nil), input...)
}

func cloneCredentialFields(input []sdk.CredentialField) []sdk.CredentialField {
	return append([]sdk.CredentialField(nil), input...)
}

func cloneAccountTypes(input []sdk.AccountType) []sdk.AccountType {
	if input == nil {
		return nil
	}
	cloned := make([]sdk.AccountType, 0, len(input))
	for _, item := range input {
		cloned = append(cloned, sdk.AccountType{
			Key:         item.Key,
			Label:       item.Label,
			Description: item.Description,
			Fields:      cloneCredentialFields(item.Fields),
		})
	}
	return cloned
}

func cloneFrontendPages(input []sdk.FrontendPage) []sdk.FrontendPage {
	return append([]sdk.FrontendPage(nil), input...)
}

func cloneConfigSchema(input []sdk.ConfigField) []sdk.ConfigField {
	return append([]sdk.ConfigField(nil), input...)
}
