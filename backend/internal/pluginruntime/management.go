package pluginruntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/DouDOU-start/airgate-core/internal/pluginruntime/protocol"
)

const (
	// MaxPluginBinarySize 是 Web 安装允许的插件二进制上限。
	MaxPluginBinarySize int64 = 500 << 20
	// MaxPluginConfigSize 是在线编辑单个插件配置的上限。
	MaxPluginConfigSize = 1 << 20
)

var (
	ErrPluginNotFound  = errors.New("插件不存在")
	ErrPluginExists    = errors.New("插件已安装")
	ErrPluginDisabled  = errors.New("插件尚未启用")
	ErrInvalidPluginID = errors.New("插件 ID 无效")
	pluginIDPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
)

// PluginStatus 是管理 API 展示的已安装插件状态。
type PluginStatus struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Version         string    `json:"version"`
	ProtocolVersion string    `json:"protocol_version"`
	Description     string    `json:"description"`
	Author          string    `json:"author"`
	Type            string    `json:"type"`
	Priority        int32     `json:"priority"`
	Capabilities    []string  `json:"capabilities"`
	Supported       bool      `json:"supported"`
	Enabled         bool      `json:"enabled"`
	Running         bool      `json:"running"`
	Source          string    `json:"source"`
	InstalledAt     time.Time `json:"installed_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	BinarySize      int64     `json:"binary_size"`
	HasConfig       bool      `json:"has_config"`
	Error           string    `json:"error,omitempty"`
}

type runtimeState struct {
	Enabled     bool      `yaml:"enabled"`
	Source      string    `yaml:"source"`
	InstalledAt time.Time `yaml:"installed_at"`
	UpdatedAt   time.Time `yaml:"updated_at"`
}

type pluginManifest struct {
	ID              string   `yaml:"id"`
	Name            string   `yaml:"name"`
	Version         string   `yaml:"version"`
	ProtocolVersion string   `yaml:"protocol_version"`
	Description     string   `yaml:"description"`
	Author          string   `yaml:"author"`
	Type            string   `yaml:"type"`
	Priority        int32    `yaml:"priority"`
	Capabilities    []string `yaml:"capabilities"`
}

// ValidatePluginID 限制目录名只使用不可逃逸的短 ID。
func ValidatePluginID(id string) error {
	if !pluginIDPattern.MatchString(id) {
		return fmt.Errorf("%w: 仅允许 1-64 位字母、数字、短横线和下划线，且必须以字母或数字开头", ErrInvalidPluginID)
	}
	return nil
}

// ListInstalled 从文件系统读取已安装插件，不依赖数据库。
func (m *Manager) ListInstalled() ([]PluginStatus, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	entries, err := os.ReadDir(m.pluginDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []PluginStatus{}, nil
		}
		return nil, fmt.Errorf("读取插件目录失败: %w", err)
	}

	result := make([]PluginStatus, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || ValidatePluginID(entry.Name()) != nil {
			continue
		}
		id := entry.Name()
		binaryPath := filepath.Join(m.pluginDir, id, pluginBinaryName(id))
		binaryInfo, err := os.Stat(binaryPath)
		if err != nil || !binaryInfo.Mode().IsRegular() {
			continue
		}

		state, stateErr := m.readRuntimeState(id, binaryInfo.ModTime())
		manifest, manifestErr := m.readManifest(id)
		status := PluginStatus{
			ID:           id,
			Name:         id,
			Capabilities: []string{},
			Enabled:      state.Enabled,
			Source:       state.Source,
			InstalledAt:  state.InstalledAt,
			UpdatedAt:    state.UpdatedAt,
			BinarySize:   binaryInfo.Size(),
			HasConfig:    regularFileExists(filepath.Join(m.pluginDir, id, "config.yaml")),
		}
		if manifestErr == nil {
			applyManifest(&status, manifest)
		}
		if active := m.instanceByID(id); active != nil {
			status.Running = true
			applyPluginInfo(&status, active.info)
		}
		status.Error = m.lastError(id)
		if stateErr != nil {
			status.Error = stateErr.Error()
		} else if manifestErr != nil && !os.IsNotExist(manifestErr) {
			status.Error = manifestErr.Error()
		}
		result = append(result, status)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

// InstallBinary 校验并安装一个通用插件二进制。安装完成后始终保持停用，
// 由管理员确认配置后再显式启用。
func (m *Manager) InstallBinary(ctx context.Context, requestedID, source string, reader io.Reader, configText string) (PluginStatus, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	requestedID = strings.TrimSpace(requestedID)
	if requestedID != "" {
		if err := ValidatePluginID(requestedID); err != nil {
			return PluginStatus{}, err
		}
	}
	if reader == nil {
		return PluginStatus{}, fmt.Errorf("插件二进制不能为空")
	}
	if len(configText) > MaxPluginConfigSize {
		return PluginStatus{}, fmt.Errorf("插件配置不能超过 1MB")
	}

	if err := os.MkdirAll(m.pluginDir, 0o755); err != nil {
		return PluginStatus{}, fmt.Errorf("创建插件目录失败: %w", err)
	}
	tempDir, err := os.MkdirTemp(m.pluginDir, ".install-")
	if err != nil {
		return PluginStatus{}, fmt.Errorf("创建安装临时目录失败: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	tempBinary := filepath.Join(tempDir, "plugin-candidate")
	file, err := os.OpenFile(tempBinary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return PluginStatus{}, fmt.Errorf("创建插件二进制失败: %w", err)
	}
	written, copyErr := io.Copy(file, io.LimitReader(reader, MaxPluginBinarySize+1))
	closeErr := file.Close()
	if copyErr != nil {
		return PluginStatus{}, fmt.Errorf("写入插件二进制失败: %w", copyErr)
	}
	if closeErr != nil {
		return PluginStatus{}, fmt.Errorf("关闭插件二进制失败: %w", closeErr)
	}
	if written == 0 {
		return PluginStatus{}, fmt.Errorf("插件二进制不能为空")
	}
	if written > MaxPluginBinarySize {
		return PluginStatus{}, fmt.Errorf("插件二进制不能超过 500MB")
	}

	tempConfig := filepath.Join(tempDir, "config.yaml")
	if err := atomicWriteFile(tempConfig, []byte(configText), 0o600); err != nil {
		return PluginStatus{}, fmt.Errorf("写入插件配置失败: %w", err)
	}
	// 安装阶段只做握手、协议和配置校验，不调用 Start，确保默认停用的插件不会产生运行副作用。
	inst, err := m.launchPlugin(ctx, requestedID, exec.Command(tempBinary), tempConfig, false)
	if err != nil {
		return PluginStatus{}, fmt.Errorf("校验插件失败: %w", err)
	}
	stopPlugin(inst, context.Background())

	id := inst.info.ID
	if id == "" {
		id = requestedID
	}
	if err := ValidatePluginID(id); err != nil {
		return PluginStatus{}, err
	}
	destination := filepath.Join(m.pluginDir, id)
	if _, err := os.Stat(destination); err == nil {
		return PluginStatus{}, fmt.Errorf("%w: %s", ErrPluginExists, id)
	} else if !os.IsNotExist(err) {
		return PluginStatus{}, fmt.Errorf("检查插件目录失败: %w", err)
	}

	finalBinary := filepath.Join(tempDir, pluginBinaryName(id))
	if err := os.Rename(tempBinary, finalBinary); err != nil {
		return PluginStatus{}, fmt.Errorf("整理插件二进制失败: %w", err)
	}
	now := time.Now().UTC()
	manifest := manifestFromInfo(inst.info)
	manifestData, err := yaml.Marshal(manifest)
	if err != nil {
		return PluginStatus{}, fmt.Errorf("编码插件元信息失败: %w", err)
	}
	if err := atomicWriteFile(filepath.Join(tempDir, "manifest.yaml"), manifestData, 0o600); err != nil {
		return PluginStatus{}, fmt.Errorf("保存插件元信息失败: %w", err)
	}
	state := runtimeState{Enabled: false, Source: strings.TrimSpace(source), InstalledAt: now, UpdatedAt: now}
	if state.Source == "" {
		state.Source = "upload"
	}
	stateData, err := yaml.Marshal(state)
	if err != nil {
		return PluginStatus{}, fmt.Errorf("编码插件运行状态失败: %w", err)
	}
	if err := atomicWriteFile(filepath.Join(tempDir, "runtime.yaml"), stateData, 0o600); err != nil {
		return PluginStatus{}, fmt.Errorf("保存插件运行状态失败: %w", err)
	}
	if err := os.Rename(tempDir, destination); err != nil {
		return PluginStatus{}, fmt.Errorf("提交插件安装失败: %w", err)
	}

	status := PluginStatus{
		ID: id, Enabled: false, Running: false, Source: state.Source,
		InstalledAt: now, UpdatedAt: now, BinarySize: written, HasConfig: true,
	}
	applyPluginInfo(&status, inst.info)
	return status, nil
}

// GetConfig 返回插件原始 YAML 配置。
func (m *Manager) GetConfig(id string) (string, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if err := m.ensureInstalled(id); err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(m.pluginDir, id, "config.yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("读取插件配置失败: %w", err)
	}
	if len(data) > MaxPluginConfigSize {
		return "", fmt.Errorf("插件配置超过 1MB，无法在线编辑")
	}
	return string(data), nil
}

// UpdateConfig 原子保存 YAML；运行中的插件会先启动新实例，成功后再切换。
func (m *Manager) UpdateConfig(ctx context.Context, id, configText string) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if err := m.ensureInstalled(id); err != nil {
		return err
	}
	if len(configText) > MaxPluginConfigSize {
		return fmt.Errorf("插件配置不能超过 1MB")
	}
	if err := validateConfigText(configText, m.logLevel); err != nil {
		return err
	}

	configPath := filepath.Join(m.pluginDir, id, "config.yaml")
	oldConfig, oldErr := os.ReadFile(configPath)
	if oldErr != nil && !os.IsNotExist(oldErr) {
		return fmt.Errorf("读取原插件配置失败: %w", oldErr)
	}
	if err := atomicWriteFile(configPath, []byte(configText), 0o600); err != nil {
		return fmt.Errorf("保存插件配置失败: %w", err)
	}

	state, err := m.readRuntimeState(id, time.Now().UTC())
	if err != nil {
		return err
	}
	state.UpdatedAt = time.Now().UTC()
	if !state.Enabled {
		return m.writeRuntimeState(id, state)
	}
	if err := m.reloadLocked(ctx, id); err != nil {
		if oldErr == nil {
			_ = atomicWriteFile(configPath, oldConfig, 0o600)
		} else {
			_ = os.Remove(configPath)
		}
		m.setLastError(id, err)
		return fmt.Errorf("新配置无法启动，已恢复原配置: %w", err)
	}
	return m.writeRuntimeState(id, state)
}

// SetEnabled 持久化插件运行开关并同步子进程状态。
func (m *Manager) SetEnabled(ctx context.Context, id string, enabled bool) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if err := m.ensureInstalled(id); err != nil {
		return err
	}
	binaryInfo, err := os.Stat(filepath.Join(m.pluginDir, id, pluginBinaryName(id)))
	if err != nil {
		return fmt.Errorf("读取插件二进制失败: %w", err)
	}
	state, err := m.readRuntimeState(id, binaryInfo.ModTime())
	if err != nil {
		return err
	}
	if enabled {
		started := false
		if m.instanceByID(id) == nil {
			inst, startErr := m.launchInstalled(ctx, id)
			if startErr != nil {
				m.setLastError(id, startErr)
				return startErr
			}
			m.setInstance(inst)
			started = true
		}
		state.Enabled = true
		state.UpdatedAt = time.Now().UTC()
		if err := m.writeRuntimeState(id, state); err != nil {
			if started {
				m.detachAndStop(id, context.Background())
			}
			return err
		}
		m.clearLastError(id)
		return nil
	}

	state.Enabled = false
	state.UpdatedAt = time.Now().UTC()
	if err := m.writeRuntimeState(id, state); err != nil {
		return err
	}
	m.detachAndStop(id, ctx)
	m.clearLastError(id)
	return nil
}

// Reload 重新加载已启用插件。
func (m *Manager) Reload(ctx context.Context, id string) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if err := m.ensureInstalled(id); err != nil {
		return err
	}
	state, err := m.readRuntimeState(id, time.Now().UTC())
	if err != nil {
		return err
	}
	if !state.Enabled {
		return ErrPluginDisabled
	}
	if err := m.reloadLocked(ctx, id); err != nil {
		m.setLastError(id, err)
		return err
	}
	state.UpdatedAt = time.Now().UTC()
	return m.writeRuntimeState(id, state)
}

// Uninstall 停止插件后删除其独立目录。
func (m *Manager) Uninstall(ctx context.Context, id string) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if err := m.ensureInstalled(id); err != nil {
		return err
	}
	m.detachAndStop(id, ctx)
	if err := os.RemoveAll(filepath.Join(m.pluginDir, id)); err != nil {
		return fmt.Errorf("删除插件目录失败: %w", err)
	}
	m.clearLastError(id)
	return nil
}

func (m *Manager) reloadLocked(ctx context.Context, id string) error {
	active := m.instanceByID(id)
	inst, err := m.launchInstalled(ctx, id)
	if err != nil {
		return err
	}
	m.setInstance(inst)
	if active != nil {
		stopPlugin(active, context.Background())
	}
	m.clearLastError(id)
	return nil
}

func (m *Manager) launchInstalled(ctx context.Context, id string) (*instance, error) {
	dir := filepath.Join(m.pluginDir, id)
	return m.launchPlugin(ctx, id, exec.Command(filepath.Join(dir, pluginBinaryName(id))), filepath.Join(dir, "config.yaml"), true)
}

func (m *Manager) detachAndStop(id string, ctx context.Context) {
	m.mu.Lock()
	inst := m.instances[id]
	if inst != nil {
		delete(m.instances, id)
	}
	m.mu.Unlock()
	if inst != nil {
		stopPlugin(inst, ctx)
	}
}

func (m *Manager) ensureInstalled(id string) error {
	if err := ValidatePluginID(id); err != nil {
		return err
	}
	info, err := os.Stat(filepath.Join(m.pluginDir, id, pluginBinaryName(id)))
	if err != nil {
		if os.IsNotExist(err) {
			return ErrPluginNotFound
		}
		return fmt.Errorf("检查插件失败: %w", err)
	}
	if !info.Mode().IsRegular() {
		return ErrPluginNotFound
	}
	return nil
}

func (m *Manager) readRuntimeState(id string, fallbackTime time.Time) (runtimeState, error) {
	state := runtimeState{
		Enabled:     m.defaultEnabled,
		Source:      "manual",
		InstalledAt: fallbackTime.UTC(),
		UpdatedAt:   fallbackTime.UTC(),
	}
	data, err := os.ReadFile(filepath.Join(m.pluginDir, id, "runtime.yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return runtimeState{}, fmt.Errorf("读取 runtime.yaml 失败: %w", err)
	}
	if err := yaml.Unmarshal(data, &state); err != nil {
		return runtimeState{}, fmt.Errorf("解析 runtime.yaml 失败: %w", err)
	}
	if state.Source == "" {
		state.Source = "manual"
	}
	if state.InstalledAt.IsZero() {
		state.InstalledAt = fallbackTime.UTC()
	}
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = state.InstalledAt
	}
	return state, nil
}

func (m *Manager) writeRuntimeState(id string, state runtimeState) error {
	data, err := yaml.Marshal(state)
	if err != nil {
		return fmt.Errorf("编码 runtime.yaml 失败: %w", err)
	}
	if err := atomicWriteFile(filepath.Join(m.pluginDir, id, "runtime.yaml"), data, 0o600); err != nil {
		return fmt.Errorf("保存 runtime.yaml 失败: %w", err)
	}
	return nil
}

func (m *Manager) readManifest(id string) (pluginManifest, error) {
	data, err := os.ReadFile(filepath.Join(m.pluginDir, id, "manifest.yaml"))
	if err != nil {
		return pluginManifest{}, err
	}
	var manifest pluginManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return pluginManifest{}, fmt.Errorf("解析 manifest.yaml 失败: %w", err)
	}
	return manifest, nil
}

func (m *Manager) setLastError(id string, err error) {
	if err == nil {
		return
	}
	m.mu.Lock()
	if m.lastErrors == nil {
		m.lastErrors = make(map[string]string)
	}
	m.lastErrors[id] = err.Error()
	m.mu.Unlock()
}

func (m *Manager) clearLastError(id string) {
	m.mu.Lock()
	delete(m.lastErrors, id)
	m.mu.Unlock()
}

func (m *Manager) lastError(id string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastErrors[id]
}

func manifestFromInfo(info protocol.PluginInfo) pluginManifest {
	return pluginManifest{
		ID: info.ID, Name: info.Name, Version: info.Version, ProtocolVersion: info.ProtocolVersion,
		Description: info.Description, Author: info.Author, Type: info.Type, Priority: info.Priority,
		Capabilities: append([]string(nil), info.Capabilities...),
	}
}

func applyManifest(status *PluginStatus, manifest pluginManifest) {
	status.Name = manifest.Name
	status.Version = manifest.Version
	status.ProtocolVersion = manifest.ProtocolVersion
	status.Description = manifest.Description
	status.Author = manifest.Author
	status.Type = manifest.Type
	status.Priority = manifest.Priority
	status.Capabilities = append([]string{}, manifest.Capabilities...)
	status.Supported = supportsAnyCapability(status.Capabilities)
	if status.Name == "" {
		status.Name = status.ID
	}
}

func applyPluginInfo(status *PluginStatus, info protocol.PluginInfo) {
	applyManifest(status, manifestFromInfo(info))
}

func supportsAnyCapability(capabilities []string) bool {
	for _, capability := range capabilities {
		if capability == protocol.CapabilityRelayHookV1 {
			return true
		}
	}
	return false
}

func regularFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func validateConfigText(configText, logLevel string) error {
	tempDir, err := os.MkdirTemp("", "airgate-plugin-config-")
	if err != nil {
		return fmt.Errorf("创建配置校验目录失败: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()
	path := filepath.Join(tempDir, "config.yaml")
	if err := os.WriteFile(path, []byte(configText), 0o600); err != nil {
		return fmt.Errorf("写入待校验配置失败: %w", err)
	}
	if _, err := loadPluginConfig(path, logLevel); err != nil {
		return err
	}
	return nil
}

func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".tmp-")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, path)
}
