// Package plugin 提供插件生命周期管理、市场和请求转发
package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	goplugin "github.com/hashicorp/go-plugin"

	sdk "github.com/DouDOU-start/airgate-sdk"
	sdkgrpc "github.com/DouDOU-start/airgate-sdk/grpc"
	"github.com/DouDOU-start/airgate-sdk/shared"

	"github.com/DouDOU-start/airgate-core/internal/billing"
)

// PluginInstance 运行中的插件实例
type PluginInstance struct {
	Name        string
	SourceName  string
	BinaryDir   string
	DisplayName string
	Version     string
	Author      string
	Platform    string
	Type        string // "gateway", "extension"
	Client      *goplugin.Client
	Gateway     *sdkgrpc.GatewayGRPCClient
	Extension   *sdkgrpc.ExtensionGRPCClient
}

// Manager 插件管理器
// 负责插件的安装、卸载、启停、进程管理和 gRPC 通信
type Manager struct {
	pluginDir string                // 插件二进制目录
	logLevel  string                // 日志级别，传给插件子进程
	priceMgr  *billing.PriceManager // 价格管理器，插件启动时自动同步模型价格

	mu        sync.RWMutex
	instances map[string]*PluginInstance // pluginName → 运行实例
	aliases   map[string]string          // alias/sourceName → canonical pluginName
	devPaths  map[string]string          // pluginName → 源码目录（仅 dev 模式插件）

	// 缓存：插件启动时从 gRPC 获取并缓存
	modelCache        map[string][]sdk.ModelInfo       // platform → models
	routeCache        map[string][]sdk.RouteDefinition // pluginName → routes
	credCache         map[string][]sdk.CredentialField // platform → credential fields（兼容旧接口）
	accountTypeCache  map[string][]sdk.AccountType     // platform → account types
	frontendPageCache map[string][]sdk.FrontendPage    // pluginName → 前端页面声明
}

// NewManager 创建插件管理器
func NewManager(pluginDir, logLevel string, priceMgr *billing.PriceManager) *Manager {
	return &Manager{
		pluginDir:         pluginDir,
		logLevel:          logLevel,
		priceMgr:          priceMgr,
		instances:         make(map[string]*PluginInstance),
		aliases:           make(map[string]string),
		devPaths:          make(map[string]string),
		modelCache:        make(map[string][]sdk.ModelInfo),
		routeCache:        make(map[string][]sdk.RouteDefinition),
		credCache:         make(map[string][]sdk.CredentialField),
		accountTypeCache:  make(map[string][]sdk.AccountType),
		frontendPageCache: make(map[string][]sdk.FrontendPage),
	}
}

// LoadAll 启动时扫描插件目录，发现可执行二进制则直接加载
// 目录即状态：目录存在就加载，目录不存在就不加载
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

		// 检查二进制是否存在
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

