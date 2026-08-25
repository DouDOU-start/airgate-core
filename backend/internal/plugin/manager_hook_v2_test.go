package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/plugin/hookv2"
)

const relayHookV2HelperEnv = "AIRGATE_TEST_RELAY_HOOK_V2_HELPER"

func serveRelayHookV2ProbeProcessIfRequested() {
	if os.Getenv(relayHookV2HelperEnv) != "probe" {
		return
	}
	if os.Getenv(hookv2.Handshake.MagicCookieKey) != hookv2.Handshake.MagicCookieValue {
		os.Exit(1)
	}
	hookv2.Serve(&relayHookV2HelperPlugin{})
	os.Exit(0)
}

type relayHookV2HelperPlugin struct{}

func (*relayHookV2HelperPlugin) Info() hookv2.PluginInfo {
	return hookv2.PluginInfo{
		ID:              "relay-hook-v2-helper",
		Name:            "Relay Hook v2 Helper",
		Version:         "1.0.0",
		ProtocolVersion: hookv2.ProtocolVersion,
		Priority:        42,
		Capabilities:    []string{hookv2.CapabilityRelayHookV1},
		Metadata:        map[string]string{"test": "true"},
		ConfigSchema: &hookv2.ConfigSchema{Fields: []hookv2.ConfigField{
			{Key: "enabled", Label: "Enabled", Widget: "switch", Default: true},
			{Key: "limit", Label: "Limit", Widget: "number", Default: 7},
			{Key: "rules", Label: "Rules", Widget: "textarea"},
		}},
	}
}

func (*relayHookV2HelperPlugin) Init(_ context.Context, config map[string]string) error {
	if config[hookv2.ConfigKeyLogLevel] != "warn" {
		return fmt.Errorf("log_level = %q", config[hookv2.ConfigKeyLogLevel])
	}
	if config["from_yaml"] != "loaded" || config["nested"] != `{"enabled":true}` {
		return fmt.Errorf("yaml config = %#v", config)
	}
	if _, exists := config["db_dsn"]; exists {
		return fmt.Errorf("v2 config unexpectedly contains db_dsn")
	}
	if _, exists := config["plugin_dsn"]; exists {
		return fmt.Errorf("v2 config unexpectedly contains plugin_dsn")
	}
	return nil
}

func (*relayHookV2HelperPlugin) Start(context.Context) error { return nil }
func (*relayHookV2HelperPlugin) Stop(context.Context) error  { return nil }
func (*relayHookV2HelperPlugin) Handle(_ context.Context, request hookv2.Request) (hookv2.Response, error) {
	if request.Path != relayHookV2BeforeDispatchPath {
		return hookv2.Response{StatusCode: http.StatusNotFound}, nil
	}
	var payload relayHookV2Request
	if err := json.Unmarshal(request.Body, &payload); err != nil {
		return hookv2.Response{StatusCode: http.StatusBadRequest}, nil
	}
	var body map[string]any
	if err := json.Unmarshal(payload.Body, &body); err != nil {
		return hookv2.Response{StatusCode: http.StatusBadRequest}, nil
	}
	body["input"] = "mutated-by-helper"
	replacement, err := json.Marshal(body)
	if err != nil {
		return hookv2.Response{}, err
	}
	response, err := json.Marshal(relayHookV2Decision{
		Version:     relayHookV2Version,
		RequestBody: replacement,
	})
	if err != nil {
		return hookv2.Response{}, err
	}
	return hookv2.Response{StatusCode: http.StatusOK, Body: response}, nil
}

func TestRelayHookV2HelperProcess(t *testing.T) {
	if os.Getenv(relayHookV2HelperEnv) != "1" {
		return
	}
	hookv2.Serve(&relayHookV2HelperPlugin{})
}

