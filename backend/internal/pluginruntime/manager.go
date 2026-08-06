// Package pluginruntime 提供通用独立进程插件运行器和能力驱动。
package pluginruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	goplugin "github.com/hashicorp/go-plugin"

	"github.com/DouDOU-start/airgate-core/internal/config"
	"github.com/DouDOU-start/airgate-core/internal/pluginruntime/protocol"
	"github.com/DouDOU-start/airgate-core/internal/relay/relayhook"
)

const (
	pluginStartTimeout  = 15 * time.Second
	pluginStopTimeout   = 3 * time.Second
	circuitFailureLimit = 3
	circuitOpenDuration = 30 * time.Second
	defaultHookTimeout  = 50 * time.Millisecond
	maxHookBodyBytes    = 32 << 20
)

type instance struct {
	id      string
	name    string
	info    protocol.PluginInfo
	client  *goplugin.Client
	plugin  protocol.Plugin
	started bool

	mu                  sync.Mutex
	calls               sync.WaitGroup
	stopping            bool
	consecutiveFailures int
	circuitUntil        time.Time
}

// Manager 管理多个相互独立的插件进程，并实现首个 relay_hook.v1 能力驱动。
// 插件类型不参与运行限制；能力驱动只选择声明了对应 capability 的实例。
type Manager struct {
	pluginDir      string
	logLevel       string
	hookTimeout    time.Duration
	defaultEnabled bool
	dev            []config.DevPlugin

	opMu sync.Mutex
	mu   sync.RWMutex

	instances  map[string]*instance
	lastErrors map[string]string
}

var _ relayhook.Hook = (*Manager)(nil)

// New 创建插件运行器。调用 LoadAll 前不会启动任何外部进程。
func New(cfg config.PluginsConfig, logLevel string) *Manager {
	timeout := time.Duration(cfg.HookTimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = defaultHookTimeout
	}
	pluginDir := cfg.Dir
	if pluginDir == "" {
		pluginDir = "data/plugins"
	}
	return &Manager{
		pluginDir:      pluginDir,
		logLevel:       logLevel,
		hookTimeout:    timeout,
		defaultEnabled: cfg.Enabled,
		dev:            append([]config.DevPlugin(nil), cfg.Dev...),
		instances:      make(map[string]*instance),
		lastErrors:     make(map[string]string),
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
				slog.Error("读取插件运行状态失败", "plugin_id", id, "error", stateErr)
				continue
			}
			if !state.Enabled {
				continue
			}
			inst, startErr := m.launchPlugin(ctx, id, exec.Command(binaryPath), filepath.Join(dir, "config.yaml"), true)
			if startErr != nil {
				m.setLastError(id, startErr)
				slog.Error("插件加载失败", "plugin_id", id, "error", startErr)
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
			slog.Error("开发插件缺少源码目录", "plugin_id", dev.Name)
			continue
		}
		name := strings.TrimSpace(dev.Name)
		if name == "" {
			name = filepath.Base(filepath.Clean(dev.Path))
		}
		if err := ValidatePluginID(name); err != nil {
			slog.Error("开发插件名称无效", "plugin_id", name, "error", err)
			continue
		}
		if m.instanceByID(name) != nil {
			slog.Error("开发插件名称与运行实例冲突", "plugin_id", name)
			continue
		}
		configPath := dev.Config
		if configPath == "" {
			configPath = filepath.Join(dev.Path, "config.yaml")
		}
		cmd := exec.Command("go", "run", ".")
		cmd.Dir = dev.Path
		inst, startErr := m.launchPlugin(ctx, "", cmd, configPath, true)
		if startErr != nil {
			slog.Error("开发插件加载失败", "plugin_id", name, "error", startErr)
			continue
		}
		inst.id = name
		m.setInstance(inst)
	}
	return nil
}

