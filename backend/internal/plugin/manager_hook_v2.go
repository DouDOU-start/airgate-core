package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"gopkg.in/yaml.v3"

	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"

	"github.com/DouDOU-start/airgate-core/ent"
	pluginent "github.com/DouDOU-start/airgate-core/ent/plugin"
	"github.com/DouDOU-start/airgate-core/internal/plugin/hookv2"
)

var errSDKPluginHandshake = errors.New("SDK 插件握手失败")

func pluginExecutableName(name string) string {
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(name), ".exe") {
		return name + ".exe"
	}
	return name
}

// findPluginExecutablePath 优先采用当前 Windows 安装格式 <name>.exe，同时兼容
// 历史版本曾写入的无扩展名 <name>。非 Windows 平台只检查原始名称。
func findPluginExecutablePath(dir, name string) (string, bool) {
	primary := pluginExecutableName(name)
	candidates := []string{primary}
	if runtime.GOOS == "windows" && !strings.EqualFold(primary, name) {
		candidates = append(candidates, name)
	}
	for _, candidate := range candidates {
		path := filepath.Join(dir, candidate)
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() {
			return path, true
		}
	}
	return filepath.Join(dir, primary), false
}

// runnablePluginExecutablePath 把 Windows 历史无扩展名二进制按需复制为 .exe。
// Go 在 Windows 上不会直接启动无扩展名 PE，因此只返回 legacy 路径仍无法兼容；
// 保留原文件并生成当前格式副本，可让升级后的 LoadAll/ReloadInstance 自愈。
func runnablePluginExecutablePath(dir, name string) (string, error) {
	path, ok := findPluginExecutablePath(dir, name)
	if !ok {
		return path, os.ErrNotExist
	}
	currentPath := filepath.Join(dir, pluginExecutableName(name))
	if runtime.GOOS != "windows" || strings.EqualFold(path, currentPath) {
		return path, nil
	}
	if err := copyLegacyWindowsPluginExecutable(path, currentPath); err != nil {
		return currentPath, err
	}
	return currentPath, nil
}

func copyLegacyWindowsPluginExecutable(sourcePath, targetPath string) error {
	if info, err := os.Stat(targetPath); err == nil && !info.IsDir() {
		return nil
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("打开旧版插件二进制失败: %w", err)
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return fmt.Errorf("读取旧版插件二进制信息失败: %w", err)
	}
	mode := info.Mode().Perm()
	if mode == 0 {
		mode = 0755
	}
	target, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if errors.Is(err, os.ErrExist) {
		if existing, statErr := os.Stat(targetPath); statErr == nil && !existing.IsDir() {
			return nil
		}
	}
	if err != nil {
		return fmt.Errorf("创建新版 .exe 插件副本失败: %w", err)
	}
	complete := false
	defer func() {
		_ = target.Close()
		if !complete {
			_ = os.Remove(targetPath)
		}
	}()
	if _, err := io.Copy(target, source); err != nil {
		return fmt.Errorf("复制旧版插件二进制失败: %w", err)
	}
	if err := target.Close(); err != nil {
		return fmt.Errorf("关闭新版 .exe 插件副本失败: %w", err)
	}
	complete = true
	return nil
}

// startPlugin 优先使用当前 SDK runtime；只有握手阶段明确失败时，才把同一条
// 启动命令作为旧版 Relay Hook v2 插件重新拉起。exec.Cmd 启动后不能复用，
// 因此两个 runtime 必须各自使用独立克隆。
func (m *Manager) startPlugin(ctx context.Context, requestedName string, cmd *exec.Cmd, binaryDir string) (string, error) {
	if cmd == nil {
		return "", fmt.Errorf("插件启动命令不能为空")
	}
	sdkCmd := clonePluginCommand(cmd)
	v2Cmd := clonePluginCommand(cmd)

	canonicalName, sdkErr := m.startSDKPlugin(ctx, requestedName, sdkCmd, binaryDir)
	if sdkErr == nil {
		return canonicalName, nil
	}
	if !errors.Is(sdkErr, errSDKPluginHandshake) {
		return "", sdkErr
	}

	canonicalName, v2Err := m.startRelayHookV2Plugin(ctx, requestedName, v2Cmd, binaryDir)
	if v2Err == nil {
		return canonicalName, nil
	}
	return "", fmt.Errorf("插件不兼容当前 SDK runtime 或 Relay Hook v2: SDK: %v; Relay Hook v2: %w", sdkErr, v2Err)
}

