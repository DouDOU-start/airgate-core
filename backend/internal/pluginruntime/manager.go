// Package pluginruntime 提供通用独立进程插件运行器和能力驱动。
package pluginruntime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/go-hclog"
	goplugin "github.com/hashicorp/go-plugin"

	"github.com/DouDOU-start/airgate-core/internal/config"
	"github.com/DouDOU-start/airgate-core/internal/pluginruntime/protocol"
	"github.com/DouDOU-start/airgate-core/internal/relay/accounttesthook"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
	"github.com/DouDOU-start/airgate-core/internal/relay/relayhook"
)

const (
	pluginStartTimeout  = 15 * time.Second
	pluginStopTimeout   = 3 * time.Second
	circuitFailureLimit = 3
	circuitOpenDuration = 30 * time.Second
	defaultHookTimeout  = 500 * time.Millisecond
	accountTestTimeout  = 2 * time.Second
	maxHookBodyBytes    = 32 << 20
	defaultCodexMode    = "auto"
)

type instance struct {
	id        string
	name      string
	info      protocol.PluginInfo
	client    *goplugin.Client
	plugin    protocol.Plugin
	started   bool
	codexMode string

	mu                  sync.Mutex
	calls               sync.WaitGroup
	stopping            bool
	consecutiveFailures int
	circuitUntil        time.Time
	halfOpenProbe       bool
	recovering          bool
	restart             func(context.Context) (*instance, error)
}

// Manager 管理多个相互独立的插件进程，并实现首个 relay_hook.v1 能力驱动。
// 插件类型不参与运行限制；能力驱动只选择声明了对应 capability 的实例。
type Manager struct {
	pluginDir       string
	logLevel        string
	hookTimeout     time.Duration
	defaultEnabled  bool
	dev             []config.DevPlugin
	coreBaseURL     string
	corePluginToken string

	opMu sync.Mutex
	mu   sync.RWMutex

	instances  map[string]*instance
	lastErrors map[string]string
}

// HostAccess 是 Core 启动时提供给需要回调宿主的插件的进程期访问参数。
// 这些值只在 Init 调用中注入，不属于插件持久化配置。
type HostAccess struct {
	BaseURL string
	Token   string
}

var _ relayhook.Hook = (*Manager)(nil)
var _ relayhook.ProviderAttemptTransformer = (*Manager)(nil)
var _ accounttesthook.Transformer = (*Manager)(nil)

// New 创建插件运行器。调用 LoadAll 前不会启动任何外部进程。
func New(cfg config.PluginsConfig, logLevel string, hostAccess ...HostAccess) *Manager {
	timeout := time.Duration(cfg.HookTimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = defaultHookTimeout
	}
	pluginDir := cfg.Dir
	if pluginDir == "" {
		pluginDir = "data/plugins"
	}
	var host HostAccess
	if len(hostAccess) > 0 {
		host = hostAccess[0]
	}
	return &Manager{
		pluginDir:       pluginDir,
		logLevel:        logLevel,
		hookTimeout:     timeout,
		defaultEnabled:  cfg.Enabled,
		dev:             append([]config.DevPlugin(nil), cfg.Dev...),
		coreBaseURL:     strings.TrimRight(strings.TrimSpace(host.BaseURL), "/"),
		corePluginToken: strings.TrimSpace(host.Token),
		instances:       make(map[string]*instance),
		lastErrors:      make(map[string]string),
	}
}

// LoadAll 按目录名稳定顺序扫描 <dir>/<id>/<id>，再加载显式启用的开发项。
// 单个坏插件只记录错误并继续；插件目录本身不可读时返回错误。
func (m *Manager) LoadAll(ctx context.Context) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	entries, err := os.ReadDir(m.pluginDir)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("读取插件目录失败: %w", err)
	}
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			id := entry.Name()
			if ValidatePluginID(id) != nil {
				continue
			}
			dir := filepath.Join(m.pluginDir, id)
			binaryPath := filepath.Join(dir, pluginBinaryName(id))
			info, statErr := os.Stat(binaryPath)
			if statErr != nil || info.IsDir() {
				continue
			}
			state, stateErr := m.readRuntimeState(id, info.ModTime())
			if stateErr != nil {
				m.setLastError(id, stateErr)
				slog.Error("读取插件运行状态失败", "plugin_ref", pluginLogRef(id), "error_code", pluginErrorCode(stateErr))
				continue
			}
			if !state.Enabled {
				continue
			}
			inst, startErr := m.launchInstalled(ctx, id)
			if startErr != nil {
				m.setLastError(id, startErr)
				slog.Error("插件加载失败", "plugin_ref", pluginLogRef(id), "error_code", pluginErrorCode(startErr))
				continue
			}
			m.setInstance(inst)
			m.clearLastError(id)
		}
	}

	for _, dev := range m.dev {
		if !dev.Enabled {
			continue
		}
		if dev.Path == "" {
			slog.Error("开发插件缺少源码目录", "plugin_ref", pluginLogRef(dev.Name))
			continue
		}
		name := strings.TrimSpace(dev.Name)
		if name == "" {
			name = filepath.Base(filepath.Clean(dev.Path))
		}
		if err := ValidatePluginID(name); err != nil {
			slog.Error("开发插件名称无效", "plugin_ref", pluginLogRef(name), "error_code", pluginErrorCode(err))
			continue
		}
		if m.instanceByID(name) != nil {
			slog.Error("开发插件名称与运行实例冲突", "plugin_ref", pluginLogRef(name))
			continue
		}
		inst, startErr := m.launchDevPlugin(ctx, name, dev)
		if startErr != nil {
			slog.Error("开发插件加载失败", "plugin_ref", pluginLogRef(name), "error_code", pluginErrorCode(startErr))
			continue
		}
		m.setInstance(inst)
	}
	return nil
}

