// Package plugin 提供插件生命周期管理、市场和请求转发。
package plugin

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	goplugin "github.com/hashicorp/go-plugin"

	sdk "github.com/DouDOU-start/airgate-sdk"
	sdkgrpc "github.com/DouDOU-start/airgate-sdk/grpc"

	"github.com/DouDOU-start/airgate-core/ent"
)

// PluginInstance 运行中的插件实例。
type PluginInstance struct {
	Name               string
	SourceName         string
	BinaryDir          string
	DisplayName        string
	Version            string
	Author             string
	Platform           string
	Type               string // "gateway", "extension", "middleware"
	InstructionPresets []string
	ConfigSchema       []sdk.ConfigField
	Capabilities       []string // 插件声明的 host capability 列表（仅展示用）
	Priority           int32    // 仅对 type=middleware 生效，决定 chain 顺序

	Client     *goplugin.Client
	Gateway    *sdkgrpc.GatewayGRPCClient
	Extension  *sdkgrpc.ExtensionGRPCClient
	Middleware *sdkgrpc.MiddlewareGRPCClient

	// 后台任务调度上下文。stopBackground 由 Core 调度器创建，用于停止
	// 该插件实例的所有后台任务 goroutine。stopPlugin 时调用。
	stopBackground context.CancelFunc
}

// Manager 插件管理器。
type Manager struct {
	pluginDir string
	logLevel  string
	coreDSN   string      // core 数据库 DSN，启动插件时自动注入到 Init Config 的 db_dsn 字段
	db        *ent.Client // 用于读取/持久化插件配置

	// hostFactory 是 Core 暴露给插件的 HostService 工厂。每个插件 spawn 时会从中
	// 派生一个独立的 *pluginHostHandle，做 per-plugin 的 capability 隔离。
	// 由 SetHostService 注入；nil 时插件 ctx.Host()==nil（软失败模式）。
	hostFactory *HostService

	// hostHandles 保留每个插件名 → 它当前的 host handle，用于 spawn 完成后写入 capability set。
	// key 一般是 canonicalName；spawn 中会用 requestedName 临时占位，spawn 后改 key。
	hostHandles map[string]*pluginHostHandle

	// pluginDB 给每个插件 provision 独立 schema + 受限 role + plugin_dsn。
	// 详见 ADR-0001 Decision 5。nil 时不做 provisioning（仍然可以正常加载插件，
	// 只是它们拿不到 plugin_dsn，必须用旧的 db_dsn）。
	pluginDB *pluginDSNProvisioner

	// devWatcher 监听 dev 模式插件源码目录的 .go 改动，自动 ReloadDev。
	// 实现是 mtime 轮询（不是 fsnotify），原因见 dev_watcher.go 顶部注释。
	devWatcher *devWatcher

	mu        sync.RWMutex
	instances map[string]*PluginInstance
	aliases   map[string]string
	devPaths  map[string]string

	modelCache        map[string][]sdk.ModelInfo
	routeCache        map[string][]sdk.RouteDefinition
	credCache         map[string][]sdk.CredentialField
	accountTypeCache  map[string][]sdk.AccountType
	frontendPageCache map[string][]sdk.FrontendPage
}

// SetHostService 注入 Core 实现的 HostService 工厂。
//
// 必须在 Manager 加载任何插件之前（即 server 启动时）调用，否则启动较早的插件
// 会拿到 host_broker_id=0，需要重启才能恢复 host 通路。
func (m *Manager) SetHostService(factory *HostService) {
	m.hostFactory = factory
}

// PluginMeta 插件运行时元信息。
type PluginMeta struct {
	Name               string
	DisplayName        string
	Version            string
	Author             string
	Type               string
	Platform           string
	AccountTypes       []sdk.AccountType
	FrontendPages      []sdk.FrontendPage
	InstructionPresets []string
	ConfigSchema       []sdk.ConfigField
	Config             map[string]string
	HasWebAssets       bool
	IsDev              bool
}