// LoadDev 加载开发模式插件（通过 go run 启动源码）
func (m *Manager) LoadDev(ctx context.Context, name, srcPath string) error {
	// 验证源码目录存在
	if _, err := os.Stat(srcPath); err != nil {
		return fmt.Errorf("插件源码目录不存在: %s", srcPath)
	}

	requestedName := normalizePluginName(name)
	if requestedName == "" {
		// name 未配置时用项目目录名作为临时标识，启动后由插件 Info().ID 覆盖
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

	// 记录源码路径，用于热加载
	m.mu.Lock()
	m.devPaths[canonicalName] = srcPath
	m.registerAliasesLocked(canonicalName, requestedName)
	m.mu.Unlock()

	slog.Info("开发插件加载成功", "name", canonicalName, "requested_name", requestedName, "src", srcPath)
	return nil
}

// ReloadDev 热加载开发模式插件：停止旧进程，重新 go run 启动
func (m *Manager) ReloadDev(ctx context.Context, name string) error {
	m.mu.RLock()
	resolvedName := m.resolveNameLocked(name)
	srcPath, isDev := m.devPaths[resolvedName]
	m.mu.RUnlock()

	if !isDev {
		return fmt.Errorf("插件 %s 不是开发模式插件，无法热加载", name)
	}

	slog.Info("正在热加载开发插件", "name", resolvedName, "src", srcPath)

	// 停止旧进程
	m.stopPlugin(resolvedName)

	// 重新启动
	return m.LoadDev(ctx, resolvedName, srcPath)
}

// IsDev 检查插件是否为开发模式
func (m *Manager) IsDev(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.devPaths[m.resolveNameLocked(name)]
	return ok
}

// startPlugin 启动插件子进程并建立 gRPC 连接
// 支持 gateway 和 extension 两种插件类型：先尝试 gateway，失败则尝试 extension
func (m *Manager) startPlugin(ctx context.Context, requestedName string, cmd *exec.Cmd, binaryDir string) (string, error) {
	// 创建 go-plugin 客户端（SyncStdout/Stderr 转发子进程日志到主进程）
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

	// 建立 RPC 连接
	rpcClient, err := client.Client()
	if err != nil {
		client.Kill()
		return "", fmt.Errorf("连接插件进程失败: %w", err)
	}

	// 先通过 PluginService.GetInfo() 获取插件类型，再 Dispense 对应接口
	// 注意：go-plugin 的 Dispense 总是成功（只创建 stub），不能靠它区分类型
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

	// 通过 Info().Type 判断真实插件类型
	info := probe.Info()
	if info.Type == sdk.PluginTypeExtension {
		// 是 extension 插件，用 extension 接口重新 Dispense
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

	// 默认作为 gateway 插件处理
	return m.startGatewayPlugin(ctx, client, probe, requestedName, binaryDir)
}

// startGatewayPlugin 初始化并启动 gateway 类型插件
func (m *Manager) startGatewayPlugin(ctx context.Context, client *goplugin.Client, gateway *sdkgrpc.GatewayGRPCClient, requestedName, binaryDir string) (string, error) {
	info := gateway.Info()
	canonicalName := canonicalPluginName(info, requestedName)
	if canonicalName == "" {
		client.Kill()
		return "", fmt.Errorf("插件未提供有效的 ID/name")
	}

	// 初始化插件（注入 log_level 让插件子进程应用 Core 的日志级别）
	initConfig := map[string]interface{}{
		sdk.ConfigKeyLogLevel: m.logLevel,
	}
	pluginCtx := newCorePluginContext(initConfig, canonicalName)
	if err := gateway.Init(pluginCtx); err != nil {
		client.Kill()
		return "", fmt.Errorf("初始化插件失败: %w", err)
	}

	// 启动插件
	if err := gateway.Start(ctx); err != nil {
		client.Kill()
		return "", fmt.Errorf("启动插件失败: %w", err)
	}

	// 获取插件元信息并缓存
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

	// 同步模型价格到 PriceManager（插件声明的价格为每百万 token，转换为每 token）
	if m.priceMgr != nil {
		for _, model := range models {
			m.priceMgr.SetPrice(platform, model.ID, billing.ModelPrice{
				InputPerToken:  model.InputPrice / 1_000_000,
				OutputPerToken: model.OutputPrice / 1_000_000,
				CachePerToken:  model.CachePrice / 1_000_000,
			})
		}
	}

	m.mu.Lock()
	m.instances[canonicalName] = instance
	m.registerAliasesLocked(canonicalName, requestedName, binaryDir)
	m.modelCache[platform] = models
	m.routeCache[canonicalName] = routes
	if len(info.AccountTypes) > 0 {
		m.credCache[platform] = info.AccountTypes[0].Fields
	} else {
		delete(m.credCache, platform)
	}
	m.accountTypeCache[platform] = info.AccountTypes
	if len(info.FrontendPages) > 0 {
		m.frontendPageCache[canonicalName] = info.FrontendPages
	}
	m.mu.Unlock()

	// 提取前端静态资源
	m.extractPluginWebAssets(canonicalName, gateway)

	if normalizePluginName(requestedName) != "" && canonicalName != normalizePluginName(requestedName) {
		slog.Info("插件名称已统一到 Info().ID", "requested_name", requestedName, "canonical_name", canonicalName)
	}

	return canonicalName, nil
}

// startExtensionPlugin 初始化并启动 extension 类型插件
func (m *Manager) startExtensionPlugin(ctx context.Context, client *goplugin.Client, ext *sdkgrpc.ExtensionGRPCClient, requestedName, binaryDir string) (string, error) {
	info := ext.Info()
	canonicalName := canonicalPluginName(info, requestedName)
	if canonicalName == "" {
		client.Kill()
		return "", fmt.Errorf("插件未提供有效的 ID/name")
	}

	// 初始化插件（注入 log_level 让插件子进程应用 Core 的日志级别）
	initConfig := map[string]interface{}{
		sdk.ConfigKeyLogLevel: m.logLevel,
	}
	pluginCtx := newCorePluginContext(initConfig, canonicalName)
	if err := ext.Init(pluginCtx); err != nil {
		client.Kill()
		return "", fmt.Errorf("初始化 extension 插件失败: %w", err)
	}

	// 启动插件
	if err := ext.Start(ctx); err != nil {
		client.Kill()
		return "", fmt.Errorf("启动 extension 插件失败: %w", err)
	}

	// 执行数据库迁移
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
		m.frontendPageCache[canonicalName] = info.FrontendPages
	}
	m.mu.Unlock()

	// 提取前端静态资源
	m.extractPluginWebAssets(canonicalName, ext)

	if normalizePluginName(requestedName) != "" && canonicalName != normalizePluginName(requestedName) {
		slog.Info("插件名称已统一到 Info().ID", "requested_name", requestedName, "canonical_name", canonicalName)
	}

	return canonicalName, nil
}

// webAssetsProvider 获取前端资源的接口（gateway 和 extension 都实现了）
type webAssetsProvider interface {
	GetWebAssets() (map[string][]byte, error)
}

// extractPluginWebAssets 提取插件的前端静态资源到磁盘
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

// stopPlugin 停止插件进程
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

// StopAll 停止所有插件
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

// Uninstall 卸载插件：停止进程并删除目录
func (m *Manager) Uninstall(ctx context.Context, name string) error {
	resolvedName := m.resolveName(name)
	inst := m.GetInstance(resolvedName)

	m.stopPlugin(resolvedName)

	m.mu.Lock()
	delete(m.devPaths, resolvedName)
	m.mu.Unlock()

	targetDirs := []string{filepath.Join(m.pluginDir, resolvedName)}
	if inst != nil && inst.BinaryDir != "" && inst.BinaryDir != resolvedName {
		targetDirs = append(targetDirs, filepath.Join(m.pluginDir, inst.BinaryDir))
	}

	for _, targetDir := range targetDirs {
		if err := os.RemoveAll(targetDir); err != nil {
			return fmt.Errorf("删除插件目录失败: %w", err)
		}
	}

	slog.Info("插件已卸载", "name", resolvedName)
	return nil
}

// InstallFromBinary 从二进制数据安装插件
// 写入文件、探测真实插件名、自动启动
func (m *Manager) InstallFromBinary(ctx context.Context, name string, binary []byte) error {
	// 先写入临时目录，探测插件真实名称
	realName, err := m.probePluginName(name, binary)
	if err != nil {
		slog.Warn("探测插件名称失败，使用传入名称", "name", name, "error", err)
		realName = name
	}

	// 停止旧实例（如果有）
	m.stopPlugin(realName)

	// 写入最终目录
	targetDir := filepath.Join(m.pluginDir, realName)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("创建插件目录失败: %w", err)
	}
	binaryPath := filepath.Join(targetDir, realName)
	if err := os.WriteFile(binaryPath, binary, 0755); err != nil {
		return fmt.Errorf("写入插件二进制失败: %w", err)
	}

	// 启动插件
	canonicalName, err := m.startPlugin(ctx, realName, exec.Command(binaryPath), realName)
	if err != nil {
		return fmt.Errorf("启动插件失败: %w", err)
	}

	slog.Info("插件从二进制安装成功", "name", canonicalName)
	return nil
}