func (m *Manager) launchDevPlugin(ctx context.Context, name string, dev config.DevPlugin) (*instance, error) {
	configPath := dev.Config
	if configPath == "" {
		configPath = filepath.Join(dev.Path, "config.yaml")
	}
	cmd := exec.Command("go", "run", ".")
	cmd.Dir = dev.Path
	inst, err := m.launchPlugin(ctx, "", cmd, configPath, true, true)
	if err != nil {
		return nil, err
	}
	inst.id = name
	inst.restart = func(restartCtx context.Context) (*instance, error) {
		return m.launchDevPlugin(restartCtx, name, dev)
	}
	return inst, nil
}

func (m *Manager) launchPlugin(ctx context.Context, requestedID string, cmd *exec.Cmd, configPath string, initialize, start bool) (*instance, error) {
	client := goplugin.NewClient(&goplugin.ClientConfig{
		HandshakeConfig: protocol.Handshake,
		Plugins: goplugin.PluginSet{
			protocol.PluginKey: &protocol.GRPCPlugin{},
		},
		Cmd:              cmd,
		AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolGRPC},
		StartTimeout:     pluginStartTimeout,
		// 同时关闭子进程原始 stderr、同步标准流和 go-plugin 内部日志转发。
		// 插件需要的业务诊断应由插件自身写入独立日志目标，避免宿主日志
		// 泄露请求改写规则、正文或插件身份。
		Stderr:     io.Discard,
		SyncStdout: io.Discard,
		SyncStderr: io.Discard,
		Logger:     hclog.NewNullLogger(),
	})

	rpcClient, err := client.Client()
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("连接插件进程失败: %w", err)
	}
	raw, err := rpcClient.Dispense(protocol.PluginKey)
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("获取通用插件接口失败: %w", err)
	}
	plugin, ok := raw.(protocol.Plugin)
	if !ok {
		client.Kill()
		return nil, fmt.Errorf("通用插件接口类型不匹配")
	}

	info := normalizePluginInfo(plugin.Info())
	if info.ProtocolVersion != protocol.ProtocolVersion {
		client.Kill()
		return nil, fmt.Errorf("插件协议版本为 %q，当前要求 %q", info.ProtocolVersion, protocol.ProtocolVersion)
	}
	id := info.ID
	if id == "" {
		id = requestedID
	}
	if err := ValidatePluginID(id); err != nil {
		client.Kill()
		return nil, err
	}
	if requestedID != "" && info.ID != "" && info.ID != requestedID {
		client.Kill()
		return nil, fmt.Errorf("插件声明的 ID %q 与安装目录 %q 不一致", info.ID, requestedID)
	}
	info.ID = id
	if info.Name == "" {
		info.Name = id
	}

	var configValues map[string]string
	if initialize {
		values, loadErr := loadPluginConfig(configPath, m.logLevel)
		if loadErr != nil {
			client.Kill()
			return nil, loadErr
		}
		if prepareErr := m.preparePluginConfig(info, values); prepareErr != nil {
			client.Kill()
			return nil, prepareErr
		}
		configValues = values
		initCtx, initCancel := context.WithTimeout(ctx, pluginStartTimeout)
		err = plugin.Init(initCtx, values)
		initCancel()
		if err != nil {
			client.Kill()
			return nil, fmt.Errorf("初始化插件失败: %w", err)
		}
	}
	if start {
		startCtx, startCancel := context.WithTimeout(ctx, pluginStartTimeout)
		err = plugin.Start(startCtx)
		startCancel()
		if err != nil {
			client.Kill()
			return nil, fmt.Errorf("启动插件失败: %w", err)
		}
	}

	codexMode := defaultCodexMode
	if hasCapability(info, protocol.CapabilityCodexExecutorV1) {
		codexMode = normalizeCodexMode(configValues["codex_mode"])
	}
	inst := &instance{id: id, name: info.Name, info: info, client: client, plugin: plugin, started: start, codexMode: codexMode}
	if start {
		slog.Info("插件已启动", "plugin_ref", pluginLogRef(id))
	}
	return inst, nil
}

// preparePluginConfig 清除配置文件中可能伪造的宿主保留键，并只向声明了
// account_autofill.v1 的插件注入当前 Core 的进程期访问参数。
func (m *Manager) preparePluginConfig(info protocol.PluginInfo, values map[string]string) error {
	delete(values, protocol.ConfigKeyCoreBaseURL)
	delete(values, protocol.ConfigKeyCorePluginToken)
	if !hasCapability(info, protocol.CapabilityAccountAutofillV1) {
		return nil
	}
	if m.coreBaseURL == "" || m.corePluginToken == "" {
		return fmt.Errorf("宿主未提供自动补号插件所需的访问参数")
	}
	values[protocol.ConfigKeyCoreBaseURL] = m.coreBaseURL
	values[protocol.ConfigKeyCorePluginToken] = m.corePluginToken
	return nil
}

