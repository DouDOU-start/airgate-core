package pluginruntime

import (
	"bytes"
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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/config"
	"github.com/DouDOU-start/airgate-core/internal/pluginruntime/protocol"
	"github.com/DouDOU-start/airgate-core/internal/relay/accounttesthook"
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
		candidates = append(candidates, relayhook.Candidate{Kind: "account", ID: id, Platform: "codex", Type: "oauth", State: "active"})
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

func testAccountTransformInstance(id string, priority int32, plugin *fakePlugin) *instance {
	plugin.info = protocol.PluginInfo{
		ID:              id,
		Name:            id,
		ProtocolVersion: protocol.ProtocolVersion,
		Priority:        priority,
		Capabilities:    []string{protocol.CapabilityAccountTestTransformV1},
	}
	return &instance{id: id, name: id, info: plugin.info, plugin: plugin}
}

func testProviderAttemptInstance(id string, priority int32, plugin *fakePlugin) *instance {
	plugin.info = protocol.PluginInfo{
		ID:              id,
		Name:            id,
		ProtocolVersion: protocol.ProtocolVersion,
		Priority:        priority,
		Capabilities:    []string{protocol.CapabilityProviderAttemptTransformV1},
	}
	return &instance{id: id, name: id, info: plugin.info, plugin: plugin}
}

func testCodexInstance(id string, priority int32, mode string) *instance {
	plugin := &fakePlugin{}
	plugin.info = protocol.PluginInfo{
		ID:              id,
		Name:            id,
		ProtocolVersion: protocol.ProtocolVersion,
		Priority:        priority,
		Capabilities:    []string{protocol.CapabilityCodexExecutorV1},
	}
	return &instance{id: id, name: id, info: plugin.info, plugin: plugin, started: true, codexMode: mode}
}

func TestTransformProviderAttemptChainsPluginsByPriority(t *testing.T) {
	transform := func(key string, wantPrevious string) *fakePlugin {
		return &fakePlugin{handler: func(_ context.Context, request protocol.Request) (protocol.Response, error) {
			if request.Path != relayhook.ProviderAttemptPath {
				return protocol.Response{}, fmt.Errorf("unexpected path %q", request.Path)
			}
			var attempt relayhook.ProviderAttemptRequest
			if err := json.Unmarshal(request.Body, &attempt); err != nil {
				return protocol.Response{}, err
			}
			if attempt.Client != "codex" || attempt.Account.Platform != "codex" || attempt.Account.Type != "oauth" {
				return protocol.Response{}, fmt.Errorf("attempt metadata was not preserved: %+v", attempt)
			}
			var body map[string]any
			if err := json.Unmarshal(attempt.Body, &body); err != nil {
				return protocol.Response{}, err
			}
			if wantPrevious != "" && body[wantPrevious] != true {
				return protocol.Response{}, fmt.Errorf("previous transform %q is missing from %s", wantPrevious, attempt.Body)
			}
			body[key] = true
			replaced, err := json.Marshal(body)
			if err != nil {
				return protocol.Response{}, err
			}
			decision, err := json.Marshal(relayhook.ProviderAttemptDecision{Version: relayhook.VersionV1, RequestBody: replaced})
			if err != nil {
				return protocol.Response{}, err
			}
			return protocol.Response{StatusCode: http.StatusOK, Body: decision}, nil
		}}
	}

	first := transform("first", "")
	second := transform("second", "first")
	manager := &Manager{
		hookTimeout: time.Second,
		instances: map[string]*instance{
			"second": testProviderAttemptInstance("second", 20, second),
			"first":  testProviderAttemptInstance("first", 10, first),
		},
		lastErrors: make(map[string]string),
	}
	request := relayhook.ProviderAttemptRequest{
		Version: relayhook.VersionV1, Client: "codex", Endpoint: "responses", Protocol: "openai",
		Model: "gpt-test", Stream: false, Body: json.RawMessage(`{"model":"gpt-test","stream":false,"input":"hello"}`),
		Account: relayhook.Candidate{Kind: "account", ID: 7, Platform: "codex", Type: "oauth", State: "active"},
	}

	decision, err := manager.TransformProviderAttempt(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(decision.RequestBody, &got); err != nil {
		t.Fatal(err)
	}
	if got["first"] != true || got["second"] != true {
		t.Fatalf("provider-attempt chain result = %s", decision.RequestBody)
	}
	if first.calls.Load() != 1 || second.calls.Load() != 1 {
		t.Fatalf("plugin calls = first:%d second:%d", first.calls.Load(), second.calls.Load())
	}
	if bytes.Contains(request.Body, []byte(`"first"`)) || bytes.Contains(request.Body, []byte(`"second"`)) {
		t.Fatalf("manager mutated caller-owned body: %s", request.Body)
	}
}

func TestTransformProviderAttemptSkipsInvalidReplacementAndContinues(t *testing.T) {
	invalid := &fakePlugin{handler: func(context.Context, protocol.Request) (protocol.Response, error) {
		return protocol.Response{StatusCode: http.StatusOK, Body: []byte(`{"version":"v1","request_body":{"model":"changed","stream":false}}`)}, nil
	}}
	valid := &fakePlugin{handler: func(_ context.Context, request protocol.Request) (protocol.Response, error) {
		var attempt relayhook.ProviderAttemptRequest
		if err := json.Unmarshal(request.Body, &attempt); err != nil {
			return protocol.Response{}, err
		}
		if bytes.Contains(attempt.Body, []byte("changed")) {
			return protocol.Response{}, errors.New("invalid replacement leaked into the chain")
		}
		return protocol.Response{StatusCode: http.StatusOK, Body: []byte(`{"version":"v1","request_body":{"model":"gpt-test","stream":false,"input":"safe"}}`)}, nil
	}}
	manager := &Manager{
		hookTimeout: time.Second,
		instances: map[string]*instance{
			"invalid": testProviderAttemptInstance("invalid", 10, invalid),
			"valid":   testProviderAttemptInstance("valid", 20, valid),
		},
		lastErrors: make(map[string]string),
	}
	decision, err := manager.TransformProviderAttempt(context.Background(), relayhook.ProviderAttemptRequest{
		Version: relayhook.VersionV1, Model: "gpt-test", Stream: false,
		Body: json.RawMessage(`{"model":"gpt-test","stream":false,"input":"base"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(decision.RequestBody, []byte(`"input":"safe"`)) {
		t.Fatalf("valid transform was not applied after invalid plugin: %s", decision.RequestBody)
	}
}

func TestTransformAccountTestAppliesPluginResult(t *testing.T) {
	plugin := &fakePlugin{handler: func(_ context.Context, request protocol.Request) (protocol.Response, error) {
		if request.Path != accounttesthook.TransformPath {
			return protocol.Response{}, fmt.Errorf("调用路径异常: %s", request.Path)
		}
		var transformRequest accounttesthook.Request
		if err := json.Unmarshal(request.Body, &transformRequest); err != nil {
			return protocol.Response{}, err
		}
		if transformRequest.Mode != "overage" {
			return protocol.Response{}, fmt.Errorf("测试模式异常: %s", transformRequest.Mode)
		}
		var body map[string]any
		if err := json.Unmarshal(transformRequest.Body, &body); err != nil {
			return protocol.Response{}, err
		}
		body["transformed"] = true
		replaced, _ := json.Marshal(body)
		decision, _ := json.Marshal(accounttesthook.Decision{
			Version:     accounttesthook.VersionV1,
			RequestBody: replaced,
		})
		return protocol.Response{StatusCode: http.StatusOK, Body: decision}, nil
	}}
	manager := &Manager{
		instances:  map[string]*instance{"overage": testAccountTransformInstance("overage", 10, plugin)},
		lastErrors: make(map[string]string),
	}
	decision, err := manager.TransformAccountTest(context.Background(), accounttesthook.Request{
		Version: accounttesthook.VersionV1,
		Mode:    "overage", Platform: "codex", Endpoint: "responses", Model: "gpt-test",
		Body: json.RawMessage(`{"model":"gpt-test","stream":true,"input":"你好"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(decision.RequestBody, &body); err != nil {
		t.Fatal(err)
	}
	if body["transformed"] != true {
		t.Fatalf("插件变换结果未生效: %s", decision.RequestBody)
	}
}

func TestTransformAccountTestRequiresAvailablePlugin(t *testing.T) {
	manager := &Manager{instances: map[string]*instance{}, lastErrors: make(map[string]string)}
	_, err := manager.TransformAccountTest(context.Background(), accounttesthook.Request{})
	if !errors.Is(err, accounttesthook.ErrUnavailable) {
		t.Fatalf("无插件时错误 = %v，期望 ErrUnavailable", err)
	}
}

func TestManagerCodexTransportModeUsesHighestPriorityPluginPolicy(t *testing.T) {
	manager := &Manager{instances: map[string]*instance{
		"later":   testCodexInstance("later", 200, "cpa_only"),
		"primary": testCodexInstance("primary", 100, " CPA_TRANSLATE "),
	}}
	if got := manager.CodexTransportMode(); got != "cpa_translate" {
		t.Fatalf("CodexTransportMode() = %q, want cpa_translate", got)
	}
	manager.instances["primary"].codexMode = "invalid"
	if got := manager.CodexTransportMode(); got != "auto" {
		t.Fatalf("invalid policy should fail safe to auto, got %q", got)
	}
}

func TestManagerCodexTransportModeDefaultsToAuto(t *testing.T) {
	if got := (&Manager{}).CodexTransportMode(); got != defaultCodexMode {
		t.Fatalf("empty manager mode = %q, want %q", got, defaultCodexMode)
	}
}

func TestManagerCodexTransportModeReturnsAutoAfterExecutorIsDisabled(t *testing.T) {
	pluginDir := t.TempDir()
	id := "codex-fixture"
	installDir := filepath.Join(pluginDir, id)
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, pluginBinaryName(id)), []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "runtime.yaml"), []byte("enabled: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	manager := New(config.PluginsConfig{Dir: pluginDir}, "error")
	manager.setInstance(testCodexInstance(id, 10, "native_only"))
	if got := manager.CodexTransportMode(); got != "native_only" {
		t.Fatalf("enabled executor mode = %q, want native_only", got)
	}
	if err := manager.SetEnabled(context.Background(), id, false); err != nil {
		t.Fatalf("disable executor: %v", err)
	}
	if got := manager.CodexTransportMode(); got != defaultCodexMode {
		t.Fatalf("disabled executor left stale mode %q, want %q", got, defaultCodexMode)
	}
}

func TestManagerCodexTransportModeIgnoresInstalledExecutorThatIsNotRunning(t *testing.T) {
	for _, test := range []struct {
		name    string
		enabled bool
	}{
		{name: "disabled", enabled: false},
		{name: "startup failure", enabled: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			pluginDir := t.TempDir()
			id := "codex-fixture"
			installDir := filepath.Join(pluginDir, id)
			if err := os.MkdirAll(installDir, 0o755); err != nil {
				t.Fatal(err)
			}
			// The enabled case deliberately uses an invalid executable so LoadAll
			// exercises a failed launch after reading native_only from disk.
			if err := os.WriteFile(filepath.Join(installDir, pluginBinaryName(id)), []byte("not an executable"), 0o700); err != nil {
				t.Fatal(err)
			}
			manifest := "id: " + id + "\npriority: 10\ncapabilities:\n  - " + protocol.CapabilityCodexExecutorV1 + "\n"
			if err := os.WriteFile(filepath.Join(installDir, "manifest.yaml"), []byte(manifest), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(installDir, "config.yaml"), []byte("codex_mode: native_only\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			runtimeState := fmt.Sprintf("enabled: %t\n", test.enabled)
			if err := os.WriteFile(filepath.Join(installDir, "runtime.yaml"), []byte(runtimeState), 0o600); err != nil {
				t.Fatal(err)
			}

			manager := New(config.PluginsConfig{Dir: pluginDir}, "error")
			if err := manager.LoadAll(context.Background()); err != nil {
				t.Fatalf("LoadAll: %v", err)
			}
			t.Cleanup(func() { manager.StopAll(context.Background()) })
			if manager.instanceByID(id) != nil {
				t.Fatal("non-running executor was added to the active instance set")
			}
			if got := manager.CodexTransportMode(); got != defaultCodexMode {
				t.Fatalf("non-running executor left stale mode %q, want %q", got, defaultCodexMode)
			}
		})
	}
}

func TestTransformAccountTestDoesNotExposePluginErrorBody(t *testing.T) {
	const sensitive = "注入 function call 历史失败"
	plugin := &fakePlugin{handler: func(context.Context, protocol.Request) (protocol.Response, error) {
		return protocol.Response{StatusCode: http.StatusUnprocessableEntity, Body: []byte(`{"error":"` + sensitive + `"}`)}, nil
	}}
	manager := &Manager{
		instances:  map[string]*instance{"sensitive-plugin": testAccountTransformInstance("sensitive-plugin", 10, plugin)},
		lastErrors: make(map[string]string),
	}
	_, err := manager.TransformAccountTest(context.Background(), accounttesthook.Request{
		Version: accounttesthook.VersionV1,
		Body:    json.RawMessage(`{"model":"gpt-test","stream":true,"input":"你好"}`),
	})
	if err == nil || !strings.Contains(err.Error(), "状态码 422") {
		t.Fatalf("插件错误状态未保留: %v", err)
	}
	if strings.Contains(err.Error(), sensitive) || strings.Contains(manager.lastError("sensitive-plugin"), sensitive) {
		t.Fatalf("Core 暴露了插件错误正文: err=%v last_error=%s", err, manager.lastError("sensitive-plugin"))
	}
}

func TestBeforeDispatchSanitizesPluginCallErrorInCoreLog(t *testing.T) {
	const sensitive = "function_call_secret"
	plugin := &fakePlugin{handler: func(context.Context, protocol.Request) (protocol.Response, error) {
		return protocol.Response{}, errors.New(sensitive)
	}}
	manager := &Manager{
		instances:  map[string]*instance{"airgate-codex-enhance": testInstance("airgate-codex-enhance", 10, plugin)},
		lastErrors: make(map[string]string),
	}
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)

	decision, err := manager.BeforeDispatch(context.Background(), testRelayRequest(1))
	if err != nil || decision.Version != "" {
		t.Fatalf("Relay Hook 应保持 fail-open: decision=%+v err=%v", decision, err)
	}
	output := logs.String()
	if strings.Contains(output, sensitive) || strings.Contains(output, "airgate-codex-enhance") {
		t.Fatalf("Core 日志暴露了插件身份或错误正文: %s", output)
	}
	if !strings.Contains(output, "plugin_ref=p-") || !strings.Contains(output, "error_code=plugin_error") {
		t.Fatalf("Core 日志缺少通用插件诊断字段: %s", output)
	}
}

func TestTransformAccountTestRejectsModelChange(t *testing.T) {
	plugin := &fakePlugin{handler: func(context.Context, protocol.Request) (protocol.Response, error) {
		return protocol.Response{StatusCode: http.StatusOK, Body: []byte(`{"version":"v1","request_body":{"model":"changed","stream":true,"input":"你好"}}`)}, nil
	}}
	manager := &Manager{
		instances:  map[string]*instance{"invalid": testAccountTransformInstance("invalid", 10, plugin)},
		lastErrors: make(map[string]string),
	}
	_, err := manager.TransformAccountTest(context.Background(), accounttesthook.Request{
		Version: accounttesthook.VersionV1, Mode: "overage", Model: "gpt-test",
		Body: json.RawMessage(`{"model":"gpt-test","stream":true,"input":"你好"}`),
	})
	if err == nil || !strings.Contains(err.Error(), "不得修改 model") {
		t.Fatalf("非法 model 变换错误 = %v", err)
	}
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

func TestBeforeDispatchPreservesValidatedRateLimitedAuthorization(t *testing.T) {
	plugin := &fakePlugin{handler: func(context.Context, protocol.Request) (protocol.Response, error) {
		return protocol.Response{StatusCode: http.StatusOK, Body: []byte(`{"version":"v1","route":{"account_ids":[2,3],"allow_rate_limited_account_ids":[2,3],"fallback":"core"}}`)}, nil
	}}
	manager := &Manager{
		hookTimeout: time.Second,
		instances:   map[string]*instance{"route": testInstance("route", 10, plugin)},
		lastErrors:  make(map[string]string),
	}
	request := testRelayRequest(2, 3)
	request.Candidates[0].State = "rate_limited"
	request.Candidates[1].Platform = "xai"
	request.Candidates[1].State = "rate_limited"
	decision, err := manager.BeforeDispatch(context.Background(), request)
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	if decision.Route == nil || !equalManagerIntSlice(decision.Route.AllowRateLimitedAccountIDs, []int{2}) {
		t.Fatalf("限流账号授权未正确保留: %+v", decision.Route)
	}
}

func equalManagerIntSlice(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
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

func TestCircuitHalfOpenAllowsOnlyOneProbe(t *testing.T) {
	inst := testInstance("half-open", 10, &fakePlugin{handler: func(context.Context, protocol.Request) (protocol.Response, error) {
		return protocol.Response{StatusCode: http.StatusNoContent}, nil
	}})
	inst.circuitUntil = time.Now().Add(-time.Millisecond)
	if !inst.acquireCall(time.Now()) {
		t.Fatal("熔断到期后应允许一次半开探测")
	}
	if inst.acquireCall(time.Now()) {
		t.Fatal("半开探测进行中不应放入第二个调用")
	}
	inst.recordSuccess()
	inst.calls.Done()
	if !inst.acquireCall(time.Now()) {
		t.Fatal("半开探测成功后应恢复正常调用")
	}
	inst.calls.Done()
}

func TestBeforeDispatchAutomaticallyRestartsUnhealthyPlugin(t *testing.T) {
	slow := &fakePlugin{handler: func(ctx context.Context, _ protocol.Request) (protocol.Response, error) {
		<-ctx.Done()
		return protocol.Response{}, ctx.Err()
	}}
	recovered := &fakePlugin{handler: func(context.Context, protocol.Request) (protocol.Response, error) {
		return protocol.Response{StatusCode: http.StatusOK, Body: []byte(`{"version":"v1","route":{"account_ids":[2],"fallback":"core"}}`)}, nil
	}}
	failedInst := testInstance("auto-restart", 10, slow)
	recoveredInst := testInstance("auto-restart", 10, recovered)
	restartCalled := make(chan struct{}, 1)
	failedInst.restart = func(context.Context) (*instance, error) {
		restartCalled <- struct{}{}
		return recoveredInst, nil
	}
	manager := &Manager{
		hookTimeout: 5 * time.Millisecond,
		instances:   map[string]*instance{"auto-restart": failedInst},
		lastErrors:  make(map[string]string),
	}
	for index := 0; index < circuitFailureLimit; index++ {
		_, _ = manager.BeforeDispatch(context.Background(), testRelayRequest(2))
	}
	select {
	case <-restartCalled:
	case <-time.After(time.Second):
		t.Fatal("插件熔断后未触发自动重启")
	}
	deadline := time.Now().Add(time.Second)
	for manager.instanceByID("auto-restart") != recoveredInst && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if manager.instanceByID("auto-restart") != recoveredInst {
		t.Fatal("自动重启后未切换到健康实例")
	}
	decision, err := manager.BeforeDispatch(context.Background(), testRelayRequest(2))
	if err != nil || decision.Route == nil || decision.Route.AccountIDs[0] != 2 {
		t.Fatalf("自动恢复后的调用异常: decision=%+v err=%v", decision, err)
	}
	if manager.lastError("auto-restart") != "" {
		t.Fatalf("自动恢复后仍保留错误: %s", manager.lastError("auto-restart"))
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

func TestConfigFieldValueUsesMigrationFallback(t *testing.T) {
	field := protocol.ConfigField{
		Key:         "overage_group_ids",
		FallbackKey: "group_ids",
		Default:     []int{99},
	}
	legacy := []any{12, 15}
	value, exists := configFieldValue(map[string]any{"group_ids": legacy}, field)
	if !exists || fmt.Sprint(value) != fmt.Sprint(legacy) {
		t.Fatalf("旧配置未回填到新字段: value=%v exists=%v", value, exists)
	}

	value, exists = configFieldValue(map[string]any{
		"group_ids":         legacy,
		"overage_group_ids": []any{},
	}, field)
	if !exists || !emptyConfigValue(value) {
		t.Fatalf("新字段应优先于旧字段，显式空值也不能回退: value=%v exists=%v", value, exists)
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

	failed := manager.instanceByID("fixture-hook")
	if failed == nil || failed.client == nil {
		t.Fatal("未找到待模拟故障的真实插件实例")
	}
	failed.client.Kill()
	for index := 0; index < circuitFailureLimit; index++ {
		_, _ = manager.BeforeDispatch(context.Background(), testRelayRequest(7))
	}
	deadline := time.Now().Add(5 * time.Second)
	for manager.instanceByID("fixture-hook") == failed && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if manager.instanceByID("fixture-hook") == failed {
		t.Fatal("真实插件进程退出后未自动拉起新实例")
	}
	decision, err = manager.BeforeDispatch(context.Background(), testRelayRequest(7))
	if err != nil || decision.Route == nil || len(decision.Route.AccountIDs) != 1 || decision.Route.AccountIDs[0] != 7 {
		t.Fatalf("真实插件自动恢复后的决策异常: decision=%+v err=%v", decision, err)
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
	if installed.ConfigSchema == nil || len(installed.ConfigSchema.Fields) != 1 {
		t.Fatalf("插件配置结构未写入安装状态: %+v", installed.ConfigSchema)
	}
	if installed.ConfigReady {
		t.Fatal("缺少必填分组时不应标记为可启用")
	}

	items, err := manager.ListInstalled()
	if err != nil || len(items) != 1 || items[0].Name != "测试 Relay Hook" {
		t.Fatalf("插件列表异常: items=%+v err=%v", items, err)
	}
	configText, err := manager.GetConfig("fixture-hook")
	if err != nil || configText != "enabled: false\n" {
		t.Fatalf("读取配置异常: %q, %v", configText, err)
	}
	form, err := manager.GetConfigForm("fixture-hook")
	if err != nil || form.Schema == nil || len(form.Schema.Fields) != 1 {
		t.Fatalf("读取动态配置表单异常: form=%+v err=%v", form, err)
	}
	if _, leaked := form.Values["enabled"]; leaked {
		t.Fatalf("动态表单不应返回插件未声明字段: %+v", form.Values)
	}
	if err := manager.SetEnabled(context.Background(), "fixture-hook", true); !errors.Is(err, ErrPluginConfigIncomplete) {
		t.Fatalf("配置未完成时启用错误 = %v，期望 ErrPluginConfigIncomplete", err)
	}
	if err := manager.UpdateConfigForm(context.Background(), "fixture-hook", map[string]any{}); err == nil {
		t.Fatal("必填分组为空时应拒绝保存")
	}
	if err := manager.UpdateConfigForm(context.Background(), "fixture-hook", map[string]any{
		"group_ids": []any{12, 15},
		"ignored":   "不会保存",
	}); err != nil {
		t.Fatalf("保存动态配置表单失败: %v", err)
	}
	configText, err = manager.GetConfig("fixture-hook")
	if err != nil || !strings.Contains(configText, "group_ids:") || strings.Contains(configText, "ignored") {
		t.Fatalf("结构化配置持久化异常: %q, %v", configText, err)
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

func TestManager自动补号配置保留敏感值并校验数字(t *testing.T) {
	pluginDir := t.TempDir()
	installDir := filepath.Join(pluginDir, "autofill-fixture")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	binaryPath := filepath.Join(installDir, pluginBinaryName("autofill-fixture"))
	if err := os.WriteFile(binaryPath, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `id: autofill-fixture
name: 自动补号测试插件
version: 1.0.0
protocol_version: "2"
capabilities:
  - account_autofill.v1
config_schema:
  version: "1"
  fields:
    - key: password
      label: 客户密码
      widget: text
      required: true
      secret: true
    - key: batch_size
      label: 每次补号数量
      widget: number
      required: true
      min: 1
      max: 100
`
	if err := os.WriteFile(filepath.Join(installDir, "manifest.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "config.yaml"), []byte("password: real-secret\nbatch_size: 10\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := New(config.PluginsConfig{Dir: pluginDir}, "error")
	if err := manager.writeRuntimeState("autofill-fixture", runtimeState{Enabled: false, InstalledAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	form, err := manager.GetConfigForm("autofill-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if form.Values["password"] != maskedPluginSecret {
		t.Fatalf("敏感配置未掩码: %+v", form.Values)
	}
	if err := manager.UpdateConfigForm(context.Background(), "autofill-fixture", map[string]any{
		"password":   maskedPluginSecret,
		"batch_size": "20",
	}); err != nil {
		t.Fatal(err)
	}
	configText, err := manager.GetConfig("autofill-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(configText, "real-secret") || strings.Contains(configText, maskedPluginSecret) {
		t.Fatalf("保存表单后敏感配置被覆盖: %s", configText)
	}
	if err := manager.UpdateConfigForm(context.Background(), "autofill-fixture", map[string]any{
		"password":   maskedPluginSecret,
		"batch_size": 101,
	}); err == nil {
		t.Fatal("超过上限的补号数量应被拒绝")
	}
	if !supportsAnyCapability([]string{protocol.CapabilityAccountAutofillV1}) {
		t.Fatal("Core 未识别自动补号能力")
	}
}

func TestManager只向自动补号能力注入宿主访问参数(t *testing.T) {
	manager := New(config.PluginsConfig{Dir: t.TempDir()}, "error", HostAccess{
		BaseURL: "http://127.0.0.1:9517/",
		Token:   "core-plugin-token",
	})
	autofillValues := map[string]string{
		protocol.ConfigKeyCoreBaseURL:     "http://伪造地址",
		protocol.ConfigKeyCorePluginToken: "伪造令牌",
	}
	if err := manager.preparePluginConfig(protocol.PluginInfo{
		Capabilities: []string{protocol.CapabilityAccountAutofillV1},
	}, autofillValues); err != nil {
		t.Fatal(err)
	}
	if autofillValues[protocol.ConfigKeyCoreBaseURL] != "http://127.0.0.1:9517" ||
		autofillValues[protocol.ConfigKeyCorePluginToken] != "core-plugin-token" {
		t.Fatalf("自动补号插件未收到可信宿主参数: %+v", autofillValues)
	}

	ordinaryValues := map[string]string{
		protocol.ConfigKeyCoreBaseURL:     "http://伪造地址",
		protocol.ConfigKeyCorePluginToken: "伪造令牌",
	}
	if err := manager.preparePluginConfig(protocol.PluginInfo{
		Capabilities: []string{protocol.CapabilityRelayHookV1},
	}, ordinaryValues); err != nil {
		t.Fatal(err)
	}
	if _, exists := ordinaryValues[protocol.ConfigKeyCoreBaseURL]; exists {
		t.Fatalf("普通插件获得了宿主地址: %+v", ordinaryValues)
	}
	if _, exists := ordinaryValues[protocol.ConfigKeyCorePluginToken]; exists {
		t.Fatalf("普通插件获得了宿主令牌: %+v", ordinaryValues)
	}
}

func TestManager自动补号能力缺少宿主参数时拒绝初始化(t *testing.T) {
	manager := New(config.PluginsConfig{Dir: t.TempDir()}, "error")
	err := manager.preparePluginConfig(protocol.PluginInfo{
		Capabilities: []string{protocol.CapabilityAccountAutofillV1},
	}, map[string]string{})
	if err == nil || !strings.Contains(err.Error(), "访问参数") {
		t.Fatalf("缺少宿主参数时错误 = %v", err)
	}
}

func TestManagerUpdatesBinaryWithoutLosingConfiguration(t *testing.T) {
	if testing.Short() {
		t.Skip("短测试模式跳过真实插件进程构建")
	}
	buildFixture := func(id, version string) string {
		path := filepath.Join(t.TempDir(), "fixture-source")
		if runtime.GOOS == "windows" {
			path += ".exe"
		}
		ldflags := "-X main.fixturePluginID=" + id + " -X main.fixturePluginVersion=" + version
		cmd := exec.Command("go", "build", "-ldflags", ldflags, "-o", path, "./testdata/hookplugin")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("构建测试插件失败: %v\n%s", err, output)
		}
		return path
	}
	openFixture := func(path string) *os.File {
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		return file
	}

	manager := New(config.PluginsConfig{Dir: t.TempDir(), HookTimeoutMS: 500}, "error")
	t.Cleanup(func() { manager.StopAll(context.Background()) })
	configText := "group_ids:\n  - 7\n"

	initialFile := openFixture(buildFixture("fixture-hook", "0.0.1"))
	installed, err := manager.InstallBinary(context.Background(), "", "upload:fixture-v1", initialFile, configText)
	_ = initialFile.Close()
	if err != nil {
		t.Fatalf("安装插件失败: %v", err)
	}
	if err := manager.SetEnabled(context.Background(), installed.ID, true); err != nil {
		t.Fatalf("启用插件失败: %v", err)
	}
	oldInstance := manager.instanceByID(installed.ID)

	updatedFile := openFixture(buildFixture("fixture-hook", "0.0.2"))
	updated, err := manager.UpdateBinary(context.Background(), installed.ID, "upload:fixture-v2", updatedFile)
	_ = updatedFile.Close()
	if err != nil {
		t.Fatalf("更新插件失败: %v", err)
	}
	if updated.Version != "0.0.2" || !updated.Enabled || !updated.Running {
		t.Fatalf("更新状态异常: %+v", updated)
	}
	if !updated.InstalledAt.Equal(installed.InstalledAt) || !updated.UpdatedAt.After(installed.UpdatedAt) {
		t.Fatalf("更新时间异常: installed=%s updated=%s", updated.InstalledAt, updated.UpdatedAt)
	}
	if updated.Source != "upload:fixture-v2" {
		t.Fatalf("更新来源异常: %s", updated.Source)
	}
	if current := manager.instanceByID(installed.ID); current == nil || current == oldInstance || current.info.Version != "0.0.2" {
		t.Fatalf("运行实例未切换到新版本: %+v", current)
	}
	installedBinary := filepath.Join(manager.pluginDir, installed.ID, pluginBinaryName(installed.ID))
	if info, statErr := os.Stat(installedBinary); statErr != nil || !info.Mode().IsRegular() || info.Size() != updated.BinarySize {
		t.Fatalf("更新后安装文件缺失或异常: path=%s info=%+v err=%v", installedBinary, info, statErr)
	}
	persistedConfig, err := manager.GetConfig(installed.ID)
	if err != nil || persistedConfig != configText {
		t.Fatalf("更新后配置发生变化: %q, %v", persistedConfig, err)
	}

	activeAfterUpdate := manager.instanceByID(installed.ID)
	invalidFile := openFixture(buildFixture("another-plugin", "9.9.9"))
	_, err = manager.UpdateBinary(context.Background(), installed.ID, "upload:invalid", invalidFile)
	_ = invalidFile.Close()
	if err == nil {
		t.Fatal("插件 ID 不一致时更新应失败")
	}
	if manager.instanceByID(installed.ID) != activeAfterUpdate {
		t.Fatal("失败更新替换了原运行实例")
	}
	items, listErr := manager.ListInstalled()
	if listErr != nil || len(items) != 1 || items[0].Version != "0.0.2" || !items[0].Running {
		t.Fatalf("失败更新改变了已安装版本: items=%+v err=%v", items, listErr)
	}
	persistedConfig, err = manager.GetConfig(installed.ID)
	if err != nil || persistedConfig != configText {
		t.Fatalf("失败更新改变了配置: %q, %v", persistedConfig, err)
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
		_, installErr := manager.InstallBinary(context.Background(), "", "test", file, "group_ids: [7]\n")
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
