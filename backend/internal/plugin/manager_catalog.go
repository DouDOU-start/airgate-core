package plugin

import (
	"os"
	"path/filepath"
	"strings"

	sdk "github.com/DouDOU-start/airgate-sdk"
	sdkgrpc "github.com/DouDOU-start/airgate-sdk/grpc"
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

// GetAllPluginMeta 获取所有运行中插件的元信息。
func (m *Manager) GetAllPluginMeta() []PluginMeta {
	m.mu.RLock()
	defer m.mu.RUnlock()

	metas := make([]PluginMeta, 0, len(m.instances))
	for _, inst := range m.instances {
		_, isDev := m.devPaths[inst.Name]
		meta := PluginMeta{
			Name:        inst.Name,
			DisplayName: inst.DisplayName,
			Version:     inst.Version,
			Author:      inst.Author,
			Type:        inst.Type,
			Platform:    inst.Platform,
			IsDev:       isDev,
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