func normalizePluginInfo(info protocol.PluginInfo) protocol.PluginInfo {
	info.ID = strings.TrimSpace(info.ID)
	info.Name = strings.TrimSpace(info.Name)
	info.Version = strings.TrimSpace(info.Version)
	info.ProtocolVersion = strings.TrimSpace(info.ProtocolVersion)
	info.Type = strings.TrimSpace(info.Type)
	if info.Type == "" {
		info.Type = "general"
	}
	seen := make(map[string]struct{}, len(info.Capabilities))
	capabilities := make([]string, 0, len(info.Capabilities))
	for _, capability := range info.Capabilities {
		capability = strings.TrimSpace(capability)
		if capability == "" {
			continue
		}
		if _, exists := seen[capability]; exists {
			continue
		}
		seen[capability] = struct{}{}
		capabilities = append(capabilities, capability)
	}
	sort.Strings(capabilities)
	info.Capabilities = capabilities
	return info
}

func (m *Manager) setInstance(inst *instance) {
	if inst == nil {
		return
	}
	m.mu.Lock()
	if m.instances == nil {
		m.instances = make(map[string]*instance)
	}
	m.instances[inst.id] = inst
	m.mu.Unlock()
}

func (m *Manager) instanceByID(id string) *instance {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.instances[id]
}

func (m *Manager) instancesFor(capability string) []*instance {
	m.mu.RLock()
	result := make([]*instance, 0, len(m.instances))
	for _, inst := range m.instances {
		if capability == "" || hasCapability(inst.info, capability) {
			result = append(result, inst)
		}
	}
	m.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool {
		if result[i].info.Priority != result[j].info.Priority {
			return result[i].info.Priority < result[j].info.Priority
		}
		return result[i].id < result[j].id
	})
	return result
}

// SupportsCodexWebSocket reports whether at least one currently running
// Codex executor implements the optional bidirectional WebSocket RPC.  The
// capability is intentionally derived from both the concrete executor
// interface and the plugin's advertised metadata: an older plugin may still
// advertise codex_executor.v1 while only implementing the unary HTTP/SSE
// path, and must not make the gateway promise a WebSocket upgrade it cannot
// service.
func (m *Manager) SupportsCodexWebSocket() bool {
	if m == nil {
		return false
	}
	for _, inst := range m.instancesFor(protocol.CapabilityCodexExecutorV1) {
		if inst == nil {
			continue
		}
		if _, ok := inst.plugin.(protocol.CodexWebSocketExecutor); !ok {
			continue
		}
		metadata := inst.info.Metadata
		if value := strings.TrimSpace(metadata["supports_websockets"]); value != "" && !strings.EqualFold(value, "true") {
			continue
		}
		if transports := strings.TrimSpace(metadata["codex_transports"]); transports != "" && !csvToken(transports, "websocket") {
			continue
		}
		return true
	}
	return false
}

// CodexTransportMode returns the effective plugin-wide Codex transport mode.
// The highest-priority running Codex executor wins, matching the dispatch
// order used by ExecuteCodex. Disabled or failed executors do not contribute
// policy. Empty/unknown values are fail-safe to auto.
// This method is intentionally a small optional policy interface consumed by
// relay transport; keeping it here avoids putting routing configuration in
// per-account credential maps.
func (m *Manager) CodexTransportMode() string {
	if m == nil {
		return defaultCodexMode
	}
	instances := m.instancesFor(protocol.CapabilityCodexExecutorV1)
	if len(instances) == 0 {
		return defaultCodexMode
	}
	return normalizeCodexMode(instances[0].codexMode)
}

func normalizeCodexMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "native":
		return "native"
	case "native_only":
		return "native_only"
	case "cpa_translate":
		return "cpa_translate"
	case "cpa_only":
		return "cpa_only"
	case "auto":
		fallthrough
	default:
		return defaultCodexMode
	}
}

func csvToken(value, want string) bool {
	want = strings.TrimSpace(want)
	for _, token := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(token), want) {
			return true
		}
	}
	return false
}

// ErrCodexExecutorUnavailable indicates that no running plugin currently
// advertises the native Codex executor capability. Callers may use this as a
// signal to fall back to CPA translation.
var ErrCodexExecutorUnavailable = errors.New("codex executor plugin unavailable")
var ErrCodexExecutorUnsupported = errors.New("codex executor plugin unsupported")

