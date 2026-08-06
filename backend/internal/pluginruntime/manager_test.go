package pluginruntime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/config"
	"github.com/DouDOU-start/airgate-core/internal/pluginruntime/protocol"
	"github.com/DouDOU-start/airgate-core/internal/relay/relayhook"
)

type fakePlugin struct {
	info    protocol.PluginInfo
	calls   atomic.Int32
	handler func(context.Context, protocol.Request) (protocol.Response, error)
}

func (f *fakePlugin) Info() protocol.PluginInfo { return f.info }
func (f *fakePlugin) Init(context.Context, map[string]string) error {
	return nil
}
func (f *fakePlugin) Start(context.Context) error { return nil }
func (f *fakePlugin) Stop(context.Context) error  { return nil }
func (f *fakePlugin) Handle(ctx context.Context, request protocol.Request) (protocol.Response, error) {
	f.calls.Add(1)
	return f.handler(ctx, request)
}

func testRelayRequest(accountIDs ...int) relayhook.Request {
	candidates := make([]relayhook.Candidate, 0, len(accountIDs))
	for _, id := range accountIDs {
		candidates = append(candidates, relayhook.Candidate{Kind: "account", ID: id})
	}
	return relayhook.Request{
		Version:    relayhook.VersionV1,
		Model:      "gpt-test",
		Body:       json.RawMessage(`{"model":"gpt-test","stream":false,"input":"你好"}`),
		Candidates: candidates,
	}
}

func testInstance(id string, priority int32, plugin *fakePlugin) *instance {
	plugin.info = protocol.PluginInfo{
		ID:              id,
		Name:            id,
		ProtocolVersion: protocol.ProtocolVersion,
		Priority:        priority,
		Capabilities:    []string{protocol.CapabilityRelayHookV1},
	}
	return &instance{id: id, name: id, info: plugin.info, plugin: plugin}
}

func TestBeforeDispatchDecodesDecision(t *testing.T) {
	plugin := &fakePlugin{handler: func(context.Context, protocol.Request) (protocol.Response, error) {
		return protocol.Response{StatusCode: http.StatusOK, Body: []byte(`{"version":"v1","route":{"account_ids":[2,3],"fallback":"core"}}`)}, nil
	}}
	manager := &Manager{
		hookTimeout: time.Second,
		instances:   map[string]*instance{"route": testInstance("route", 10, plugin)},
		lastErrors:  make(map[string]string),
	}
	decision, err := manager.BeforeDispatch(context.Background(), testRelayRequest(2, 3))
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	if decision.Route == nil || len(decision.Route.AccountIDs) != 2 || decision.Route.AccountIDs[0] != 2 {
		t.Fatalf("决策解析异常: %+v", decision)
	}
}

func TestBeforeDispatchChainsPluginsByPriority(t *testing.T) {
	first := &fakePlugin{handler: func(_ context.Context, request protocol.Request) (protocol.Response, error) {
		var hookRequest relayhook.Request
		if err := json.Unmarshal(request.Body, &hookRequest); err != nil {
			return protocol.Response{}, err
		}
		var body map[string]any
		_ = json.Unmarshal(hookRequest.Body, &body)
		body["first"] = true
		replaced, _ := json.Marshal(body)
		decision, _ := json.Marshal(relayhook.Decision{
			Version:     relayhook.VersionV1,
			RequestBody: replaced,
			Route:       &relayhook.RoutePlan{AccountIDs: []int{2}, Fallback: relayhook.FallbackCore},
		})
		return protocol.Response{StatusCode: http.StatusOK, Body: decision}, nil
	}}
	second := &fakePlugin{handler: func(_ context.Context, request protocol.Request) (protocol.Response, error) {
		var hookRequest relayhook.Request
		if err := json.Unmarshal(request.Body, &hookRequest); err != nil {
			return protocol.Response{}, err
		}
		var body map[string]any
		_ = json.Unmarshal(hookRequest.Body, &body)
		if body["first"] != true {
			return protocol.Response{}, errors.New("未收到前一个插件改写后的请求体")
		}
		body["second"] = true
		replaced, _ := json.Marshal(body)
		decision, _ := json.Marshal(relayhook.Decision{
			Version:     relayhook.VersionV1,
			RequestBody: replaced,
			Route:       &relayhook.RoutePlan{AccountIDs: []int{3}, Fallback: relayhook.FallbackCore},
		})
		return protocol.Response{StatusCode: http.StatusOK, Body: decision}, nil
	}}
	manager := &Manager{
		hookTimeout: time.Second,
		instances: map[string]*instance{
			"second": testInstance("second", 20, second),
			"first":  testInstance("first", 10, first),
		},
		lastErrors: make(map[string]string),
	}
	decision, err := manager.BeforeDispatch(context.Background(), testRelayRequest(2, 3))
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(decision.RequestBody, &body); err != nil {
		t.Fatalf("解析链式请求体失败: %v", err)
	}
	if body["first"] != true || body["second"] != true {
		t.Fatalf("链式请求体不完整: %s", decision.RequestBody)
	}
	if decision.Route == nil || len(decision.Route.AccountIDs) != 1 || decision.Route.AccountIDs[0] != 3 {
		t.Fatalf("最后一个非空路由未生效: %+v", decision.Route)
	}
}