// probePluginName 临时启动插件二进制，通过 gRPC 获取真实插件 ID
func (m *Manager) probePluginName(fallbackName string, binary []byte) (string, error) {
	tmpDir, err := os.MkdirTemp("", "airgate-probe-*")
	if err != nil {
		return "", fmt.Errorf("创建临时目录失败: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			slog.Warn("清理插件探测临时目录失败", "dir", tmpDir, "error", err)
		}
	}()

	tmpBinary := filepath.Join(tmpDir, fallbackName)
	if err := os.WriteFile(tmpBinary, binary, 0755); err != nil {
		return "", fmt.Errorf("写入临时二进制失败: %w", err)
	}

	client := goplugin.NewClient(&goplugin.ClientConfig{
		HandshakeConfig: shared.Handshake,
		Plugins: goplugin.PluginSet{
			shared.PluginKeyGateway:   &sdkgrpc.GatewayGRPCPlugin{},
			shared.PluginKeyExtension: &sdkgrpc.ExtensionGRPCPlugin{},
		},
		Cmd:              exec.Command(tmpBinary),
		AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolGRPC},
	})
	defer client.Kill()

	rpcClient, err := client.Client()
	if err != nil {
		return "", fmt.Errorf("连接探测进程失败: %w", err)
	}

	// 通过 gateway 接口的 Info().Type 探测插件类型（与 startPlugin 逻辑一致）
	raw, err := rpcClient.Dispense(shared.PluginKeyGateway)
	if err != nil {
		return "", fmt.Errorf("获取探测接口失败: %w", err)
	}
	probe, ok := raw.(*sdkgrpc.GatewayGRPCClient)
	if !ok {
		return "", fmt.Errorf("探测类型断言失败")
	}

	info := probe.Info()
	if info.Type == sdk.PluginTypeExtension {
		// 是 extension 插件，用 extension 接口获取 ID
		extRaw, err := rpcClient.Dispense(shared.PluginKeyExtension)
		if err != nil {
			return "", fmt.Errorf("获取 extension 探测接口失败: %w", err)
		}
		if ext, ok := extRaw.(*sdkgrpc.ExtensionGRPCClient); ok {
			if extInfo := ext.Info(); extInfo.ID != "" {
				return extInfo.ID, nil
			}
		}
		return fallbackName, nil
	}

	// 默认作为 gateway 处理
	if info.ID != "" {
		return info.ID, nil
	}
	return fallbackName, nil
}