// ExecuteCodex dispatches a native Codex exchange to the highest-priority
// running plugin that advertises codex_executor.v1. The plugin stream is
// delivered in wire order through emit. Plugin lifecycle accounting and
// circuit-breaking remain owned by Manager.
func (m *Manager) ExecuteCodex(ctx context.Context, request protocol.CodexExecuteRequest, emit func(protocol.CodexExecuteEvent) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	instances := m.instancesFor(protocol.CapabilityCodexExecutorV1)
	if len(instances) == 0 {
		return ErrCodexExecutorUnavailable
	}
	for _, inst := range instances {
		if !inst.acquireCall(time.Now()) {
			continue
		}
		executor, ok := inst.plugin.(interface {
			ExecuteStream(context.Context, protocol.CodexExecuteRequest, func(protocol.CodexExecuteEvent) error) error
		})
		if !ok {
			inst.calls.Done()
			continue
		}
		// responseStarted is the replay-safety boundary for native provider
		// calls.  The transport buffers response_headers before committing bytes
		// to the downstream client, but those headers prove that the selected
		// executor already reached the provider.  Retrying another executor after
		// that point could duplicate a POST/Responses side effect even when no
		// body bytes were observed by Core yet.
		var responseStarted atomic.Bool
		var consumerErr error
		var consumerErrMu sync.Mutex
		// A downstream HTTP/SSE writer can fail while a third-party executor is
		// still blocked reading its upstream stream.  Give each attempt a child
		// context and cancel it on the first consumer error so an executor that
		// ignores the callback's return value cannot retain the request forever.
		callCtx, cancel := context.WithCancel(ctx)
		wrappedEmit := func(event protocol.CodexExecuteEvent) error {
			switch event.Type {
			case protocol.CodexEventResponseHeaders, protocol.CodexEventData:
				responseStarted.Store(true)
			case protocol.CodexEventAuditResult:
				// An audit result with ResponseStarted means the provider returned
				// response headers even if a legacy/older executor omitted the
				// explicit response_headers event. A concrete status or completed
				// marker is equivalent evidence at this trust boundary.
				if event.Audit != nil && (event.Audit.ResponseStarted || event.Audit.StatusCode != 0 || event.Audit.StreamCompleted) {
					responseStarted.Store(true)
				}
			case protocol.CodexEventError:
				// DownstreamStarted is emitted by executors that encountered a
				// read/size failure after the provider response began but before
				// any data event could be delivered.
				if event.Error != nil && (event.Error.DownstreamStarted || event.Error.UpstreamStatus != 0) {
					responseStarted.Store(true)
				}
			}
			if emit == nil {
				return nil
			}
			err := emit(event)
			if err != nil {
				consumerErrMu.Lock()
				if consumerErr == nil {
					consumerErr = err
					cancel()
				}
				consumerErrMu.Unlock()
			}
			return err
		}
		// Keep the in-flight accounting balanced even if a third-party plugin
		// panics while executing.  The recovery boundary may propagate that
		// panic, but StopAll/reload must never wait forever on a leaked call slot.
		err := func() (err error) {
			defer inst.calls.Done()
			defer cancel()
			return executor.ExecuteStream(callCtx, request, wrappedEmit)
		}()
		// An emit failure belongs to Core's downstream consumer, not to the
		// plugin.  Some executors propagate that callback error while others may
		// return nil after stopping their upstream request; preserve the original
		// consumer error in either case and keep it out of plugin health accounting.
		consumerErrMu.Lock()
		consumerFailure := consumerErr
		consumerErrMu.Unlock()
		if consumerFailure != nil {
			inst.recordNeutral()
			return consumerFailure
		}
		if err == nil {
			inst.recordSuccess()
			m.clearLastError(inst.id)
			return nil
		}
		// Request cancellation is likewise not evidence that the plugin is
		// unhealthy. In particular, normal downstream disconnects must not trip
		// the three-failure circuit breaker and restart a healthy executor.
		if ctx.Err() != nil {
			inst.recordNeutral()
			return ctx.Err()
		}
		_ = m.recordFailure(inst, err)
		// Once the provider response has started, the adapter owns the attempt
		// semantics and must not silently replay it through another executor.
		if responseStarted.Load() {
			return err
		}
	}
	return ErrCodexExecutorUnavailable
}

// ExecuteCodexWebSocket dispatches a long-lived bidirectional Responses
// WebSocket session to one native executor. Unlike the unary/server-streaming
// path, a session owns a caller-provided input channel for its entire lifetime;
// retrying it on another plugin would lose frames already consumed by the
// first executor, so a failed session is returned to the caller for reconnect
// handling instead of being silently replayed.
func (m *Manager) ExecuteCodexWebSocket(ctx context.Context, request protocol.CodexExecuteRequest, frames <-chan protocol.CodexWebSocketFrame, emit func(protocol.CodexWebSocketFrame) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	instances := m.instancesFor(protocol.CapabilityCodexExecutorV1)
	if len(instances) == 0 {
		return ErrCodexExecutorUnavailable
	}
	for _, inst := range instances {
		if !inst.acquireCall(time.Now()) {
			continue
		}
		executor, ok := inst.plugin.(protocol.CodexWebSocketExecutor)
		if !ok {
			inst.calls.Done()
			continue
		}
		// Give each long-lived executor invocation its own cancellation boundary.
		// A downstream WebSocket writer can fail while a third-party executor is
		// still waiting on its input channel; canceling only the caller's context
		// would leave that executor blocked indefinitely.  The child context is
		// canceled on the first consumer error below and when this attempt returns.
		callCtx, cancel := context.WithCancel(ctx)
		var consumerErr error
		var consumerErrMu sync.Mutex
		wrappedEmit := func(frame protocol.CodexWebSocketFrame) error {
			if emit == nil {
				return nil
			}
			err := emit(frame)
			if err != nil {
				consumerErrMu.Lock()
				if consumerErr == nil {
					consumerErr = err
					cancel()
				}
				consumerErrMu.Unlock()
			}
			return err
		}
		// As with the unary stream, balance the lifecycle WaitGroup on every
		// exit path, including a plugin panic during a long-lived duplex call.
		err := func() (err error) {
			defer inst.calls.Done()
			// Cancel before dropping the in-flight call count so lifecycle
			// shutdown cannot observe a zero count while the executor still holds
			// resources tied to the child context.
			defer cancel()
			return executor.ExecuteWebSocket(callCtx, request, frames, wrappedEmit)
		}()
		consumerErrMu.Lock()
		consumerFailure := consumerErr
		consumerErrMu.Unlock()
		if consumerFailure != nil {
			inst.recordNeutral()
			return consumerFailure
		}
		if err == nil {
			inst.recordSuccess()
			m.clearLastError(inst.id)
			return nil
		}
		if ctx.Err() != nil {
			inst.recordNeutral()
			return ctx.Err()
		}
		_ = m.recordFailure(inst, err)
		return err
	}
	return ErrCodexExecutorUnsupported
}