// clonePluginCommand 复制尚未启动的命令描述。Process/ProcessState/Cancel 等运行期
// 字段有意不复制；stdout/stderr 由 go-plugin ClientConfig 统一接管。
func clonePluginCommand(cmd *exec.Cmd) *exec.Cmd {
	if cmd == nil {
		return nil
	}
	return &exec.Cmd{
		Path:  cmd.Path,
		Args:  append([]string(nil), cmd.Args...),
		Env:   append([]string(nil), cmd.Env...),
		Dir:   cmd.Dir,
		Stdin: cmd.Stdin,
	}
}

func (m *Manager) newRelayHookV2ClientConfig(cmd *exec.Cmd, forwardOutput bool) *goplugin.ClientConfig {
	cfg := &goplugin.ClientConfig{
		HandshakeConfig: hookv2.Handshake,
		Plugins: goplugin.PluginSet{
			hookv2.PluginKey: &hookv2.GRPCPlugin{},
		},
		Cmd:              cmd,
		AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolGRPC},
		GRPCDialOptions: []grpc.DialOption{
			grpc.WithDefaultCallOptions(
				grpc.MaxCallRecvMsgSize(hookv2.MaxMessageBytes),
				grpc.MaxCallSendMsgSize(hookv2.MaxMessageBytes),
			),
		},
		StartTimeout: pluginStartTimeout,
	}
	if forwardOutput {
		cfg.SyncStdout = os.Stdout
		cfg.SyncStderr = os.Stderr
	}
	return cfg
}

func (m *Manager) startRelayHookV2Plugin(ctx context.Context, requestedName string, cmd *exec.Cmd, binaryDir string) (string, error) {
	client := goplugin.NewClient(m.newRelayHookV2ClientConfig(cmd, true))
	rpcClient, err := client.Client()
	if err != nil {
		client.Kill()
		return "", fmt.Errorf("连接 Relay Hook v2 插件进程失败: %w", err)
	}
	raw, err := rpcClient.Dispense(hookv2.PluginKey)
	if err != nil {
		client.Kill()
		return "", fmt.Errorf("获取 Relay Hook v2 插件接口失败: %w", err)
	}
	pluginClient, ok := raw.(relayHookV2Client)
	if !ok {
		client.Kill()
		return "", fmt.Errorf("Relay Hook v2 插件接口类型不匹配")
	}

	info := normalizeRelayHookV2Info(pluginClient.Info())
	if info.ProtocolVersion != hookv2.ProtocolVersion {
		client.Kill()
		return "", fmt.Errorf("Relay Hook v2 插件协议版本为 %q，期望 %q", info.ProtocolVersion, hookv2.ProtocolVersion)
	}
	if !containsString(info.Capabilities, hookv2.CapabilityRelayHookV1) &&
		!containsString(info.Capabilities, hookv2.CapabilityAccountTestTransformV1) {
		client.Kill()
		return "", fmt.Errorf("Relay Hook v2 插件未声明受支持的 Hook 能力")
	}
	canonicalName := normalizePluginName(info.ID)
	if canonicalName == "" {
		canonicalName = normalizePluginName(requestedName)
	}
	if canonicalName == "" {
		client.Kill()
		return "", fmt.Errorf("Relay Hook v2 插件未提供有效的 ID")
	}
	if info.Name == "" {
		info.Name = canonicalName
	}
	info.ID = canonicalName

	config, err := m.buildRelayHookV2Config(ctx, canonicalName, cmd, binaryDir)
	if err != nil {
		client.Kill()
		return "", err
	}
	initCtx, initCancel := context.WithTimeout(ctx, pluginStartTimeout)
	err = pluginClient.Init(initCtx, config)
	initCancel()
	if err != nil {
		client.Kill()
		return "", fmt.Errorf("初始化 Relay Hook v2 插件失败: %w", err)
	}
	startCtx, startCancel := context.WithTimeout(ctx, pluginStartTimeout)
	err = pluginClient.Start(startCtx)
	startCancel()
	if err != nil {
		client.Kill()
		return "", fmt.Errorf("启动 Relay Hook v2 插件失败: %w", err)
	}

	pluginType := strings.TrimSpace(info.Type)
	if pluginType == "" {
		pluginType = string(sdk.PluginTypeMiddleware)
	}
	hook := newRelayHookV2Plugin(canonicalName, pluginClient)
	instance := &PluginInstance{
		Name:             canonicalName,
		SourceName:       normalizePluginName(requestedName),
		BinaryDir:        normalizePluginName(binaryDir),
		DisplayName:      info.Name,
		Version:          info.Version,
		Author:           info.Author,
		Type:             pluginType,
		ConfigSchema:     relayHookV2ConfigSchema(info.ConfigSchema),
		RichConfigSchema: relayHookV2RichConfigSchema(info.ConfigSchema),
		Metadata:         cloneStringMap(info.Metadata),
		Capabilities:     append([]string(nil), info.Capabilities...),
		Priority:         info.Priority,
		Client:           client,
		RelayHookV2:      hook,
	}

	m.mu.Lock()
	m.instances[canonicalName] = instance
	m.registerAliasesLocked(canonicalName, requestedName, binaryDir)
	m.mu.Unlock()

	if normalizePluginName(requestedName) != "" && canonicalName != normalizePluginName(requestedName) {
		slog.Info("plugin_name_canonicalized", "requested_name", requestedName, "canonical_name", canonicalName)
	}
	slog.Info("plugin_runtime_started",
		sdk.LogFieldPluginID, canonicalName,
		"kind", pluginType,
		"runtime", "relay_hook_v2",
		"priority", info.Priority,
		"capabilities", info.Capabilities,
	)
	return canonicalName, nil
}

