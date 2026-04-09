package plugin

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	sdk "github.com/DouDOU-start/airgate-sdk"
	sdkgrpc "github.com/DouDOU-start/airgate-sdk/grpc"

	"github.com/DouDOU-start/airgate-core/ent"
	pluginent "github.com/DouDOU-start/airgate-core/ent/plugin"
	settingent "github.com/DouDOU-start/airgate-core/ent/setting"
	"github.com/DouDOU-start/airgate-core/internal/auth"
)

// pluginGRPCMaxMessageBytes 是与插件之间 gRPC 单条消息的最大字节数（收/发同值）。
// 默认值 4 MB 经常被大段 LLM 响应或翻译后的 SSE 事件击穿，统一抬到 64 MB。
const pluginGRPCMaxMessageBytes = 64 * 1024 * 1024

// newPluginClientConfig 构造与插件子进程通信的 go-plugin ClientConfig。
//
// forwardOutput=true 时把插件的 stdout/stderr 透传到 core 自身（用于正常运行的插件），
// false 时丢弃（用于一次性的探测客户端，避免污染日志）。
//
// 抽出这个 helper 是为了让 manager_install.go / manager_runtime.go 共用同一份握手 +
// gRPC 上限配置，避免改一处忘另一处。
func newPluginClientConfig(cmd *exec.Cmd, forwardOutput bool) *goplugin.ClientConfig {
	cfg := &goplugin.ClientConfig{
		HandshakeConfig: sdkgrpc.Handshake,
		Plugins: goplugin.PluginSet{
			sdkgrpc.PluginKeyGateway:   &sdkgrpc.GatewayGRPCPlugin{},
			sdkgrpc.PluginKeyExtension: &sdkgrpc.ExtensionGRPCPlugin{},
		},
		Cmd:              cmd,
		AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolGRPC},
		GRPCDialOptions: []grpc.DialOption{
			grpc.WithDefaultCallOptions(
				grpc.MaxCallRecvMsgSize(pluginGRPCMaxMessageBytes),
				grpc.MaxCallSendMsgSize(pluginGRPCMaxMessageBytes),
			),
		},
	}
	if forwardOutput {
		cfg.SyncStdout = os.Stdout
		cfg.SyncStderr = os.Stderr
	}
	return cfg
}

// buildInitConfig 构造传递给插件 Init() 的配置 map。
//
// 内容来源（优先级从低到高）：
//  1. 系统自动注入（管理员不必填、也不允许覆盖）：
//     - sdk.ConfigKeyLogLevel  来自 core 配置 log.level
//     - db_dsn                 来自 core 配置 database 节，让插件复用 core 数据库
//     - core_base_url          core 自身 HTTP 监听根地址，供插件回调 core admin API
//     - admin_api_key          从 settings 表读 admin_api_key_encrypted 解密而来
//     （未生成时不注入，插件以软失败方式运行）
//  2. 用户配置：DB ent.Plugin.Config (JSONB) — 由管理员通过 UI 写入
//
// 用户配置不允许覆盖系统字段（防止管理员误填把 db_dsn 改成不可用的串）。
// 当 DB 中没有该插件记录或 config 为空时，仅返回系统字段。
// 这里刻意不报错：缺配置只是让插件以"未配置态"加载，UI 仍然可见。
func (m *Manager) buildInitConfig(ctx context.Context, name string) map[string]interface{} {
	cfg := map[string]interface{}{
		sdk.ConfigKeyLogLevel: m.logLevel,
	}
	if m.coreDSN != "" {
		cfg["db_dsn"] = m.coreDSN
	}
	if m.coreBaseURL != "" {
		cfg["core_base_url"] = m.coreBaseURL
	}
	if key := m.lookupAdminAPIKey(ctx); key != "" {
		cfg["admin_api_key"] = key
	}
	if m.db == nil {
		return cfg
	}
	row, err := m.db.Plugin.Query().Where(pluginent.NameEQ(name)).Only(ctx)
	if err != nil {
		// not found 是常态，不打 warn
		if !ent.IsNotFound(err) {
			slog.Warn("读取插件 DB 配置失败", "plugin", name, "error", err)
		}
		return cfg
	}
	for k, v := range row.Config {
		if _, exists := cfg[k]; exists {
			continue // 系统字段不被用户覆盖
		}
		cfg[k] = v
	}
	return cfg
}