func hasCapability(info protocol.PluginInfo, capability string) bool {
	for _, current := range info.Capabilities {
		if current == capability {
			return true
		}
	}
	return false
}

// pluginLogRef 为 Core 日志生成稳定但不暴露插件名称的引用。
func pluginLogRef(id string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(id)))
	return fmt.Sprintf("p-%x", sum[:6])
}

// pluginErrorCode 将跨插件边界的错误压缩为宿主可观测的通用分类，避免插件
// 返回的业务文案进入 Core 日志。
func pluginErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "plugin_error"
	}
}

func sanitizedPluginCallError(message string, err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%s: %w", message, context.DeadlineExceeded)
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("%s: %w", message, context.Canceled)
	default:
		return errors.New(message)
	}
}

// BeforeDispatch 按 priority 升序、ID 升序执行全部 relay_hook.v1 插件。
// 请求体修改会传给后续插件，路由使用最后一个非空结果；单个插件失败只跳过该实例。
func (m *Manager) BeforeDispatch(ctx context.Context, request relayhook.Request) (relayhook.Decision, error) {
	instances := m.instancesFor(protocol.CapabilityRelayHookV1)
	if len(instances) == 0 {
		return relayhook.Decision{}, nil
	}
	timeout := m.hookTimeout
	if timeout <= 0 {
		timeout = defaultHookTimeout
	}
	chainCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	current := request
	var result relayhook.Decision
	for _, inst := range instances {
		if chainCtx.Err() != nil {
			break
		}
		decision, called, err := m.callRelayHook(chainCtx, inst, current)
		if !called {
			continue
		}
		if err != nil {
			slog.Warn("Relay Hook 插件调用失败，已跳过当前实例", "plugin_ref", pluginLogRef(inst.id), "error_code", pluginErrorCode(err))
			continue
		}
		m.clearLastError(inst.id)
		normalized, err := normalizeRelayDecision(current, decision)
		if err != nil {
			err = m.recordFailure(inst, err)
			slog.Warn("Relay Hook 插件决策无效，已跳过当前实例", "plugin_ref", pluginLogRef(inst.id), "error_code", pluginErrorCode(err))
			continue
		}
		if len(normalized.RequestBody) > 0 {
			current.Body = normalized.RequestBody
			result.RequestBody = normalized.RequestBody
			result.Version = relayhook.VersionV1
		}
		if normalized.Route != nil {
			plan := *normalized.Route
			plan.AccountIDs = append([]int(nil), normalized.Route.AccountIDs...)
			plan.AllowRateLimitedAccountIDs = append([]int(nil), normalized.Route.AllowRateLimitedAccountIDs...)
			result.Route = &plan
			result.Version = relayhook.VersionV1
		}
	}
	return result, nil
}

func (m *Manager) callRelayHook(ctx context.Context, inst *instance, request relayhook.Request) (relayhook.Decision, bool, error) {
	if !inst.acquireCall(time.Now()) {
		return relayhook.Decision{}, false, nil
	}
	defer inst.calls.Done()

	payload, err := json.Marshal(request)
	if err != nil {
		return relayhook.Decision{}, true, m.recordFailure(inst, fmt.Errorf("序列化 Relay Hook 请求失败: %w", err))
	}
	response, callErr := inst.plugin.Handle(ctx, protocol.Request{
		Method: http.MethodPost,
		Path:   relayhook.BeforeDispatchPath,
		Header: map[string][]string{"Content-Type": {"application/json"}},
		Body:   payload,
	})
	if callErr != nil {
		return relayhook.Decision{}, true, m.recordFailure(inst, sanitizedPluginCallError("调用 Relay Hook 插件失败", callErr))
	}
	if response.StatusCode == http.StatusNoContent || len(response.Body) == 0 {
		inst.recordSuccess()
		return relayhook.Decision{}, true, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return relayhook.Decision{}, true, m.recordFailure(inst, fmt.Errorf("relay hook 插件返回状态码 %d", response.StatusCode))
	}

	var decision relayhook.Decision
	if err := json.Unmarshal(response.Body, &decision); err != nil {
		return relayhook.Decision{}, true, m.recordFailure(inst, fmt.Errorf("解析 Relay Hook 决策失败: %w", err))
	}
	inst.recordSuccess()
	return decision, true, nil
}

