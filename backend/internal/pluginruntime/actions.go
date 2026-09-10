package pluginruntime

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/pluginruntime/protocol"
)

const (
	// MaxPluginActionBodySize 是管理员调用插件管理动作时允许的最大请求体。
	MaxPluginActionBodySize     = 1 << 20
	maxPluginActionResponseSize = 4 << 20
)

// InvokeManagement 调用已运行插件的管理动作。动作名会映射到插件内部的
// /management/<action> 路径，浏览器不会直接连接插件进程。
func (m *Manager) InvokeManagement(ctx context.Context, id, action string, body []byte) (protocol.Response, error) {
	if err := ValidatePluginID(id); err != nil {
		return protocol.Response{}, err
	}
	if len(body) > MaxPluginActionBodySize {
		return protocol.Response{}, fmt.Errorf("插件动作请求体超过 %d 字节限制", MaxPluginActionBodySize)
	}
	actionPath, err := normalizeManagementAction(action)
	if err != nil {
		return protocol.Response{}, err
	}

	inst := m.instanceByID(id)
	if inst == nil {
		if err := m.ensureInstalled(id); err != nil {
			return protocol.Response{}, err
		}
		return protocol.Response{}, ErrPluginDisabled
	}
	if !supportsManagementActions(inst.info) {
		return protocol.Response{}, ErrPluginCapabilityUnsupported
	}
	if !inst.acquireCall(time.Now()) {
		return protocol.Response{}, ErrPluginUnavailable
	}
	defer inst.calls.Done()

	result, callErr := inst.plugin.Handle(ctx, protocol.Request{
		Method: http.MethodPost,
		Path:   actionPath,
		Header: map[string][]string{"Content-Type": {"application/json"}},
		Body:   append([]byte(nil), body...),
	})
	if callErr != nil {
		return protocol.Response{}, m.recordFailure(inst, sanitizedPluginCallError("调用插件管理动作失败", callErr))
	}
	if result.StatusCode < 200 || result.StatusCode > 599 {
		return protocol.Response{}, m.recordFailure(inst, fmt.Errorf("插件管理动作返回非法状态码 %d", result.StatusCode))
	}
	if len(result.Body) > maxPluginActionResponseSize {
		return protocol.Response{}, m.recordFailure(inst, fmt.Errorf("插件管理动作响应超过 %d 字节限制", maxPluginActionResponseSize))
	}
	inst.recordSuccess()
	return result, nil
}

func normalizeManagementAction(action string) (string, error) {
	action = strings.Trim(strings.TrimSpace(action), "/")
	if action == "" || strings.Contains(action, "\\") || strings.Contains(action, "?") || strings.Contains(action, "#") {
		return "", fmt.Errorf("插件管理动作路径无效")
	}
	for _, segment := range strings.Split(action, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("插件管理动作路径无效")
		}
	}
	normalized := path.Clean("/" + action)
	if normalized == "/" {
		return "", fmt.Errorf("插件管理动作路径无效")
	}
	return "/management" + normalized, nil
}

func supportsManagementActions(info protocol.PluginInfo) bool {
	return hasCapability(info, protocol.CapabilityAccountProviderManagementV1) ||
		hasCapability(info, protocol.CapabilityCodexFingerprintV1)
}