func (m *Manager) launchPlugin(ctx context.Context, requestedID string, cmd *exec.Cmd, configPath string, start bool) (*instance, error) {
	client := goplugin.NewClient(&goplugin.ClientConfig{
		HandshakeConfig: protocol.Handshake,
		Plugins: goplugin.PluginSet{
			protocol.PluginKey: &protocol.GRPCPlugin{},
		},
		Cmd:              cmd,
		AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolGRPC},
		StartTimeout:     pluginStartTimeout,
		SyncStdout:       os.Stdout,
		SyncStderr:       os.Stderr,
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

	values, err := loadPluginConfig(configPath, m.logLevel)
	if err != nil {
		client.Kill()
		return nil, err
	}
	initCtx, cancel := context.WithTimeout(ctx, pluginStartTimeout)
	err = plugin.Init(initCtx, values)
	cancel()
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("初始化插件失败: %w", err)
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

	inst := &instance{id: id, name: info.Name, info: info, client: client, plugin: plugin, started: start}
	if start {
		slog.Info("插件已启动", "plugin_id", id, "type", info.Type, "capabilities", info.Capabilities, "version", info.Version)
	}
	return inst, nil
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

func hasCapability(info protocol.PluginInfo, capability string) bool {
	for _, current := range info.Capabilities {
		if current == capability {
			return true
		}
	}
	return false
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
			m.setLastError(inst.id, err)
			slog.Warn("Relay Hook 插件调用失败，已跳过当前实例", "plugin_id", inst.id, "error", err)
			continue
		}
		m.clearLastError(inst.id)
		normalized, err := normalizeRelayDecision(current, decision)
		if err != nil {
			err = m.recordFailure(inst, err)
			m.setLastError(inst.id, err)
			slog.Warn("Relay Hook 插件决策无效，已跳过当前实例", "plugin_id", inst.id, "error", err)
			continue
		}
		if len(normalized.RequestBody) > 0 {
			current.Body = append(json.RawMessage(nil), normalized.RequestBody...)
			result.RequestBody = append(json.RawMessage(nil), normalized.RequestBody...)
			result.Version = relayhook.VersionV1
		}
		if normalized.Route != nil {
			plan := *normalized.Route
			plan.AccountIDs = append([]int(nil), normalized.Route.AccountIDs...)
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
		return relayhook.Decision{}, true, fmt.Errorf("序列化 Relay Hook 请求失败: %w", err)
	}
	response, callErr := inst.plugin.Handle(ctx, protocol.Request{
		Method: http.MethodPost,
		Path:   relayhook.BeforeDispatchPath,
		Header: map[string][]string{"Content-Type": {"application/json"}},
		Body:   payload,
	})
	if callErr != nil {
		return relayhook.Decision{}, true, m.recordFailure(inst, fmt.Errorf("调用 Relay Hook 插件失败: %w", callErr))
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
		var object map[string]json.RawMessage
		if err := json.Unmarshal(decision.RequestBody, &object); err != nil || object == nil {
			return relayhook.Decision{}, fmt.Errorf("relay hook 替换请求体不是 JSON 对象")
		}
		var model string
		if err := json.Unmarshal(object["model"], &model); err != nil || model != request.Model {
			return relayhook.Decision{}, fmt.Errorf("relay hook 插件不得修改 model")
		}
		stream := false
		if raw, exists := object["stream"]; exists {
			if err := json.Unmarshal(raw, &stream); err != nil {
				return relayhook.Decision{}, fmt.Errorf("relay hook 替换请求体的 stream 无效")
			}
		}
		if stream != request.Stream {
			return relayhook.Decision{}, fmt.Errorf("relay hook 插件不得修改 stream")
		}
		result.RequestBody = append(json.RawMessage(nil), decision.RequestBody...)
	}
	if decision.Route != nil {
		if decision.Route.Fallback != relayhook.FallbackCore {
			return relayhook.Decision{}, fmt.Errorf("relay hook 不支持 fallback %q", decision.Route.Fallback)
		}
		allowed := make(map[int]struct{})
		for _, candidate := range request.Candidates {
			if candidate.Kind == "account" {
				allowed[candidate.ID] = struct{}{}
			}
		}
		seen := make(map[int]struct{}, len(decision.Route.AccountIDs))
		accountIDs := make([]int, 0, len(decision.Route.AccountIDs))
		for _, accountID := range decision.Route.AccountIDs {
			if _, ok := allowed[accountID]; !ok {
				continue
			}
			if _, duplicate := seen[accountID]; duplicate {
				continue
			}
			seen[accountID] = struct{}{}
			accountIDs = append(accountIDs, accountID)
		}
		if len(accountIDs) > 0 {
			result.Route = &relayhook.RoutePlan{AccountIDs: accountIDs, Fallback: relayhook.FallbackCore}
		}
	}
	return result, nil
}

func (m *Manager) recordFailure(inst *instance, err error) error {
	if inst.recordFailure(time.Now()) {
		slog.Warn("插件连续失败，已临时熔断", "plugin_id", inst.id, "duration", circuitOpenDuration.String(), "error", err)
	}
	return err
}

func (i *instance) acquireCall(now time.Time) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.stopping || (!i.circuitUntil.IsZero() && now.Before(i.circuitUntil)) {
		return false
	}
	i.calls.Add(1)
	return true
}

func (i *instance) recordSuccess() {
	i.mu.Lock()
	i.consecutiveFailures = 0
	i.circuitUntil = time.Time{}
	i.mu.Unlock()
}

func (i *instance) recordFailure(now time.Time) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.consecutiveFailures++
	if i.consecutiveFailures < circuitFailureLimit {
		return false
	}
	i.consecutiveFailures = 0
	i.circuitUntil = now.Add(circuitOpenDuration)
	return true
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
		slog.Info("插件已停止", "plugin_id", inst.id)
	}
}

func pluginBinaryName(id string) string {
	if runtime.GOOS == "windows" {
		return id + ".exe"
	}
	return id
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
		slog.Warn("等待插件在途调用结束超时", "plugin_id", inst.id)
	}
	if inst.started && inst.plugin != nil {
		if err := inst.plugin.Stop(stopCtx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Warn("停止插件失败", "plugin_id", inst.id, "error", err)
		}
	}
	if inst.client != nil {
		inst.client.Kill()
	}
}