func normalizeRelayDecision(request relayhook.Request, decision relayhook.Decision) (relayhook.Decision, error) {
	if decision.Version == "" && len(decision.RequestBody) == 0 && decision.Route == nil {
		return relayhook.Decision{}, nil
	}
	if decision.Version != relayhook.VersionV1 {
		return relayhook.Decision{}, fmt.Errorf("relay hook 决策版本为 %q，期望 %q", decision.Version, relayhook.VersionV1)
	}
	result := relayhook.Decision{Version: relayhook.VersionV1}
	if len(decision.RequestBody) > 0 {
		if len(decision.RequestBody) > maxHookBodyBytes {
			return relayhook.Decision{}, fmt.Errorf("relay hook 替换请求体超过 %d 字节限制", maxHookBodyBytes)
		}
		parsed, err := dto.ParseChatRequest(decision.RequestBody)
		if err != nil {
			return relayhook.Decision{}, fmt.Errorf("relay hook 替换请求体不是 JSON 对象")
		}
		if parsed.Model != request.Model {
			return relayhook.Decision{}, fmt.Errorf("relay hook 插件不得修改 model")
		}
		if parsed.Stream != request.Stream {
			return relayhook.Decision{}, fmt.Errorf("relay hook 插件不得修改 stream")
		}
		result.RequestBody = decision.RequestBody
	}
	if decision.Route != nil {
		plan, err := relayhook.NormalizeRoutePlan(request.Candidates, decision.Route)
		if err != nil {
			return relayhook.Decision{}, fmt.Errorf("relay hook 路由计划无效: %w", err)
		}
		result.Route = plan
	}
	return result, nil
}

// TransformProviderAttempt runs account-scoped body transforms after Core has
// selected the concrete account. Like the inbound Relay Hook this extension
// is fail-open, but each replacement remains local to the current attempt and
// therefore cannot contaminate a later failover target.
func (m *Manager) TransformProviderAttempt(ctx context.Context, request relayhook.ProviderAttemptRequest) (relayhook.ProviderAttemptDecision, error) {
	instances := m.instancesFor(protocol.CapabilityProviderAttemptTransformV1)
	if len(instances) == 0 {
		return relayhook.ProviderAttemptDecision{}, nil
	}
	timeout := m.hookTimeout
	if timeout <= 0 {
		timeout = defaultHookTimeout
	}
	chainCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	current := request
	var result relayhook.ProviderAttemptDecision
	for _, inst := range instances {
		if chainCtx.Err() != nil {
			break
		}
		decision, called, err := m.callProviderAttemptTransform(chainCtx, inst, current)
		if !called {
			continue
		}
		if err != nil {
			slog.Warn("provider attempt transform failed; skipping plugin", "plugin_ref", pluginLogRef(inst.id), "error_code", pluginErrorCode(err))
			continue
		}
		m.clearLastError(inst.id)
		normalized, err := normalizeProviderAttemptDecision(current, decision)
		if err != nil {
			err = m.recordFailure(inst, err)
			slog.Warn("provider attempt transform returned an invalid decision; skipping plugin", "plugin_ref", pluginLogRef(inst.id), "error_code", pluginErrorCode(err))
			continue
		}
		if len(normalized.RequestBody) == 0 {
			continue
		}
		current.Body = normalized.RequestBody
		result = normalized
	}
	return result, nil
}

func (m *Manager) callProviderAttemptTransform(ctx context.Context, inst *instance, request relayhook.ProviderAttemptRequest) (relayhook.ProviderAttemptDecision, bool, error) {
	if !inst.acquireCall(time.Now()) {
		return relayhook.ProviderAttemptDecision{}, false, nil
	}
	defer inst.calls.Done()

	payload, err := json.Marshal(request)
	if err != nil {
		return relayhook.ProviderAttemptDecision{}, true, m.recordFailure(inst, fmt.Errorf("serialize provider attempt transform request: %w", err))
	}
	response, callErr := inst.plugin.Handle(ctx, protocol.Request{
		Method: http.MethodPost,
		Path:   relayhook.ProviderAttemptPath,
		Header: map[string][]string{"Content-Type": {"application/json"}},
		Body:   payload,
	})
	if callErr != nil {
		return relayhook.ProviderAttemptDecision{}, true, m.recordFailure(inst, sanitizedPluginCallError("call provider attempt transform plugin failed", callErr))
	}
	if response.StatusCode == http.StatusNoContent || len(response.Body) == 0 {
		inst.recordSuccess()
		return relayhook.ProviderAttemptDecision{}, true, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return relayhook.ProviderAttemptDecision{}, true, m.recordFailure(inst, fmt.Errorf("provider attempt transform plugin returned status %d", response.StatusCode))
	}

	var decision relayhook.ProviderAttemptDecision
	if err := json.Unmarshal(response.Body, &decision); err != nil {
		return relayhook.ProviderAttemptDecision{}, true, m.recordFailure(inst, fmt.Errorf("decode provider attempt transform decision: %w", err))
	}
	inst.recordSuccess()
	return decision, true, nil
}

func normalizeProviderAttemptDecision(request relayhook.ProviderAttemptRequest, decision relayhook.ProviderAttemptDecision) (relayhook.ProviderAttemptDecision, error) {
	if decision.Version == "" && len(decision.RequestBody) == 0 {
		return relayhook.ProviderAttemptDecision{}, nil
	}
	if decision.Version != relayhook.VersionV1 {
		return relayhook.ProviderAttemptDecision{}, fmt.Errorf("provider attempt transform version is %q, want %q", decision.Version, relayhook.VersionV1)
	}
	if len(decision.RequestBody) == 0 {
		return relayhook.ProviderAttemptDecision{}, fmt.Errorf("provider attempt transform decision is missing request body")
	}
	if len(decision.RequestBody) > maxHookBodyBytes {
		return relayhook.ProviderAttemptDecision{}, fmt.Errorf("provider attempt transform body exceeds %d bytes", maxHookBodyBytes)
	}
	parsed, err := dto.ParseChatRequest(decision.RequestBody)
	if err != nil {
		return relayhook.ProviderAttemptDecision{}, fmt.Errorf("provider attempt transform body is not a JSON object")
	}
	if parsed.Model != request.Model {
		return relayhook.ProviderAttemptDecision{}, fmt.Errorf("provider attempt transform cannot change model")
	}
	if parsed.Stream != request.Stream {
		return relayhook.ProviderAttemptDecision{}, fmt.Errorf("provider attempt transform cannot change stream")
	}
	return relayhook.ProviderAttemptDecision{Version: relayhook.VersionV1, RequestBody: decision.RequestBody}, nil
}

