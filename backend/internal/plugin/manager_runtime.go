package plugin

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

	goplugin "github.com/hashicorp/go-plugin"

	sdk "github.com/DouDOU-start/airgate-sdk"
	sdkgrpc "github.com/DouDOU-start/airgate-sdk/grpc"
	"github.com/DouDOU-start/airgate-sdk/shared"
)

// LoadAll 启动时扫描插件目录，发现可执行二进制则直接加载。
func (m *Manager) LoadAll(ctx context.Context) error {
	entries, err := os.ReadDir(m.pluginDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("读取插件目录失败: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		binaryPath := filepath.Join(m.pluginDir, name, name)
		info, err := os.Stat(binaryPath)
		if err != nil || info.IsDir() {
			continue
		}

		canonicalName, err := m.startPlugin(ctx, name, exec.Command(binaryPath), name)
		if err != nil {
			slog.Error("加载插件失败", "name", name, "error", err)
			continue
		}
		slog.Info("插件加载成功", "name", canonicalName, "source", name)
	}

	return nil
}

// LoadDev 加载开发模式插件。
func (m *Manager) LoadDev(ctx context.Context, name, srcPath string) error {
	if _, err := os.Stat(srcPath); err != nil {
		return fmt.Errorf("插件源码目录不存在: %s", srcPath)
	}

	requestedName := normalizePluginName(name)
	if requestedName == "" {
		dir := filepath.Base(srcPath)
		if dir == "backend" || dir == "." {
			dir = filepath.Base(filepath.Dir(srcPath))
		}
		requestedName = dir
	}

	cmd := exec.Command("go", "run", ".")
	cmd.Dir = srcPath

	canonicalName, err := m.startPlugin(ctx, requestedName, cmd, "")
	if err != nil {
		return fmt.Errorf("加载开发插件失败: %w", err)
	}

	m.mu.Lock()
	m.devPaths[canonicalName] = srcPath
	m.registerAliasesLocked(canonicalName, requestedName)
	m.mu.Unlock()

	slog.Info("开发插件加载成功", "name", canonicalName, "requested_name", requestedName, "src", srcPath)
	return nil
}

// ReloadDev 热加载开发模式插件。
func (m *Manager) ReloadDev(ctx context.Context, name string) error {
	m.mu.RLock()
	resolvedName := m.resolveNameLocked(name)
	srcPath, isDev := m.devPaths[resolvedName]
	m.mu.RUnlock()

	if !isDev {
		return fmt.Errorf("插件 %s 不是开发模式插件，无法热加载", name)
	}

	slog.Info("正在热加载开发插件", "name", resolvedName, "src", srcPath)
	m.stopPlugin(resolvedName)
	return m.LoadDev(ctx, resolvedName, srcPath)
}

// IsDev 检查插件是否为开发模式。
func (m *Manager) IsDev(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.devPaths[m.resolveNameLocked(name)]
	return ok
}

func (m *Manager) startPlugin(ctx context.Context, requestedName string, cmd *exec.Cmd, binaryDir string) (string, error) {
	client := goplugin.NewClient(&goplugin.ClientConfig{
		HandshakeConfig: shared.Handshake,
		Plugins: goplugin.PluginSet{
			shared.PluginKeyGateway:   &sdkgrpc.GatewayGRPCPlugin{},
			shared.PluginKeyExtension: &sdkgrpc.ExtensionGRPCPlugin{},
		},
		Cmd:              cmd,
		AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolGRPC},
		SyncStdout:       os.Stdout,
		SyncStderr:       os.Stderr,
	})

	rpcClient, err := client.Client()
	if err != nil {
		client.Kill()
		return "", fmt.Errorf("连接插件进程失败: %w", err)
	}

	raw, err := rpcClient.Dispense(shared.PluginKeyGateway)
	if err != nil {
		client.Kill()
		return "", fmt.Errorf("获取插件接口失败: %w", err)
	}
	probe, ok := raw.(*sdkgrpc.GatewayGRPCClient)
	if !ok {
		client.Kill()
		return "", fmt.Errorf("插件类型断言失败")
	}

	info := probe.Info()
	if info.Type == sdk.PluginTypeExtension {
		extRaw, err := rpcClient.Dispense(shared.PluginKeyExtension)
		if err != nil {
			client.Kill()
			return "", fmt.Errorf("获取 extension 插件接口失败: %w", err)
		}
		ext, ok := extRaw.(*sdkgrpc.ExtensionGRPCClient)
		if !ok {
			client.Kill()
			return "", fmt.Errorf("extension 插件类型断言失败")
		}
		return m.startExtensionPlugin(ctx, client, ext, requestedName, binaryDir)
	}

	return m.startGatewayPlugin(ctx, client, probe, requestedName, binaryDir)
}