func TestBeforeDispatchSkipsFailedPlugin(t *testing.T) {
	success := &fakePlugin{handler: func(context.Context, protocol.Request) (protocol.Response, error) {
		return protocol.Response{StatusCode: http.StatusOK, Body: []byte(`{"version":"v1","route":{"account_ids":[2],"fallback":"core"}}`)}, nil
	}}
	failed := &fakePlugin{handler: func(context.Context, protocol.Request) (protocol.Response, error) {
		return protocol.Response{}, errors.New("连接已断开")
	}}
	manager := &Manager{
		hookTimeout: time.Second,
		instances: map[string]*instance{
			"success": testInstance("success", 10, success),
			"failed":  testInstance("failed", 20, failed),
		},
		lastErrors: make(map[string]string),
	}
	decision, err := manager.BeforeDispatch(context.Background(), testRelayRequest(2))
	if err != nil {
		t.Fatalf("单个插件失败不应使整条链失败: %v", err)
	}
	if decision.Route == nil || decision.Route.AccountIDs[0] != 2 {
		t.Fatalf("前一个插件的成功结果被丢弃: %+v", decision)
	}
	if manager.lastError("failed") == "" {
		t.Fatal("失败插件没有记录可观测错误")
	}
}

func TestBeforeDispatchTimeoutAndCircuitBreaker(t *testing.T) {
	plugin := &fakePlugin{handler: func(ctx context.Context, _ protocol.Request) (protocol.Response, error) {
		<-ctx.Done()
		return protocol.Response{}, ctx.Err()
	}}
	manager := &Manager{
		hookTimeout: time.Millisecond,
		instances:   map[string]*instance{"slow": testInstance("slow", 10, plugin)},
		lastErrors:  make(map[string]string),
	}
	for index := 0; index < circuitFailureLimit; index++ {
		_, _ = manager.BeforeDispatch(context.Background(), testRelayRequest())
	}
	before := plugin.calls.Load()
	decision, err := manager.BeforeDispatch(context.Background(), testRelayRequest())
	if err != nil {
		t.Fatalf("熔断期间应静默跳过: %v", err)
	}
	if decision.Version != "" || plugin.calls.Load() != before {
		t.Fatalf("熔断期间仍调用了插件: calls=%d -> %d", before, plugin.calls.Load())
	}
}

func TestLoadPluginConfigEncodesStructuredRulesAsJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := []byte("enabled: true\nrules:\n  - group_ids: [12]\n    clients: [codex]\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	values, err := loadPluginConfig(path, "debug")
	if err != nil {
		t.Fatal(err)
	}
	if values["enabled"] != "true" || values[protocol.ConfigKeyLogLevel] != "debug" {
		t.Fatalf("标量配置异常: %+v", values)
	}
	if values["rules"] != `[{"clients":["codex"],"group_ids":[12]}]` {
		t.Fatalf("结构化规则未编码为 JSON: %s", values["rules"])
	}
}

func TestManagerLoadsRealPluginProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("短测试模式跳过真实插件进程构建")
	}
	pluginDir := t.TempDir()
	installDir := filepath.Join(pluginDir, "fixture-hook")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	binaryName := "fixture-hook"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(installDir, binaryName)
	cmd := exec.Command("go", "build", "-o", binaryPath, "./testdata/hookplugin")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("构建测试插件失败: %v\n%s", err, output)
	}

	manager := New(config.PluginsConfig{Enabled: true, Dir: pluginDir, HookTimeoutMS: 500}, "error")
	if err := manager.LoadAll(context.Background()); err != nil {
		t.Fatalf("加载真实插件进程失败: %v", err)
	}
	t.Cleanup(func() { manager.StopAll(context.Background()) })
	decision, err := manager.BeforeDispatch(context.Background(), testRelayRequest(7))
	if err != nil {
		t.Fatalf("真实插件 RPC 调用失败: %v", err)
	}
	if decision.Route == nil || len(decision.Route.AccountIDs) != 1 || decision.Route.AccountIDs[0] != 7 {
		t.Fatalf("真实插件决策异常: %+v", decision)
	}
}

func TestManagerWebManagementLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("短测试模式跳过真实插件进程构建")
	}
	buildDir := t.TempDir()
	sourceBinary := filepath.Join(buildDir, "fixture-source")
	if runtime.GOOS == "windows" {
		sourceBinary += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", sourceBinary, "./testdata/hookplugin")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("构建测试插件失败: %v\n%s", err, output)
	}

	pluginDir := t.TempDir()
	manager := New(config.PluginsConfig{Dir: pluginDir, HookTimeoutMS: 500}, "error")
	t.Cleanup(func() { manager.StopAll(context.Background()) })

	file, err := os.Open(sourceBinary)
	if err != nil {
		t.Fatal(err)
	}
	installed, err := manager.InstallBinary(context.Background(), "", "upload:fixture-source", file, "enabled: false\n")
	_ = file.Close()
	if err != nil {
		t.Fatalf("安装插件失败: %v", err)
	}
	if installed.ID != "fixture-hook" || installed.Enabled || installed.Running {
		t.Fatalf("安装状态异常: %+v", installed)
	}
	if installed.ProtocolVersion != protocol.ProtocolVersion || !installed.Supported {
		t.Fatalf("插件协议或能力状态异常: %+v", installed)
	}

	items, err := manager.ListInstalled()
	if err != nil || len(items) != 1 || items[0].Name != "测试 Relay Hook" {
		t.Fatalf("插件列表异常: items=%+v err=%v", items, err)
	}
	configText, err := manager.GetConfig("fixture-hook")
	if err != nil || configText != "enabled: false\n" {
		t.Fatalf("读取配置异常: %q, %v", configText, err)
	}

	if err := manager.SetEnabled(context.Background(), "fixture-hook", true); err != nil {
		t.Fatalf("启用插件失败: %v", err)
	}
	decision, err := manager.BeforeDispatch(context.Background(), testRelayRequest(7))
	if err != nil || decision.Route == nil || decision.Route.AccountIDs[0] != 7 {
		t.Fatalf("启用后调用异常: decision=%+v err=%v", decision, err)
	}
	if err := manager.UpdateConfig(context.Background(), "fixture-hook", "enabled: true\n"); err != nil {
		t.Fatalf("保存并重载配置失败: %v", err)
	}
	if err := manager.Reload(context.Background(), "fixture-hook"); err != nil {
		t.Fatalf("手动重载失败: %v", err)
	}

	if err := manager.SetEnabled(context.Background(), "fixture-hook", false); err != nil {
		t.Fatalf("停用插件失败: %v", err)
	}
	items, err = manager.ListInstalled()
	if err != nil || len(items) != 1 || items[0].Enabled || items[0].Running {
		t.Fatalf("停用状态异常: items=%+v err=%v", items, err)
	}
	if err := manager.Uninstall(context.Background(), "fixture-hook"); err != nil {
		t.Fatalf("卸载插件失败: %v", err)
	}
	items, err = manager.ListInstalled()
	if err != nil || len(items) != 0 {
		t.Fatalf("卸载后列表异常: items=%+v err=%v", items, err)
	}
}

func TestManagerRunsMultipleInstalledPlugins(t *testing.T) {
	if testing.Short() {
		t.Skip("短测试模式跳过多个真实插件进程构建")
	}
	buildFixture := func(id string) string {
		path := filepath.Join(t.TempDir(), id)
		if runtime.GOOS == "windows" {
			path += ".exe"
		}
		cmd := exec.Command("go", "build", "-ldflags", "-X main.fixturePluginID="+id, "-o", path, "./testdata/hookplugin")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("构建插件 %s 失败: %v\n%s", id, err, output)
		}
		return path
	}

	manager := New(config.PluginsConfig{Dir: t.TempDir(), HookTimeoutMS: 500}, "error")
	t.Cleanup(func() { manager.StopAll(context.Background()) })
	for _, id := range []string{"fixture-a", "fixture-b"} {
		file, err := os.Open(buildFixture(id))
		if err != nil {
			t.Fatal(err)
		}
		_, installErr := manager.InstallBinary(context.Background(), "", "test", file, "enabled: true\n")
		_ = file.Close()
		if installErr != nil {
			t.Fatalf("安装插件 %s 失败: %v", id, installErr)
		}
		if err := manager.SetEnabled(context.Background(), id, true); err != nil {
			t.Fatalf("启用插件 %s 失败: %v", id, err)
		}
	}

	items, err := manager.ListInstalled()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || !items[0].Running || !items[1].Running {
		t.Fatalf("多个插件未同时运行: %+v", items)
	}
	if got := len(manager.instancesFor(protocol.CapabilityRelayHookV1)); got != 2 {
		t.Fatalf("Relay Hook 链实例数 = %d，期望 2", got)
	}
	if err := manager.SetEnabled(context.Background(), "fixture-a", false); err != nil {
		t.Fatal(err)
	}
	if manager.instanceByID("fixture-a") != nil || manager.instanceByID("fixture-b") == nil {
		t.Fatal("停用一个插件影响了其他运行实例")
	}
}
