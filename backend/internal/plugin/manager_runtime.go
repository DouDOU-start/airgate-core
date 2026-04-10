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
	pb "github.com/DouDOU-start/airgate-sdk/proto"

	"github.com/DouDOU-start/airgate-core/ent"
	pluginent "github.com/DouDOU-start/airgate-core/ent/plugin"
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
//
// hostHandle 参数：
//   - 非 nil 时作为本次 spawn 的 HostService 实现，注册到所有 PluginType 的 GRPCPlugin
//     的 HostImpl 字段；spawn 后 manager 会调 hostHandle.SetCapabilities 写入权限
//   - nil 时（探测式 spawn / 没装 host service 的部署）走软失败路径，插件 ctx.Host()==nil
func (m *Manager) newPluginClientConfig(cmd *exec.Cmd, forwardOutput bool, hostHandle *pluginHostHandle) *goplugin.ClientConfig {
	// hostHandle 通过 PluginSet 注入到 GatewayGRPCPlugin / ExtensionGRPCPlugin / MiddlewareGRPCPlugin。
	// 插件 Dispense 时，sdk-grpc 的 GRPCClient 钩子会通过 GRPCBroker 启一条
	// 反向 stream 注册 HostService，把 stream id 通过 InitRequest.host_broker_id
	// 透传到插件子进程。hostHandle 为 nil 时，stream 不创建，插件以软失败方式运行。
	//
	// 注意：MiddlewareGRPCPlugin 也注册到 PluginSet，让 middleware 类型插件可以被
	// dispense（与 gateway/extension 平行）。
	var hostImpl pb.HostServiceServer
	if hostHandle != nil {
		hostImpl = hostHandle
	}
	cfg := &goplugin.ClientConfig{
		HandshakeConfig: sdkgrpc.Handshake,
		Plugins: goplugin.PluginSet{
			sdkgrpc.PluginKeyGateway:    &sdkgrpc.GatewayGRPCPlugin{HostImpl: hostImpl},
			sdkgrpc.PluginKeyExtension:  &sdkgrpc.ExtensionGRPCPlugin{HostImpl: hostImpl},
			sdkgrpc.PluginKeyMiddleware: &sdkgrpc.MiddlewareGRPCPlugin{HostImpl: hostImpl},
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
//     - db_dsn                 admin DSN（可访问 core 业务表）—— 兼容老插件，
//     将来下线（详见 ADR-0001 Decision 5 迁移路径）
//     - plugin_dsn             受限 DSN（只能访问 plugin_<id> schema），新插件首选
//  2. 用户配置：DB ent.Plugin.Config (JSONB) — 由管理员通过 UI 写入
//
// 用户配置不允许覆盖系统字段（防止管理员误填把 db_dsn 改成不可用的串）。
// 当 DB 中没有该插件记录或 config 为空时，仅返回系统字段。
// 这里刻意不报错：缺配置只是让插件以"未配置态"加载，UI 仍然可见。
//
// Step 2 变更：下线 core_base_url / admin_api_key 注入。插件要调 core 能力一律走
// HostService（hashicorp/go-plugin GRPCBroker 反向 gRPC），不再需要 HTTP + Bearer。
//
// Step 3 变更：新增 plugin_dsn（受限 DSN + 独立 schema）。db_dsn 暂时保留以兼容
// 尚未迁移的 epay/health/openai 插件，等它们逐个迁过来后再下线 db_dsn。
func (m *Manager) buildInitConfig(ctx context.Context, name string) map[string]interface{} {
	cfg := map[string]interface{}{
		sdk.ConfigKeyLogLevel: m.logLevel,
	}
	if m.coreDSN != "" {
		cfg["db_dsn"] = m.coreDSN
	}
	// 给插件准备一个独立 schema + 受限 role，注入 plugin_dsn
	if m.pluginDB != nil {
		if pluginDSN, err := m.pluginDB.EnsureFor(ctx, name); err != nil {
			slog.Warn("provision plugin DB 失败，plugin_dsn 将不可用",
				"plugin", name, "error", err)
		} else {
			cfg["plugin_dsn"] = pluginDSN
		}
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
	// 在 spawn 之前先用 requestedName 占位创建 host handle。
	// canonical name 可能在 Info() 之后才确定；spawn 完成后会用 canonicalName 重新注册 handle。
	hostHandle := m.prepareHostHandle(requestedName)
	client := goplugin.NewClient(m.newPluginClientConfig(cmd, true, hostHandle))

	rpcClient, err := client.Client()
	if err != nil {
		client.Kill()
		m.removeHostHandle(requestedName)
		return "", fmt.Errorf("连接插件进程失败: %w", err)
	}

	raw, err := rpcClient.Dispense(sdkgrpc.PluginKeyGateway)
	if err != nil {
		client.Kill()
		m.removeHostHandle(requestedName)
		return "", fmt.Errorf("获取插件接口失败: %w", err)
	}
	probe, ok := raw.(*sdkgrpc.GatewayGRPCClient)
	if !ok {
		client.Kill()
		m.removeHostHandle(requestedName)
		return "", fmt.Errorf("插件类型断言失败")
	}

	info := probe.Info()
	switch info.Type {
	case sdk.PluginTypeExtension:
		extRaw, err := rpcClient.Dispense(sdkgrpc.PluginKeyExtension)
		if err != nil {
			client.Kill()
			m.removeHostHandle(requestedName)
			return "", fmt.Errorf("获取 extension 插件接口失败: %w", err)
		}
		ext, ok := extRaw.(*sdkgrpc.ExtensionGRPCClient)
		if !ok {
			client.Kill()
			m.removeHostHandle(requestedName)
			return "", fmt.Errorf("extension 插件类型断言失败")
		}
		return m.startExtensionPlugin(ctx, client, ext, requestedName, binaryDir)

	case sdk.PluginTypeMiddleware:
		mwRaw, err := rpcClient.Dispense(sdkgrpc.PluginKeyMiddleware)
		if err != nil {
			client.Kill()
			m.removeHostHandle(requestedName)
			return "", fmt.Errorf("获取 middleware 插件接口失败: %w", err)
		}
		mw, ok := mwRaw.(*sdkgrpc.MiddlewareGRPCClient)
		if !ok {
			client.Kill()
			m.removeHostHandle(requestedName)
			return "", fmt.Errorf("middleware 插件类型断言失败")
		}
		return m.startMiddlewarePlugin(ctx, client, mw, requestedName, binaryDir)

	default:
		// 默认按 gateway 处理（包括 info.Type == "" 的极老插件）
		return m.startGatewayPlugin(ctx, client, probe, requestedName, binaryDir)
	}
}

func (m *Manager) startGatewayPlugin(ctx context.Context, client *goplugin.Client, gateway *sdkgrpc.GatewayGRPCClient, requestedName, binaryDir string) (string, error) {
	info := gateway.Info()
	canonicalName := canonicalPluginName(info, requestedName)
	if canonicalName == "" {
		client.Kill()
		m.removeHostHandle(requestedName)
		return "", fmt.Errorf("插件未提供有效的 ID/name")
	}

	// canonicalName 可能与 requestedName 不同，把 host handle 从临时占位 key 改名到正式 key
	m.relocateHostHandle(requestedName, canonicalName)
	// 解析插件声明的 capability set，写入 handle。之后插件调任何 RPC 都会被校验。
	m.finalizeHostHandle(canonicalName, info)

	initConfig := m.buildInitConfig(ctx, canonicalName)
	pluginCtx := newCorePluginContext(initConfig, canonicalName)
	if err := gateway.Init(pluginCtx); err != nil {
		client.Kill()
		m.removeHostHandle(canonicalName)
		return "", fmt.Errorf("初始化插件失败: %w", err)
	}
	if err := gateway.Start(ctx); err != nil {
		client.Kill()
		m.removeHostHandle(canonicalName)
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
		Capabilities:       append([]string(nil), info.Capabilities...),
		Priority:           info.Priority,
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
	// 注意：不能用 `if len > 0` 守卫，必须无条件 delete + set。否则插件从"有 frontend pages"
	// 改成"无"后，旧的 cache 永远不会被清掉（airgate-health 删 admin tab 时踩到过这个坑）。
	if len(info.FrontendPages) > 0 {
		m.frontendPageCache[canonicalName] = cloneFrontendPages(info.FrontendPages)
	} else {
		delete(m.frontendPageCache, canonicalName)
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
		m.removeHostHandle(requestedName)
		return "", fmt.Errorf("插件未提供有效的 ID/name")
	}

	m.relocateHostHandle(requestedName, canonicalName)
	m.finalizeHostHandle(canonicalName, info)

	initConfig := m.buildInitConfig(ctx, canonicalName)
	pluginCtx := newCorePluginContext(initConfig, canonicalName)
	if err := ext.Init(pluginCtx); err != nil {
		client.Kill()
		m.removeHostHandle(canonicalName)
		return "", fmt.Errorf("初始化 extension 插件失败: %w", err)
	}
	if err := ext.Start(ctx); err != nil {
		client.Kill()
		m.removeHostHandle(canonicalName)
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
		Capabilities: append([]string(nil), info.Capabilities...),
		Priority:     info.Priority,
		Client:       client,
		Extension:    ext,
	}

	m.mu.Lock()
	m.instances[canonicalName] = instance
	m.registerAliasesLocked(canonicalName, requestedName, binaryDir)
	// 必须无条件 delete + set，避免插件移除 frontend pages 后旧 cache 残留。
	if len(info.FrontendPages) > 0 {
		m.frontendPageCache[canonicalName] = cloneFrontendPages(info.FrontendPages)
	} else {
		delete(m.frontendPageCache, canonicalName)
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

// startMiddlewarePlugin 处理 type=middleware 的插件。
//
// 与 extension 的区别：
//   - 不暴露自定义 HTTP 路由（middleware 完全围绕 forward chain 工作）
//   - 不声明 BackgroundTask
//   - 实例存到 m.instances 后会被 forwarder 的 middleware chain 自动 pickup
//
// 与 gateway 的区别：
//   - 不替代 upstream（不需要 Platform / Models / Routes）
func (m *Manager) startMiddlewarePlugin(ctx context.Context, client *goplugin.Client, mw *sdkgrpc.MiddlewareGRPCClient, requestedName, binaryDir string) (string, error) {
	info := mw.Info()
	canonicalName := canonicalPluginName(info, requestedName)
	if canonicalName == "" {
		client.Kill()
		m.removeHostHandle(requestedName)
		return "", fmt.Errorf("插件未提供有效的 ID/name")
	}

	m.relocateHostHandle(requestedName, canonicalName)
	m.finalizeHostHandle(canonicalName, info)

	initConfig := m.buildInitConfig(ctx, canonicalName)
	pluginCtx := newCorePluginContext(initConfig, canonicalName)
	if err := mw.Init(pluginCtx); err != nil {
		client.Kill()
		m.removeHostHandle(canonicalName)
		return "", fmt.Errorf("初始化 middleware 插件失败: %w", err)
	}
	if err := mw.Start(ctx); err != nil {
		client.Kill()
		m.removeHostHandle(canonicalName)
		return "", fmt.Errorf("启动 middleware 插件失败: %w", err)
	}

	instance := &PluginInstance{
		Name:         canonicalName,
		SourceName:   normalizePluginName(requestedName),
		BinaryDir:    normalizePluginName(binaryDir),
		DisplayName:  info.Name,
		Version:      info.Version,
		Author:       info.Author,
		Type:         string(sdk.PluginTypeMiddleware),
		ConfigSchema: cloneConfigSchema(info.ConfigSchema),
		Capabilities: append([]string(nil), info.Capabilities...),
		Priority:     info.Priority,
		Client:       client,
		Middleware:   mw,
	}

	m.mu.Lock()
	m.instances[canonicalName] = instance
	m.registerAliasesLocked(canonicalName, requestedName, binaryDir)
	// 必须无条件 delete + set，避免插件移除 frontend pages 后旧 cache 残留。
	if len(info.FrontendPages) > 0 {
		m.frontendPageCache[canonicalName] = cloneFrontendPages(info.FrontendPages)
	} else {
		delete(m.frontendPageCache, canonicalName)
	}
	m.mu.Unlock()

	m.extractPluginWebAssets(canonicalName, mw)

	if normalizePluginName(requestedName) != "" && canonicalName != normalizePluginName(requestedName) {
		slog.Info("插件名称已统一到 Info().ID", "requested_name", requestedName, "canonical_name", canonicalName)
	}

	slog.Info("middleware 插件已加载",
		"name", canonicalName,
		"priority", info.Priority,
		"capabilities", info.Capabilities)

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
	delete(m.hostHandles, inst.Name)
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
	if inst.Middleware != nil {
		if err := inst.Middleware.Stop(context.Background()); err != nil {
			slog.Warn("停止 middleware 插件失败", "name", inst.Name, "error", err)
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