// NewManager 创建插件管理器。
//
// 插件要调用 core 能力一律通过 HostService（hashicorp/go-plugin GRPCBroker 反向 gRPC），
// 由 SetHostService 注入。因此这里不再需要 coreBaseURL / apiKeySecret 参数：插件不再
// 走 HTTP + admin key 回调 core。
func NewManager(pluginDir, logLevel, coreDSN string, db *ent.Client) *Manager {
	m := &Manager{
		pluginDir:         pluginDir,
		logLevel:          logLevel,
		coreDSN:           coreDSN,
		db:                db,
		hostHandles:       make(map[string]*pluginHostHandle),
		instances:         make(map[string]*PluginInstance),
		aliases:           make(map[string]string),
		devPaths:          make(map[string]string),
		modelCache:        make(map[string][]sdk.ModelInfo),
		routeCache:        make(map[string][]sdk.RouteDefinition),
		credCache:         make(map[string][]sdk.CredentialField),
		accountTypeCache:  make(map[string][]sdk.AccountType),
		frontendPageCache: make(map[string][]sdk.FrontendPage),
	}
	if coreDSN != "" && db != nil {
		m.pluginDB = newPluginDSNProvisioner(db, coreDSN)
	}
	m.devWatcher = newDevWatcher(m)
	return m
}

// prepareHostHandle 在 spawn 一个插件之前为它创建/获取一个 host handle。
// 调用方负责在 spawn 完成后调 finalizeHostHandle 写入 capability set。
//
// 如果 hostFactory == nil（部署没启用 host service），返回 nil，调用方走软失败路径。
func (m *Manager) prepareHostHandle(name string) *pluginHostHandle {
	if m.hostFactory == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.hostHandles[name]; ok {
		return existing
	}
	handle := m.hostFactory.NewPluginHandle(name)
	m.hostHandles[name] = handle
	return handle
}

// finalizeHostHandle 把插件实际声明的 capability 写入它的 host handle，让后续 RPC 通过校验。
//
// 兼容模式（详见 ADR-0001 Decision 4）：sdk_version <= 0.2.x 的存量插件传 nil capability，
// 此时 handle 内部允许任何 RPC 调用并 log debug，便于审计。
func (m *Manager) finalizeHostHandle(name string, info sdk.PluginInfo) {
	handle := m.lookupHostHandle(name)
	if handle == nil {
		return
	}
	if isLegacySDKVersion(info.SDKVersion) {
		handle.SetCapabilities(nil) // 兼容模式
		slog.Info("plugin capability 兼容模式（sdk_version 豁免）",
			"plugin", name, "sdk_version", info.SDKVersion)
		return
	}
	caps := make(map[string]bool, len(info.Capabilities))
	for _, c := range info.Capabilities {
		caps[c] = true
	}
	handle.SetCapabilities(caps)
	slog.Info("plugin capability 已绑定",
		"plugin", name, "sdk_version", info.SDKVersion, "capabilities", info.Capabilities)
}

// lookupHostHandle 取已注册的 host handle。
func (m *Manager) lookupHostHandle(name string) *pluginHostHandle {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.hostHandles[name]
}

// removeHostHandle 在 stopPlugin 时调用，回收 handle。
func (m *Manager) removeHostHandle(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.hostHandles, name)
}

// relocateHostHandle 把一个 host handle 从 oldName key 改名为 newName key。
//
// 用于 spawn 后 canonical name 与 requestedName 不一致的场景：spawn 前我们用
// requestedName 占位创建 handle，spawn 后才知道真正的 canonicalName。
func (m *Manager) relocateHostHandle(oldName, newName string) {
	if oldName == newName {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	handle, ok := m.hostHandles[oldName]
	if !ok {
		return
	}
	delete(m.hostHandles, oldName)
	m.hostHandles[newName] = handle
}

// isLegacySDKVersion 判断一个插件的 sdk_version 是否需要走兼容模式（豁免 capability 校验）。
//
// 规则：sdk_version 字符串以 "0.1." 或 "0.2." 开头视为 legacy；空串也视为 legacy
// （非常老的版本）。0.3.x 及以上必须显式声明 capability。
func isLegacySDKVersion(v string) bool {
	if v == "" {
		return true
	}
	return strings.HasPrefix(v, "0.1.") || strings.HasPrefix(v, "0.2.") ||
		v == "0.1.0" || v == "0.2.0"
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
