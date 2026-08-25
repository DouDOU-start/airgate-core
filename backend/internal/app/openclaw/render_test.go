package openclaw

import (
	"encoding/json"
	"strings"
	"testing"
)

// newTestConfig 构造一份跑 render 单测时用的 Config，复用默认 models_preset，
// 避免每个测试重复声明。
func newTestConfig() Config {
	return Config{
		Enabled:             true,
		ProviderName:        DefaultProviderName,
		BaseURL:             "https://airgate.example.com",
		ModelsPresetJSON:    DefaultModelsPresetJSON,
		MemorySearchEnabled: false,
		MemorySearchModel:   DefaultMemorySearchModel,
		SiteName:            "AirGate",
		GatewayMode:         DefaultGatewayMode,
	}
}

// TestBuildModelsText_Defaults 验证 pipe 分隔文本能正确覆盖默认 preset 中的所有模型，
// 且格式对齐 install.sh 里 `while IFS='|' read` 的期望（idx|id|label|api|caps）。
func TestBuildModelsText_Defaults(t *testing.T) {
	s := &Service{}
	text, err := s.BuildModelsText(newTestConfig())
	if err != nil {
		t.Fatalf("BuildModelsText: %v", err)
	}

	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines for default preset, got %d: %q", len(lines), text)
	}

	// 第一行应当是 gpt-5.4，五个字段，caps 含 reasoning image
	parts := strings.Split(lines[0], "|")
	if len(parts) != 5 {
		t.Fatalf("line[0] expected 5 pipe fields, got %d: %q", len(parts), lines[0])
	}
	if parts[0] != "1" {
		t.Errorf("line[0] idx = %q, want 1", parts[0])
	}
	if parts[1] != "gpt-5.4" {
		t.Errorf("line[0] id = %q, want gpt-5.4", parts[1])
	}
	if !strings.Contains(parts[2], "GPT-5.4") {
		t.Errorf("line[0] label = %q, want to contain GPT-5.4", parts[2])
	}
	if parts[3] != "openai-responses" {
		t.Errorf("line[0] api = %q, want openai-responses", parts[3])
	}
	if !strings.Contains(parts[4], "reasoning") || !strings.Contains(parts[4], "image") {
		t.Errorf("line[0] caps = %q, want reasoning+image", parts[4])
	}
}

// TestBuildModelsText_LabelPipeSanitized 验证 label 里如果混入 `|` 不会污染分隔符。
func TestBuildModelsText_LabelPipeSanitized(t *testing.T) {
	s := &Service{}
	cfg := newTestConfig()
	cfg.ModelsPresetJSON = `[{"id":"x","label":"a|b","api":"openai-responses"}]`
	text, err := s.BuildModelsText(cfg)
	if err != nil {
		t.Fatalf("BuildModelsText: %v", err)
	}
	if strings.Count(strings.TrimSpace(text), "|") != 4 {
		t.Errorf("expected exactly 4 pipe separators, got %q", text)
	}
	if strings.Contains(text, "a|b") {
		t.Errorf("label pipe should be sanitized, got %q", text)
	}
}