// TransformAccountTest 按 priority 升序、ID 升序执行全部账号测试请求变换插件。
// 显式插件测试采用失败关闭：没有插件处理、插件报错、超时或返回非法结果都会终止测试。
func (m *Manager) TransformAccountTest(ctx context.Context, request accounttesthook.Request) (accounttesthook.Decision, error) {
	instances := m.instancesFor(protocol.CapabilityAccountTestTransformV1)
	if len(instances) == 0 {
		return accounttesthook.Decision{}, accounttesthook.ErrUnavailable
	}
	chainCtx, cancel := context.WithTimeout(ctx, accountTestTimeout)
	defer cancel()

	current := request
	applied := false
	for _, inst := range instances {
		if err := chainCtx.Err(); err != nil {
			return accounttesthook.Decision{}, fmt.Errorf("账号测试请求变换超时: %w", err)
		}
		decision, called, err := m.callAccountTestTransform(chainCtx, inst, current)
		if !called {
			continue
		}
		if err != nil {
			return accounttesthook.Decision{}, err
		}
		m.clearLastError(inst.id)
		normalized, err := normalizeAccountTestDecision(current, decision)
		if err != nil {
			err = m.recordFailure(inst, err)
			return accounttesthook.Decision{}, err
		}
		if len(normalized.RequestBody) == 0 {
			continue
		}
		current.Body = normalized.RequestBody
		applied = true
	}
	if !applied {
		return accounttesthook.Decision{}, accounttesthook.ErrUnavailable
	}
	return accounttesthook.Decision{
		Version:     accounttesthook.VersionV1,
		RequestBody: current.Body,
	}, nil
}

func (m *Manager) callAccountTestTransform(ctx context.Context, inst *instance, request accounttesthook.Request) (accounttesthook.Decision, bool, error) {
	if !inst.acquireCall(time.Now()) {
		return accounttesthook.Decision{}, false, nil
	}
	defer inst.calls.Done()

	payload, err := json.Marshal(request)
	if err != nil {
		return accounttesthook.Decision{}, true, m.recordFailure(inst, fmt.Errorf("序列化账号测试变换请求失败: %w", err))
	}
	response, callErr := inst.plugin.Handle(ctx, protocol.Request{
		Method: http.MethodPost,
		Path:   accounttesthook.TransformPath,
		Header: map[string][]string{"Content-Type": {"application/json"}},
		Body:   payload,
	})
	if callErr != nil {
		return accounttesthook.Decision{}, true, m.recordFailure(inst, sanitizedPluginCallError("调用账号测试变换插件失败", callErr))
	}
	if response.StatusCode == http.StatusNoContent || len(response.Body) == 0 {
		inst.recordSuccess()
		return accounttesthook.Decision{}, true, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return accounttesthook.Decision{}, true, m.recordFailure(inst, fmt.Errorf("账号测试变换插件返回状态码 %d", response.StatusCode))
	}

	var decision accounttesthook.Decision
	if err := json.Unmarshal(response.Body, &decision); err != nil {
		return accounttesthook.Decision{}, true, m.recordFailure(inst, fmt.Errorf("解析账号测试变换结果失败: %w", err))
	}
	inst.recordSuccess()
	return decision, true, nil
}

func normalizeAccountTestDecision(request accounttesthook.Request, decision accounttesthook.Decision) (accounttesthook.Decision, error) {
	if decision.Version == "" && len(decision.RequestBody) == 0 {
		return accounttesthook.Decision{}, nil
	}
	if decision.Version != accounttesthook.VersionV1 {
		return accounttesthook.Decision{}, fmt.Errorf("账号测试变换结果版本为 %q，期望 %q", decision.Version, accounttesthook.VersionV1)
	}
	if len(decision.RequestBody) == 0 {
		return accounttesthook.Decision{}, fmt.Errorf("账号测试变换结果缺少请求体")
	}
	if len(decision.RequestBody) > maxHookBodyBytes {
		return accounttesthook.Decision{}, fmt.Errorf("账号测试变换请求体超过 %d 字节限制", maxHookBodyBytes)
	}
	replaced, err := dto.ParseChatRequest(decision.RequestBody)
	if err != nil {
		return accounttesthook.Decision{}, fmt.Errorf("账号测试变换请求体不是 JSON 对象")
	}
	if replaced.Model != request.Model {
		return accounttesthook.Decision{}, fmt.Errorf("账号测试变换插件不得修改 model")
	}
	original, err := dto.ParseChatRequest(request.Body)
	if err != nil {
		return accounttesthook.Decision{}, fmt.Errorf("账号测试原始请求体不是 JSON 对象")
	}
	if replaced.Stream != original.Stream {
		return accounttesthook.Decision{}, fmt.Errorf("账号测试变换插件不得修改 stream")
	}
	return accounttesthook.Decision{
		Version:     accounttesthook.VersionV1,
		RequestBody: decision.RequestBody,
	}, nil
}