func TestStartPluginFallsBackToRelayHookV2WithFreshCommand(t *testing.T) {
	pluginDir := t.TempDir()
	sourceDir := t.TempDir()
	config := []byte("from_yaml: loaded\nnested:\n  enabled: true\n")
	if err := os.WriteFile(filepath.Join(sourceDir, "config.yaml"), config, 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	manager := NewManager(pluginDir, "warn", "postgres://must-not-be-injected", nil)
	cmd := exec.Command(os.Args[0], "-test.run=^TestRelayHookV2HelperProcess$")
	cmd.Dir = sourceDir
	cmd.Env = append(os.Environ(), relayHookV2HelperEnv+"=1")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	canonicalName, err := manager.startPlugin(ctx, "requested-name", cmd, "")
	if err != nil {
		t.Fatalf("startPlugin() error = %v", err)
	}
	defer manager.stopPlugin(canonicalName)
	if canonicalName != "relay-hook-v2-helper" {
		t.Fatalf("canonical name = %q", canonicalName)
	}
	instance := manager.GetInstance(canonicalName)
	if instance == nil || instance.RelayHookV2 == nil || instance.Client == nil {
		t.Fatalf("v2 instance not registered: %#v", instance)
	}
	if instance.Gateway != nil || instance.Extension != nil || instance.Middleware != nil {
		t.Fatalf("v2 instance registered SDK service: %#v", instance)
	}
	if instance.Type != "middleware" || instance.Priority != 42 {
		t.Fatalf("type/priority = %q/%d", instance.Type, instance.Priority)
	}
	wantTypes := map[string]string{"enabled": "bool", "limit": "int", "rules": "string"}
	gotTypes := make(map[string]string, len(instance.ConfigSchema))
	for _, field := range instance.ConfigSchema {
		gotTypes[field.Key] = field.Type
	}
	if !reflect.DeepEqual(gotTypes, wantTypes) {
		t.Fatalf("config schema types = %#v, want %#v", gotTypes, wantTypes)
	}

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	body := []byte(`{"model":"gpt-5.4","stream":false,"input":"original"}`)
	parsed := parseBody(body, "application/json")
	state := &forwardState{
		requestPath:       "/v1/responses",
		body:              body,
		model:             parsed.Model,
		schedulingModels:  []string{parsed.Model},
		schedulingModel:   parsed.Model,
		stream:            parsed.Stream,
		accountReq:        accountRequirementsForRequestCached(manager, "/v1/responses", parsed.Model, &parsed),
		requestedPlatform: "openai",
		keyInfo:           &auth.APIKeyInfo{KeyID: 2, UserID: 1, GroupID: 3, GroupPlatform: "openai"},
		plugin:            &PluginInstance{Name: "gateway-openai"},
	}
	(&Forwarder{manager: manager}).applyRelayHookV2(c, state)
	if bytes.Equal(state.body, body) || !bytes.Contains(state.body, []byte("mutated-by-helper")) {
		t.Fatalf("real process did not mutate body: %s", state.body)
	}
}

func TestClonePluginCommandCopiesOnlyLaunchDescription(t *testing.T) {
	original := exec.Command("example", "one", "two")
	original.Dir = "workdir"
	original.Env = []string{"A=B"}
	clone := clonePluginCommand(original)
	if clone == original {
		t.Fatal("clone reused command pointer")
	}
	if clone.Path != original.Path || clone.Dir != original.Dir || !reflect.DeepEqual(clone.Args, original.Args) || !reflect.DeepEqual(clone.Env, original.Env) {
		t.Fatalf("clone = %#v, original = %#v", clone, original)
	}
	clone.Args[1] = "changed"
	clone.Env[0] = "C=D"
	if original.Args[1] == clone.Args[1] || original.Env[0] == clone.Env[0] {
		t.Fatal("clone shares args or env backing array")
	}
}

func TestProbePluginNameFallsBackToRelayHookV2(t *testing.T) {
	binary, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatalf("read test binary: %v", err)
	}
	t.Setenv(relayHookV2HelperEnv, "probe")
	manager := NewManager(t.TempDir(), "warn", "", nil)

	name, err := manager.probePluginName("fallback-name", binary)
	if err != nil {
		t.Fatalf("probePluginName() error = %v", err)
	}
	if name != "relay-hook-v2-helper" {
		t.Fatalf("probe name = %q", name)
	}
}