// InstallFromGithub 从 GitHub Release 下载并安装插件
func (m *Manager) InstallFromGithub(ctx context.Context, repo string) error {
	owner, repoName, err := parseGithubRepo(repo)
	if err != nil {
		return err
	}

	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repoName)
	req, _ := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("请求 GitHub API 失败: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Warn("关闭 GitHub API 响应失败", "repo", repo, "error", err)
		}
	}()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("仓库 %s/%s 不存在或没有 Release", owner, repoName)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub API 返回状态码 %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return fmt.Errorf("解析 Release 数据失败: %w", err)
	}

	targetOS := runtime.GOOS
	targetArch := runtime.GOARCH
	var downloadURL string
	for _, asset := range release.Assets {
		name := strings.ToLower(asset.Name)
		if strings.Contains(name, targetOS) && strings.Contains(name, targetArch) {
			downloadURL = asset.BrowserDownloadURL
			break
		}
	}
	if downloadURL == "" {
		return fmt.Errorf("未找到适配 %s/%s 的二进制文件，Release: %s", targetOS, targetArch, release.TagName)
	}

	dlReq, _ := http.NewRequestWithContext(ctx, "GET", downloadURL, nil)
	dlResp, err := http.DefaultClient.Do(dlReq)
	if err != nil {
		return fmt.Errorf("下载插件失败: %w", err)
	}
	defer func() {
		if err := dlResp.Body.Close(); err != nil {
			slog.Warn("关闭插件下载响应失败", "url", downloadURL, "error", err)
		}
	}()

	if dlResp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载返回状态码 %d", dlResp.StatusCode)
	}

	binary, err := io.ReadAll(dlResp.Body)
	if err != nil {
		return fmt.Errorf("读取下载内容失败: %w", err)
	}

	return m.InstallFromBinary(ctx, repoName, binary)
}

// githubRelease GitHub Release API 响应
type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

// githubAsset GitHub Release Asset
type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// parseGithubRepo 解析 GitHub 仓库地址
// 支持格式：owner/repo 或 https://github.com/owner/repo
func parseGithubRepo(repo string) (owner, name string, err error) {
	repo = strings.TrimSuffix(strings.TrimSpace(repo), "/")
	repo = strings.TrimSuffix(repo, ".git")

	if strings.Contains(repo, "github.com") {
		parts := strings.Split(repo, "github.com/")
		if len(parts) != 2 {
			return "", "", fmt.Errorf("无效的 GitHub 地址: %s", repo)
		}
		repo = parts[1]
	}

	segments := strings.Split(repo, "/")
	if len(segments) != 2 || segments[0] == "" || segments[1] == "" {
		return "", "", fmt.Errorf("无效的仓库格式，请使用 owner/repo 格式")
	}
	return segments[0], segments[1], nil
}

