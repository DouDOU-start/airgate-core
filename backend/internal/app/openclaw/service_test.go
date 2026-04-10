package openclaw

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRenderInstallScript_Defaults 验证 RenderInstallScript 在默认 Config 下能产出
// 一份语法上看起来正常的 bash 脚本：含 shebang、占位符已替换、关键步骤都在。
//
// 这里直接构造 Service{} 而不是 NewService(...)，因为模板渲染只用到 InstallScriptTemplate()，
// 不依赖 settings.Service。
func TestRenderInstallScript_Defaults(t *testing.T) {
	s := &Service{}
	cfg := Config{
		BaseURL:             "https://airgate.example.com",
		SiteName:            "AirGate",
		ProviderName:        DefaultProviderName,
		MemorySearchEnabled: false,
		MemorySearchModel:   DefaultMemorySearchModel,
	}
	out, err := s.RenderInstallScript(cfg)
	if err != nil {
		t.Fatalf("RenderInstallScript: %v", err)
	}
	if !strings.HasPrefix(out, "#!/usr/bin/env bash") {
		t.Errorf("missing bash shebang; got first 30 chars: %q", out[:min(30, len(out))])
	}
	for _, want := range []string{
		`AIRGATE_BASE="https://airgate.example.com"`,
		`PROVIDER_NAME="airgate"`,
		`/openclaw/models`,
		`MEM_ENABLED="0"`,                       // memory_search 关闭时模板分支应输出 0
		`/v1/models`,                            // API Key 校验路径
		`info "已读取 API Key: ${MASKED_API_KEY}"`, // 输入后显示脱敏确认
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered script missing %q", want)
		}
	}
}

// TestRenderInstallScript_MemorySearchEnabled 检查 {{if .MemorySearchEnabled}} 分支
// 在开启时正确把 1 写进 MEM_ENABLED。
func TestRenderInstallScript_MemorySearchEnabled(t *testing.T) {
	s := &Service{}
	cfg := Config{
		BaseURL:             "http://localhost:8080",
		SiteName:            "Local",
		ProviderName:        "airgate",
		MemorySearchEnabled: true,
		MemorySearchModel:   "text-embedding-3-large",
	}
	out, err := s.RenderInstallScript(cfg)
	if err != nil {
		t.Fatalf("RenderInstallScript: %v", err)
	}
	if !strings.Contains(out, `MEM_ENABLED="1"`) {
		t.Error("expected MEM_ENABLED=1 when memory search enabled")
	}
	if !strings.Contains(out, `MEM_MODEL="text-embedding-3-large"`) {
		t.Error("expected MEM_MODEL to be substituted")
	}
}

// TestDefaultModelsPresetJSON_IsValid 防止有人手抖把默认 JSON 改坏，导致
// /openclaw/models handler 在管理员未配置时报 500。
func TestDefaultModelsPresetJSON_IsValid(t *testing.T) {
	var arr []map[string]any
	if err := json.Unmarshal([]byte(DefaultModelsPresetJSON), &arr); err != nil {
		t.Fatalf("DefaultModelsPresetJSON is not valid JSON array: %v", err)
	}
	if len(arr) == 0 {
		t.Fatal("DefaultModelsPresetJSON should not be empty")
	}
	for i, m := range arr {
		for _, k := range []string{"id", "label", "api"} {
			if _, ok := m[k]; !ok {
				t.Errorf("model[%d] missing required field %q", i, k)
			}
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
