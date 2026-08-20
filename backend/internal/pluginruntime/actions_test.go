package pluginruntime

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/DouDOU-start/airgate-core/internal/pluginruntime/protocol"
)

func TestInvokeManagementCallsRunningManagementPlugin(t *testing.T) {
	plugin := &fakePlugin{handler: func(_ context.Context, request protocol.Request) (protocol.Response, error) {
		if request.Method != http.MethodPost || request.Path != "/management/orders/status" {
			t.Fatalf("管理动作请求异常: %+v", request)
		}
		if string(request.Body) != `{"order_id":"order-1"}` {
			t.Fatalf("管理动作请求体异常: %s", request.Body)
		}
		return protocol.Response{StatusCode: http.StatusAccepted, Body: []byte(`{"pending":true}`)}, nil
	}}
	inst := testManagementInstance("provider", plugin)
	manager := &Manager{
		instances:  map[string]*instance{"provider": inst},
		lastErrors: make(map[string]string),
	}
	response, err := manager.InvokeManagement(context.Background(), "provider", "orders/status", []byte(`{"order_id":"order-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusAccepted || string(response.Body) != `{"pending":true}` {
		t.Fatalf("管理动作响应异常: %+v", response)
	}
}

func TestInvokeManagementRejectsDisabledPlugin(t *testing.T) {
	dir := t.TempDir()
	id := "disabled-provider"
	pluginDir := filepath.Join(dir, id)
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, pluginBinaryName(id)), []byte("fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{pluginDir: dir, instances: map[string]*instance{}, lastErrors: make(map[string]string)}
	_, err := manager.InvokeManagement(context.Background(), id, "overview", []byte(`{}`))
	if !errors.Is(err, ErrPluginDisabled) {
		t.Fatalf("停用插件错误 = %v，期望 ErrPluginDisabled", err)
	}
}

func TestInvokeManagementRejectsUnsupportedOrUnavailablePlugin(t *testing.T) {
	unsupported := testInstance("relay-only", 10, &fakePlugin{handler: func(context.Context, protocol.Request) (protocol.Response, error) {
		return protocol.Response{StatusCode: http.StatusOK}, nil
	}})
	unavailable := testManagementInstance("unavailable", &fakePlugin{handler: func(context.Context, protocol.Request) (protocol.Response, error) {
		return protocol.Response{StatusCode: http.StatusOK}, nil
	}})
	unavailable.stopping = true
	manager := &Manager{
		instances: map[string]*instance{
			"relay-only":  unsupported,
			"unavailable": unavailable,
		},
		lastErrors: make(map[string]string),
	}
	if _, err := manager.InvokeManagement(context.Background(), "relay-only", "overview", []byte(`{}`)); !errors.Is(err, ErrPluginCapabilityUnsupported) {
		t.Fatalf("普通插件错误 = %v，期望 ErrPluginCapabilityUnsupported", err)
	}
	if _, err := manager.InvokeManagement(context.Background(), "unavailable", "overview", []byte(`{}`)); !errors.Is(err, ErrPluginUnavailable) {
		t.Fatalf("不可用插件错误 = %v，期望 ErrPluginUnavailable", err)
	}
}

func TestNormalizeManagementActionRejectsTraversal(t *testing.T) {
	for _, action := range []string{"", "../overview", "orders/../overview", "orders//status", `orders\\status`, "overview?secret=1"} {
		if _, err := normalizeManagementAction(action); err == nil {
			t.Fatalf("非法动作路径未被拒绝: %q", action)
		}
	}
	if got, err := normalizeManagementAction("orders/status"); err != nil || got != "/management/orders/status" {
		t.Fatalf("合法动作路径结果 = %q, %v", got, err)
	}
}

func testManagementInstance(id string, plugin *fakePlugin) *instance {
	plugin.info = protocol.PluginInfo{
		ID: id, Name: id, ProtocolVersion: protocol.ProtocolVersion,
		Capabilities: []string{protocol.CapabilityAccountProviderManagementV1},
	}
	return &instance{id: id, name: id, info: plugin.info, plugin: plugin, started: true}
}