func normalizeRelayHookV2Info(info hookv2.PluginInfo) hookv2.PluginInfo {
	info.ID = strings.TrimSpace(info.ID)
	info.Name = strings.TrimSpace(info.Name)
	info.Version = strings.TrimSpace(info.Version)
	info.ProtocolVersion = strings.TrimSpace(info.ProtocolVersion)
	info.Type = strings.TrimSpace(info.Type)
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

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func relayHookV2ConfigSchema(schema *hookv2.ConfigSchema) []sdk.ConfigField {
	if schema == nil || len(schema.Fields) == 0 {
		return nil
	}
	fields := make([]sdk.ConfigField, 0, len(schema.Fields))
	for _, field := range schema.Fields {
		defaultValue, err := encodeRelayHookV2ConfigValue(field.Default)
		if err != nil {
			defaultValue = ""
		}
		fields = append(fields, sdk.ConfigField{
			Key:         field.Key,
			Label:       field.Label,
			Type:        relayHookV2ConfigFieldType(field.Widget),
			Required:    field.Required,
			Default:     defaultValue,
			Description: field.Description,
		})
	}
	return fields
}

func relayHookV2RichConfigSchema(schema *hookv2.ConfigSchema) *PluginConfigSchema {
	if schema == nil {
		return nil
	}
	result := &PluginConfigSchema{
		Version: schema.Version,
		Fields:  make([]PluginConfigField, 0, len(schema.Fields)),
	}
	for _, field := range schema.Fields {
		result.Fields = append(result.Fields, PluginConfigField{
			Key:         field.Key,
			FallbackKey: field.FallbackKey,
			Label:       field.Label,
			Type:        relayHookV2ConfigFieldType(field.Widget),
			Widget:      field.Widget,
			DataSource:  field.DataSource,
			Required:    field.Required,
			Default:     cloneConfigDefault(field.Default),
			Description: field.Description,
			Filter:      cloneConfigStringMap(field.Filter),
		})
	}
	return result
}

func relayHookV2ConfigFieldType(widget string) string {
	switch strings.ToLower(strings.TrimSpace(widget)) {
	case "switch":
		return "bool"
	case "number":
		return "int"
	default:
		return "string"
	}
}

func (m *Manager) buildRelayHookV2Config(ctx context.Context, name string, cmd *exec.Cmd, binaryDir string) (map[string]string, error) {
	config := map[string]string{hookv2.ConfigKeyLogLevel: m.logLevel}
	configPath := ""
	if cmd != nil && strings.TrimSpace(cmd.Dir) != "" {
		configPath = filepath.Join(cmd.Dir, "config.yaml")
	} else if dir := normalizePluginName(binaryDir); dir != "" {
		configPath = filepath.Join(m.pluginDir, dir, "config.yaml")
	}
	if configPath != "" {
		data, err := os.ReadFile(configPath)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("读取 Relay Hook v2 插件配置失败: %w", err)
		}
		if err == nil {
			var raw map[string]any
			if err := yaml.Unmarshal(data, &raw); err != nil {
				return nil, fmt.Errorf("解析 Relay Hook v2 插件配置失败: %w", err)
			}
			for key, value := range raw {
				encoded, err := encodeRelayHookV2ConfigValue(value)
				if err != nil {
					return nil, fmt.Errorf("编码 Relay Hook v2 插件配置项 %s 失败: %w", key, err)
				}
				config[key] = encoded
			}
		}
	}

	if m.db == nil {
		return config, nil
	}
	row, err := m.db.Plugin.Query().Where(pluginent.NameEQ(name)).Only(ctx)
	if err != nil {
		if !ent.IsNotFound(err) {
			slog.Warn("plugin_config_load_failed", sdk.LogFieldPluginID, name, sdk.LogFieldError, err)
		}
		return config, nil
	}
	for key, value := range row.Config {
		encoded, err := encodeRelayHookV2ConfigValue(value)
		if err != nil {
			return nil, fmt.Errorf("编码 Relay Hook v2 数据库配置项 %s 失败: %w", key, err)
		}
		config[key] = encoded
	}
	return config, nil
}