func (m *Manager) startGatewayPlugin(ctx context.Context, client *goplugin.Client, gateway *sdkgrpc.GatewayGRPCClient, requestedName, binaryDir string) (string, error) {
	info := gateway.Info()
	canonicalName := canonicalPluginName(info, requestedName)
	if canonicalName == "" {
		client.Kill()
		return "", fmt.Errorf("插件未提供有效的 ID/name")
	}

	initConfig := map[string]interface{}{
		sdk.ConfigKeyLogLevel: m.logLevel,
	}
	pluginCtx := newCorePluginContext(initConfig, canonicalName)
	if err := gateway.Init(pluginCtx); err != nil {
		client.Kill()
		return "", fmt.Errorf("初始化插件失败: %w", err)
	}
	if err := gateway.Start(ctx); err != nil {
		client.Kill()
		return "", fmt.Errorf("启动插件失败: %w", err)
	}

	platform := gateway.Platform()
	models := gateway.Models()
	routes := gateway.Routes()
	pluginType := string(info.Type)
	if pluginType == "" {
		pluginType = "gateway"
	}

	instance := &PluginInstance{
		Name:        canonicalName,
		SourceName:  normalizePluginName(requestedName),
		BinaryDir:   normalizePluginName(binaryDir),
		DisplayName: info.Name,
		Version:     info.Version,
		Author:      info.Author,
		Platform:    platform,
		Type:        pluginType,
		Client:      client,
		Gateway:     gateway,
	}

	m.mu.Lock()
	m.instances[canonicalName] = instance
	m.registerAliasesLocked(canonicalName, requestedName, binaryDir)
	m.modelCache[platform] = cloneModels(models)
	m.routeCache[canonicalName] = cloneRoutes(routes)
	if len(info.AccountTypes) > 0 {
		m.credCache[platform] = cloneCredentialFields(info.AccountTypes[0].Fields)
	} else {
		delete(m.credCache, platform)
	}
	m.accountTypeCache[platform] = cloneAccountTypes(info.AccountTypes)
	if len(info.FrontendPages) > 0 {
		m.frontendPageCache[canonicalName] = cloneFrontendPages(info.FrontendPages)
	}
	m.mu.Unlock()

	m.extractPluginWebAssets(canonicalName, gateway)

	if normalizePluginName(requestedName) != "" && canonicalName != normalizePluginName(requestedName) {
		slog.Info("插件名称已统一到 Info().ID", "requested_name", requestedName, "canonical_name", canonicalName)
	}

	return canonicalName, nil
}

func (m *Manager) startExtensionPlugin(ctx context.Context, client *goplugin.Client, ext *sdkgrpc.ExtensionGRPCClient, requestedName, binaryDir string) (string, error) {
	info := ext.Info()
	canonicalName := canonicalPluginName(info, requestedName)
	if canonicalName == "" {
		client.Kill()
		return "", fmt.Errorf("插件未提供有效的 ID/name")
	}

	initConfig := map[string]interface{}{
		sdk.ConfigKeyLogLevel: m.logLevel,
	}
	pluginCtx := newCorePluginContext(initConfig, canonicalName)
	if err := ext.Init(pluginCtx); err != nil {
		client.Kill()
		return "", fmt.Errorf("初始化 extension 插件失败: %w", err)
	}
	if err := ext.Start(ctx); err != nil {
		client.Kill()
		return "", fmt.Errorf("启动 extension 插件失败: %w", err)
	}
	if err := ext.Migrate(); err != nil {
		slog.Warn("extension 插件迁移失败", "plugin", canonicalName, "error", err)
	}

	pluginType := string(info.Type)
	if pluginType == "" {
		pluginType = "extension"
	}

	instance := &PluginInstance{
		Name:        canonicalName,
		SourceName:  normalizePluginName(requestedName),
		BinaryDir:   normalizePluginName(binaryDir),
		DisplayName: info.Name,
		Version:     info.Version,
		Author:      info.Author,
		Type:        pluginType,
		Client:      client,
		Extension:   ext,
	}

	m.mu.Lock()
	m.instances[canonicalName] = instance
	m.registerAliasesLocked(canonicalName, requestedName, binaryDir)
	if len(info.FrontendPages) > 0 {
		m.frontendPageCache[canonicalName] = cloneFrontendPages(info.FrontendPages)
	}
	m.mu.Unlock()

	m.extractPluginWebAssets(canonicalName, ext)

	if normalizePluginName(requestedName) != "" && canonicalName != normalizePluginName(requestedName) {
		slog.Info("插件名称已统一到 Info().ID", "requested_name", requestedName, "canonical_name", canonicalName)
	}

	return canonicalName, nil
}

func (m *Manager) stopPlugin(name string) {
	m.mu.Lock()
	resolvedName := m.resolveNameLocked(name)
	inst, ok := m.instances[resolvedName]
	if !ok {
		m.mu.Unlock()
		return
	}
	delete(m.instances, resolvedName)
	delete(m.modelCache, inst.Platform)
	delete(m.routeCache, inst.Name)
	delete(m.credCache, inst.Platform)
	delete(m.accountTypeCache, inst.Platform)
	delete(m.frontendPageCache, inst.Name)
	m.unregisterAliasesLocked(inst.Name, inst.SourceName, inst.BinaryDir)
	m.mu.Unlock()

	if inst.Gateway != nil {
		if err := inst.Gateway.Stop(context.Background()); err != nil {
			slog.Warn("停止 gateway 插件失败", "name", inst.Name, "error", err)
		}
	}
	if inst.Extension != nil {
		if err := inst.Extension.Stop(context.Background()); err != nil {
			slog.Warn("停止 extension 插件失败", "name", inst.Name, "error", err)
		}
	}
	if inst.Client != nil {
		inst.Client.Kill()
	}

	slog.Info("插件已停止", "name", inst.Name)
}

// StopAll 停止所有插件。
func (m *Manager) StopAll(ctx context.Context) {
	m.mu.RLock()
	names := make([]string, 0, len(m.instances))
	for name := range m.instances {
		names = append(names, name)
	}
	m.mu.RUnlock()

	for _, name := range names {
		m.stopPlugin(name)
	}
}