func (m *Manager) recordFailure(inst *instance, err error) error {
	m.setLastError(inst.id, err)
	if inst.recordFailure(time.Now()) {
		slog.Warn("插件连续失败，已临时熔断并安排自动重启", "plugin_ref", pluginLogRef(inst.id), "duration", circuitOpenDuration.String(), "error_code", pluginErrorCode(err))
		m.scheduleRestart(inst, err)
	}
	return err
}

func (m *Manager) scheduleRestart(inst *instance, cause error) {
	if inst == nil || !inst.beginRecovery() {
		return
	}
	go m.restartUnhealthyInstance(inst, cause)
}

func (m *Manager) restartUnhealthyInstance(failed *instance, cause error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if m.instanceByID(failed.id) != failed {
		failed.finishRecovery()
		return
	}

	restartCtx, cancel := context.WithTimeout(context.Background(), pluginStartTimeout)
	replacement, err := failed.restart(restartCtx)
	cancel()
	if err == nil && replacement == nil {
		err = errors.New("自动重启返回了空插件实例")
	}
	if err != nil {
		failed.finishRecovery()
		restartErr := fmt.Errorf("插件自动重启失败: %w", err)
		m.setLastError(failed.id, restartErr)
		slog.Error("插件自动重启失败，保留熔断状态等待后续探测", "plugin_ref", pluginLogRef(failed.id), "cause_code", pluginErrorCode(cause), "error_code", pluginErrorCode(err))
		return
	}

	m.setInstance(replacement)
	m.clearLastError(failed.id)
	failed.finishRecovery()
	stopPlugin(failed, context.Background())
	slog.Info("插件已自动重启并恢复运行", "plugin_ref", pluginLogRef(replacement.id), "cause_code", pluginErrorCode(cause))
}

func (i *instance) acquireCall(now time.Time) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.stopping || i.recovering {
		return false
	}
	if !i.circuitUntil.IsZero() {
		if now.Before(i.circuitUntil) || i.halfOpenProbe {
			return false
		}
		i.halfOpenProbe = true
	}
	i.calls.Add(1)
	return true
}

func (i *instance) recordSuccess() {
	i.mu.Lock()
	i.consecutiveFailures = 0
	i.circuitUntil = time.Time{}
	i.halfOpenProbe = false
	i.mu.Unlock()
}

// recordNeutral releases a half-open probe without treating a caller-side
// cancellation or downstream emit failure as either plugin success or plugin
// failure. Keeping circuitUntil intact lets the next request perform a fresh
// probe instead of permanently wedging halfOpenProbe=true.
func (i *instance) recordNeutral() {
	i.mu.Lock()
	i.halfOpenProbe = false
	i.mu.Unlock()
}

func (i *instance) recordFailure(now time.Time) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.halfOpenProbe || !i.circuitUntil.IsZero() {
		i.consecutiveFailures = 0
		i.halfOpenProbe = false
		i.circuitUntil = now.Add(circuitOpenDuration)
		return true
	}
	i.consecutiveFailures++
	if i.consecutiveFailures < circuitFailureLimit {
		return false
	}
	i.consecutiveFailures = 0
	i.circuitUntil = now.Add(circuitOpenDuration)
	return true
}

func (i *instance) beginRecovery() bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.stopping || i.recovering || i.restart == nil {
		return false
	}
	i.recovering = true
	return true
}

func (i *instance) finishRecovery() {
	i.mu.Lock()
	i.recovering = false
	i.mu.Unlock()
}

// StopAll 从运行集合摘除全部实例，等待在途调用后停止子进程。
func (m *Manager) StopAll(ctx context.Context) {
	if m == nil {
		return
	}
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	instances := make([]*instance, 0, len(m.instances))
	for _, inst := range m.instances {
		instances = append(instances, inst)
	}
	m.instances = make(map[string]*instance)
	m.mu.Unlock()
	for _, inst := range instances {
		stopPlugin(inst, ctx)
		slog.Info("插件已停止", "plugin_ref", pluginLogRef(inst.id))
	}
}

func pluginBinaryName(id string) string {
	if runtime.GOOS == "windows" {
		return id + ".exe"
	}
	return id
}

// temporaryPluginBinaryName keeps validation binaries directly executable on
// Windows. exec.Command does not apply PATHEXT when the command contains a
// path, so a temporary file named just "plugin-candidate" fails even though
// the same bytes would run after installation as "<id>.exe".
func temporaryPluginBinaryName(prefix string) string {
	if runtime.GOOS == "windows" {
		return prefix + ".exe"
	}
	return prefix
}

func stopPlugin(inst *instance, parent context.Context) {
	if inst == nil {
		return
	}
	inst.mu.Lock()
	inst.stopping = true
	inst.mu.Unlock()

	ctx := parent
	if ctx == nil {
		ctx = context.Background()
	}
	stopCtx, cancel := context.WithTimeout(ctx, pluginStopTimeout)
	defer cancel()
	waitDone := make(chan struct{})
	go func() {
		inst.calls.Wait()
		close(waitDone)
	}()
	select {
	case <-waitDone:
	case <-stopCtx.Done():
		slog.Warn("等待插件在途调用结束超时", "plugin_ref", pluginLogRef(inst.id))
	}
	if inst.started && inst.plugin != nil {
		if err := inst.plugin.Stop(stopCtx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Warn("停止插件失败", "plugin_ref", pluginLogRef(inst.id), "error_code", pluginErrorCode(err))
		}
	}
	if inst.client != nil {
		inst.client.Kill()
	}
}