// GetExtensionByName 根据插件名查找 extension 类型插件
func (m *Manager) GetExtensionByName(name string) *sdkgrpc.ExtensionGRPCClient {
	m.mu.RLock()
	defer m.mu.RUnlock()
	inst := m.instances[m.resolveNameLocked(name)]
	if inst == nil {
		return nil
	}
	return inst.Extension
}

// GetPluginByPlatform 根据平台查找运行中的插件实例
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

// GetInstance 获取插件实例
func (m *Manager) GetInstance(name string) *PluginInstance {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.instances[m.resolveNameLocked(name)]
}

// GetCredentialFields 获取指定平台的凭证字段声明
func (m *Manager) GetCredentialFields(platform string) []sdk.CredentialField {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.credCache[platform]
}

// GetAccountTypes 获取指定平台的账号类型声明
func (m *Manager) GetAccountTypes(platform string) []sdk.AccountType {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.accountTypeCache[platform]
}

// GetModels 获取指定平台的模型列表
func (m *Manager) GetModels(platform string) []sdk.ModelInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.modelCache[platform]
}

// GetRoutes 获取指定插件的路由声明
func (m *Manager) GetRoutes(pluginName string) []sdk.RouteDefinition {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.routeCache[m.resolveNameLocked(pluginName)]
}

// GetAllRoutes 获取所有运行中插件的路由
func (m *Manager) GetAllRoutes() map[string][]sdk.RouteDefinition {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string][]sdk.RouteDefinition, len(m.routeCache))
	for k, v := range m.routeCache {
		result[k] = v
	}
	return result
}

// MatchPluginByRoute 根据请求方法和路径匹配插件
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

// MatchPluginByPathPrefix 根据路径前缀匹配插件
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
// 当多个插件声明了相同路由时，优先使用 API Key 绑定分组的平台。
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

// IsRunning 检查插件是否正在运行
func (m *Manager) IsRunning(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.instances[m.resolveNameLocked(name)]
	return ok
}

// RunningCount 获取运行中的插件数量
func (m *Manager) RunningCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.instances)
}

// GetFrontendPages 获取插件声明的前端页面
func (m *Manager) GetFrontendPages(pluginName string) []sdk.FrontendPage {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.frontendPageCache[m.resolveNameLocked(pluginName)]
}

// PluginMeta 插件运行时元信息
type PluginMeta struct {
	Name          string
	DisplayName   string
	Version       string
	Author        string
	Type          string
	Platform      string
	AccountTypes  []sdk.AccountType
	FrontendPages []sdk.FrontendPage
	HasWebAssets  bool
	IsDev         bool
}

// GetAllPluginMeta 获取所有运行中插件的元信息
func (m *Manager) GetAllPluginMeta() []PluginMeta {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var metas []PluginMeta
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
			meta.AccountTypes = types
		}
		if pages, ok := m.frontendPageCache[inst.Name]; ok {
			meta.FrontendPages = pages
		}
		assetsDir := filepath.Join(m.pluginDir, inst.Name, "assets")
		if _, err := os.Stat(assetsDir); err == nil {
			meta.HasWebAssets = true
		}
		metas = append(metas, meta)
	}
	return metas
}

// HasWebAssets 检查插件是否有前端资源
func (m *Manager) HasWebAssets(pluginName string) bool {
	assetsDir := filepath.Join(m.pluginDir, m.resolveName(pluginName), "assets")
	_, err := os.Stat(assetsDir)
	return err == nil
}

func normalizePluginName(name string) string {
	return strings.TrimSpace(name)
}

func canonicalPluginName(info sdk.PluginInfo, fallback string) string {
	if id := normalizePluginName(info.ID); id != "" {
		return id
	}
	return normalizePluginName(fallback)
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

// extractWebAssets 将前端资源写入指定目录
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