func encodeRelayHookV2ConfigValue(value any) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "", nil
	case string:
		return typed, nil
	case bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64,
		json.Number:
		return fmt.Sprint(typed), nil
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return "", err
		}
		return string(encoded), nil
	}
}

// probePluginName 兼容安装当前 SDK 插件和旧版 Relay Hook v2 插件。
func (m *Manager) probePluginName(fallbackName string, binary []byte) (string, error) {
	name, sdkErr := m.probeSDKPluginName(fallbackName, binary)
	if sdkErr == nil {
		return name, nil
	}
	name, v2Err := m.probeRelayHookV2Name(fallbackName, binary)
	if v2Err == nil {
		return name, nil
	}
	return "", fmt.Errorf("SDK 探测失败: %v; Relay Hook v2 探测失败: %w", sdkErr, v2Err)
}

func (m *Manager) probeRelayHookV2Name(fallbackName string, binary []byte) (string, error) {
	tmpDir, err := os.MkdirTemp("", "airgate-relay-hook-v2-probe-*")
	if err != nil {
		return "", fmt.Errorf("创建 Relay Hook v2 探测临时目录失败: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			slog.Warn("清理 Relay Hook v2 探测临时目录失败", "dir", tmpDir, sdk.LogFieldError, err)
		}
	}()

	tmpBinary := filepath.Join(tmpDir, pluginExecutableName(fallbackName))
	if err := os.WriteFile(tmpBinary, binary, 0755); err != nil {
		return "", fmt.Errorf("写入 Relay Hook v2 临时二进制失败: %w", err)
	}
	client := goplugin.NewClient(m.newRelayHookV2ClientConfig(exec.Command(tmpBinary), false))
	defer client.Kill()
	rpcClient, err := client.Client()
	if err != nil {
		return "", fmt.Errorf("连接 Relay Hook v2 探测进程失败: %w", err)
	}
	raw, err := rpcClient.Dispense(hookv2.PluginKey)
	if err != nil {
		return "", fmt.Errorf("获取 Relay Hook v2 探测接口失败: %w", err)
	}
	pluginClient, ok := raw.(relayHookV2Client)
	if !ok {
		return "", fmt.Errorf("Relay Hook v2 探测接口类型不匹配")
	}
	info := normalizeRelayHookV2Info(pluginClient.Info())
	if info.ProtocolVersion != hookv2.ProtocolVersion {
		return "", fmt.Errorf("Relay Hook v2 探测到协议版本 %q", info.ProtocolVersion)
	}
	if !containsString(info.Capabilities, hookv2.CapabilityRelayHookV1) &&
		!containsString(info.Capabilities, hookv2.CapabilityAccountTestTransformV1) {
		return "", fmt.Errorf("Relay Hook v2 探测对象未声明受支持的 Hook 能力")
	}
	if info.ID != "" {
		return info.ID, nil
	}
	return fallbackName, nil
}