// lookupAdminAPIKey 从 settings 表读取 admin_api_key_encrypted 并解密为明文。
//
// 返回空串的常见情况：
//   - 管理员还没在「系统设置 → 安全与认证」生成过 admin key（settings 表里没行）
//   - apiKeySecret 与加密时不一致（用户中途改了 API_KEY_SECRET）
//
// 任何失败都只 warn，不返回 error —— 调用方据此决定不注入 admin_api_key 字段，
// 让插件以软失败方式继续运行（airgate-health 在 admin_api_key 缺失时不启 prober，
// 但插件仍然可见、仍能展示历史数据）。
func (m *Manager) lookupAdminAPIKey(ctx context.Context) string {
	if m.db == nil || m.apiKeySecret == "" {
		return ""
	}
	const (
		settingKey   = "admin_api_key_encrypted"
		settingGroup = "security"
	)
	row, err := m.db.Setting.Query().
		Where(settingent.KeyEQ(settingKey), settingent.GroupEQ(settingGroup)).
		Only(ctx)
	if err != nil {
		if !ent.IsNotFound(err) {
			slog.Warn("查询 admin_api_key_encrypted 失败", "error", err)
		}
		return ""
	}
	if row.Value == "" {
		return ""
	}
	plain, err := auth.DecryptAPIKey(row.Value, m.apiKeySecret)
	if err != nil {
		slog.Warn("解密 admin_api_key 失败，将不注入到插件",
			"error", err,
			"hint", "API_KEY_SECRET 可能跟生成 admin key 时不一致")
		return ""
	}
	return plain
}

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
	client := goplugin.NewClient(newPluginClientConfig(cmd, true))

	rpcClient, err := client.Client()
	if err != nil {
		client.Kill()
		return "", fmt.Errorf("连接插件进程失败: %w", err)
	}

	raw, err := rpcClient.Dispense(sdkgrpc.PluginKeyGateway)
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
		extRaw, err := rpcClient.Dispense(sdkgrpc.PluginKeyExtension)
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

	initConfig := m.buildInitConfig(ctx, canonicalName)
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
		Name:               canonicalName,
		SourceName:         normalizePluginName(requestedName),
		BinaryDir:          normalizePluginName(binaryDir),
		DisplayName:        info.Name,
		Version:            info.Version,
		Author:             info.Author,
		Platform:           platform,
		Type:               pluginType,
		InstructionPresets: info.InstructionPresets,
		ConfigSchema:       cloneConfigSchema(info.ConfigSchema),
		Client:             client,
		Gateway:            gateway,
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

	initConfig := m.buildInitConfig(ctx, canonicalName)
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
		Name:         canonicalName,
		SourceName:   normalizePluginName(requestedName),
		BinaryDir:    normalizePluginName(binaryDir),
		DisplayName:  info.Name,
		Version:      info.Version,
		Author:       info.Author,
		Type:         pluginType,
		ConfigSchema: cloneConfigSchema(info.ConfigSchema),
		Client:       client,
		Extension:    ext,
	}

	m.mu.Lock()
	m.instances[canonicalName] = instance
	m.registerAliasesLocked(canonicalName, requestedName, binaryDir)
	if len(info.FrontendPages) > 0 {
		m.frontendPageCache[canonicalName] = cloneFrontendPages(info.FrontendPages)
	}
	m.mu.Unlock()

	m.extractPluginWebAssets(canonicalName, ext)

	// 启动插件声明的后台任务调度（如 epay 的 expire_pending_orders）。
	// 必须在 instance 已写入 m.instances 之后，因为 stopPlugin 通过 instance 取消。
	m.startExtensionBackgroundTasks(instance)

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

	// 先停后台任务调度器，再走插件 Stop —— 避免 ticker 在 plugin 进程被 Kill
	// 之后还往 dead client 发 RPC，造成一堆 connection refused 噪音。
	if inst.stopBackground != nil {
		inst.stopBackground()
	}

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
