package openclaw

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRenderInstallScript_Defaults 验证 RenderInstallScript 在默认 Config 下能产出
// 一份语法上看起来正常的 bash 脚本：含 shebang、BaseURL/SiteName 占位符已替换、
// 新版脚本依赖的关键端点都在。
//
// 这里直接构造 Service{} 而不是 NewService(...)，因为模板渲染只用到 InstallScriptTemplate()，
// 不依赖 settings.Service。
//
// 注意：新版脚本把 provider / memorySearch / gatewayMode 的渲染全部挪到服务端
// /openclaw/render-config，客户端脚本不再包含 PROVIDER_NAME / MEM_ENABLED 等变量，
// 这里也就不再断言它们的存在。
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
		`SITE_NAME="AirGate"`,
		`/openclaw/models.txt`,    // 新版拉模型列表的端点
		`/openclaw/render-config`, // 新版渲染配置的端点
		`/v1/usage`,               // API Key 校验路径（core 自身端点，不经插件）
		`command -v curl`,         // 前置检查应仍在（但 python3 依赖已被移除）
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered script missing %q", want)
		}
	}
	// 反向断言：确认 python3 依赖和脱敏显示的历史逻辑已被彻底移除。
	for _, notWant := range []string{
		"python3",
		"MASKED_API_KEY",
		"已读取 API Key",
	} {
		if strings.Contains(out, notWant) {
			t.Errorf("rendered script should no longer contain %q", notWant)
		}
	}
}

// TestRenderInstallScriptPowerShell_Defaults 验证 PowerShell 版安装脚本能正确渲染，
// 并且用到的关键端点和跨平台约定（USERPROFILE 目录）都在。
func TestRenderInstallScriptPowerShell_Defaults(t *testing.T) {
	s := &Service{}
	cfg := Config{
		BaseURL:      "https://airgate.example.com",
		SiteName:     "AirGate",
		ProviderName: DefaultProviderName,
	}
	out, err := s.RenderInstallScriptPowerShell(cfg)
	if err != nil {
		t.Fatalf("RenderInstallScriptPowerShell: %v", err)
	}
	// 必需项：BaseURL / SiteName 已替换，版本检查、两个端点、Windows 目录约定都在
	for _, want := range []string{
		`$AirgateBase = 'https://airgate.example.com'`,
		`$SiteName    = 'AirGate'`,
		`$PSVersionTable.PSVersion.Major -lt 5`,
		`/openclaw/models.txt`,
		`/openclaw/render-config`,
		`/v1/usage`,
		`Join-Path $env:USERPROFILE '.openclaw'`,
		`Invoke-RestMethod`, // 关键 HTTP 调用
		`ConvertTo-Json`,    // body 序列化
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered PowerShell script missing %q", want)
		}
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