// TestBuildOpenClawConfig_Happy 验证在默认 preset 下挑两个模型能渲染出合法的
// openclaw.json，且关键字段（gateway/providers/defaults）都到位。
func TestBuildOpenClawConfig_Happy(t *testing.T) {
	s := &Service{}
	out, err := s.BuildOpenClawConfig(newTestConfig(), RenderConfigRequest{
		APIKey:      "sk-test-1234",
		SelectedIDs: []string{"gpt-5.4", "claude-sonnet-4-6"},
	})
	if err != nil {
		t.Fatalf("BuildOpenClawConfig: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}

	gatewayMode := cfg["gateway"].(map[string]any)["mode"]
	if gatewayMode != DefaultGatewayMode {
		t.Errorf("gateway.mode = %v, want %s", gatewayMode, DefaultGatewayMode)
	}

	provider := cfg["models"].(map[string]any)["providers"].(map[string]any)[DefaultProviderName].(map[string]any)
	if provider["baseUrl"] != "https://airgate.example.com/v1" {
		t.Errorf("provider.baseUrl = %v", provider["baseUrl"])
	}
	if provider["apiKey"] != "sk-test-1234" {
		t.Errorf("provider.apiKey = %v", provider["apiKey"])
	}
	if len(provider["models"].([]any)) != 2 {
		t.Errorf("provider.models length = %d, want 2", len(provider["models"].([]any)))
	}

	primary := cfg["agents"].(map[string]any)["defaults"].(map[string]any)["model"].(map[string]any)["primary"]
	if primary != DefaultProviderName+"/gpt-5.4" {
		t.Errorf("defaults.model.primary = %v, want airgate/gpt-5.4", primary)
	}

	// memorySearch 未启用时不应出现
	if _, ok := cfg["agents"].(map[string]any)["defaults"].(map[string]any)["memorySearch"]; ok {
		t.Error("memorySearch should not be present when disabled")
	}
}

// TestBuildOpenClawConfig_MemorySearchEnabled 验证启用 memorySearch 后配置被写入到
// agents.defaults.memorySearch 里，且 apiKey 同步到 memorySearch.remote。
func TestBuildOpenClawConfig_MemorySearchEnabled(t *testing.T) {
	s := &Service{}
	c := newTestConfig()
	c.MemorySearchEnabled = true
	c.MemorySearchModel = "text-embedding-3-large"

	out, err := s.BuildOpenClawConfig(c, RenderConfigRequest{
		APIKey:      "sk-test-1234",
		SelectedIDs: []string{"gpt-5.4"},
	})
	if err != nil {
		t.Fatalf("BuildOpenClawConfig: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	ms, ok := cfg["agents"].(map[string]any)["defaults"].(map[string]any)["memorySearch"].(map[string]any)
	if !ok {
		t.Fatal("expected memorySearch block to be present")
	}
	if ms["enabled"] != true {
		t.Errorf("memorySearch.enabled = %v, want true", ms["enabled"])
	}
	if ms["model"] != "text-embedding-3-large" {
		t.Errorf("memorySearch.model = %v", ms["model"])
	}
	remote := ms["remote"].(map[string]any)
	if remote["apiKey"] != "sk-test-1234" {
		t.Errorf("memorySearch.remote.apiKey = %v", remote["apiKey"])
	}
	if remote["baseUrl"] != "https://airgate.example.com/v1" {
		t.Errorf("memorySearch.remote.baseUrl = %v", remote["baseUrl"])
	}
}

// TestBuildOpenClawConfig_ValidationErrors 覆盖常见的错误路径：
//   - 空 api_key / selected_ids
//   - 未知模型 ID
//   - BaseURL 未配置
func TestBuildOpenClawConfig_ValidationErrors(t *testing.T) {
	s := &Service{}
	base := newTestConfig()

	cases := []struct {
		name string
		cfg  Config
		req  RenderConfigRequest
		want string
	}{
		{"empty api key", base, RenderConfigRequest{SelectedIDs: []string{"gpt-5.4"}}, "api_key"},
		{"empty selection", base, RenderConfigRequest{APIKey: "sk-x"}, "selected_ids"},
		{"unknown id", base, RenderConfigRequest{APIKey: "sk-x", SelectedIDs: []string{"no-such-model"}}, "unknown model id"},
		{"missing base url", func() Config { c := base; c.BaseURL = ""; return c }(), RenderConfigRequest{APIKey: "sk-x", SelectedIDs: []string{"gpt-5.4"}}, "base_url"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.BuildOpenClawConfig(tc.cfg, tc.req)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

// TestBuildOpenClawConfig_DedupSelection 验证重复传入同一个 ID 不会在 provider.models
// 里出现两份相同记录（保留首次出现的顺序）。
func TestBuildOpenClawConfig_DedupSelection(t *testing.T) {
	s := &Service{}
	out, err := s.BuildOpenClawConfig(newTestConfig(), RenderConfigRequest{
		APIKey:      "sk-x",
		SelectedIDs: []string{"gpt-5.4", "gpt-5.4", "claude-sonnet-4-6"},
	})
	if err != nil {
		t.Fatalf("BuildOpenClawConfig: %v", err)
	}
	var cfg map[string]any
	_ = json.Unmarshal(out, &cfg)
	models := cfg["models"].(map[string]any)["providers"].(map[string]any)[DefaultProviderName].(map[string]any)["models"].([]any)
	if len(models) != 2 {
		t.Errorf("provider.models length after dedup = %d, want 2", len(models))
	}
}