func TestRelayHookV2RealBinaryLifecycle(t *testing.T) {
	binaryPath := os.Getenv("AIRGATE_TEST_RELAY_HOOK_V2_BINARY")
	if binaryPath == "" {
		t.Skip("AIRGATE_TEST_RELAY_HOOK_V2_BINARY is not set")
	}
	binary, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatalf("read real plugin binary: %v", err)
	}
	pluginDir := t.TempDir()
	manager := NewManager(pluginDir, "warn", "postgres://must-not-be-injected", nil)
	name, err := manager.probePluginName("real-relay-hook", binary)
	if err != nil {
		t.Fatalf("probe real plugin: %v", err)
	}
	if name != "airgate-codex-enhance" {
		t.Fatalf("real plugin name = %q", name)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := manager.InstallFromBinary(ctx, "real-relay-hook", binary); err != nil {
		t.Fatalf("install real plugin: %v", err)
	}
	canonicalName := name
	defer manager.stopPlugin(canonicalName)

	installedDir := filepath.Join(pluginDir, canonicalName)
	installedBinary, ok := findPluginExecutablePath(installedDir, canonicalName)
	if !ok {
		t.Fatalf("installed real plugin binary missing: %s", installedBinary)
	}
	if runtime.GOOS == "windows" && filepath.Ext(installedBinary) != ".exe" {
		t.Fatalf("installed Windows plugin path = %q, want .exe", installedBinary)
	}
	firstInstance := manager.GetInstance(canonicalName)
	if firstInstance == nil || firstInstance.RelayHookV2 == nil {
		t.Fatalf("installed real plugin instance missing: %#v", firstInstance)
	}

	config := []byte("instruction_group_ids: [33]\ninstruction_enabled: true\ninstruction_text: REAL_OVERAGE_MARKER\noverage_group_ids: []\noverage_enabled: false\nenhancement_max_body_bytes: 8388608\n")
	if err := os.WriteFile(filepath.Join(installedDir, "config.yaml"), config, 0600); err != nil {
		t.Fatalf("write real plugin config: %v", err)
	}
	if err := manager.ReloadInstance(ctx, canonicalName); err != nil {
		t.Fatalf("reload real plugin: %v", err)
	}
	instance := manager.GetInstance(canonicalName)
	if instance == nil || instance.RelayHookV2 == nil {
		t.Fatalf("reloaded real plugin instance missing: %#v", instance)
	}
	if instance == firstInstance || instance.Client == firstInstance.Client {
		t.Fatal("reload reused the previous plugin runtime")
	}

	original := json.RawMessage(`{"model":"gpt-5.4","stream":false,"input":"hello"}`)
	payload, err := json.Marshal(relayHookV2Request{
		Version:   relayHookV2Version,
		RequestID: "real-process",
		UserID:    11,
		APIKeyID:  22,
		GroupID:   33,
		Client:    "codex-cli/integration",
		Endpoint:  "responses",
		Protocol:  "openai",
		Model:     "gpt-5.4",
		Stream:    false,
		Body:      original,
		Candidates: []relayHookV2Candidate{{
			Kind: "account", ID: 101, Platform: "openai", Type: "oauth", Priority: 100, State: "active",
		}},
	})
	if err != nil {
		t.Fatalf("marshal real hook request: %v", err)
	}
	callCtx, callCancel := context.WithTimeout(context.Background(), 2*time.Second)
	response, called, err := instance.RelayHookV2.invoke(callCtx, hookv2.Request{
		Method: http.MethodPost,
		Path:   relayHookV2BeforeDispatchPath,
		Header: map[string][]string{"Content-Type": {"application/json"}},
		Body:   payload,
	})
	callCancel()
	if err != nil || !called || response.StatusCode != http.StatusOK {
		t.Fatalf("real Handle: called=%v status=%d err=%v body=%s", called, response.StatusCode, err, response.Body)
	}
	var decision relayHookV2Decision
	if err := json.Unmarshal(response.Body, &decision); err != nil {
		t.Fatalf("decode real decision: %v", err)
	}
	replacement, changed, err := normalizeRelayHookV2Body(&forwardState{model: "gpt-5.4"}, decision)
	if err != nil || !changed || !bytes.Contains(replacement, []byte("REAL_OVERAGE_MARKER")) {
		t.Fatalf("real mutation: changed=%v err=%v body=%s", changed, err, replacement)
	}

	accountTestBody, err := manager.TransformAccountTest(
		context.Background(),
		"overage",
		"openai",
		"responses",
		"gpt-5.4",
		[]byte(`{"model":"gpt-5.4","stream":true,"input":"hi"}`),
	)
	if err != nil {
		t.Fatalf("real account test transform: %v", err)
	}
	if !bytes.Contains(accountTestBody, []byte(`"type":"function_call"`)) ||
		!bytes.Contains(accountTestBody, []byte(`"type":"function_call_output"`)) {
		t.Fatalf("real account test transform missing function history: %s", accountTestBody)
	}

	manager.stopPlugin(canonicalName)
	if manager.GetInstance(canonicalName) != nil {
		t.Fatal("real plugin still registered after Stop")
	}
}
