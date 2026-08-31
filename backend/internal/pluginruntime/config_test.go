package pluginruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/config"
	"github.com/DouDOU-start/airgate-core/internal/pluginruntime/protocol"
)

func TestEncodeConfigValueFormatsWholeFloatsAsIntegers(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{name: "整型", value: 8388608, want: "8388608"},
		{name: "int64", value: int64(8388608), want: "8388608"},
		{name: "整型浮点", value: float64(8388608), want: "8388608"},
		{name: "科学计数法浮点", value: 8.388608e+06, want: "8388608"},
		{name: "小数", value: 1.5, want: "1.5"},
		{name: "布尔", value: true, want: "true"},
		{name: "字符串保持原样", value: "keep", want: "keep"},
		{name: "json.Number整数", value: json.Number("8388608"), want: "8388608"},
		{name: "json.Number科学计数法", value: json.Number("8.388608e+06"), want: "8388608"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := encodeConfigValue(tt.value)
			if err != nil {
				t.Fatalf("encodeConfigValue() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("encodeConfigValue() = %q, want %q", got, tt.want)
			}
			if _, err := strconv.Atoi(got); tt.want == "8388608" && err != nil {
				t.Fatalf("插件 strconv.Atoi(%q) 失败: %v", got, err)
			}
		})
	}
}

func TestLoadPluginConfigDecodesScientificIntegers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("enhancement_max_body_bytes: 8.388608e+06\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	values, err := loadPluginConfig(path, "error")
	if err != nil {
		t.Fatal(err)
	}
	got := values["enhancement_max_body_bytes"]
	if got != "8388608" {
		t.Fatalf("科学计数法整数未规范化: %q", got)
	}
	if _, err := strconv.Atoi(got); err != nil {
		t.Fatalf("插件 strconv.Atoi(%q) 失败: %v", got, err)
	}
}

func TestPersistConfigFieldValueCoercesWholeNumbers(t *testing.T) {
	tests := []struct {
		name   string
		widget string
		value  any
		want   any
	}{
		{name: "JSON浮点整数", widget: "number", value: float64(8388608), want: int64(8388608)},
		{name: "输入框字符串", widget: "number", value: "8388608", want: int64(8388608)},
		{name: "小数保持浮点", widget: "number", value: 1.5, want: 1.5},
		{name: "非数字控件保持原值", widget: "text", value: float64(8388608), want: float64(8388608)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := persistConfigFieldValue(protocol.ConfigField{Key: "enhancement_max_body_bytes", Widget: tt.widget}, tt.value)
			if got != tt.want {
				t.Fatalf("persistConfigFieldValue() = %v (%T), want %v (%T)", got, got, tt.want, tt.want)
			}
		})
	}
}

func TestUpdateConfigFormPersistsJSONWholeNumbersAsIntegers(t *testing.T) {
	pluginDir := t.TempDir()
	id := "codex-enhance-fixture"
	installDir := filepath.Join(pluginDir, id)
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, pluginBinaryName(id)), []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `id: codex-enhance-fixture
name: Codex 增强测试插件
version: 1.0.0
protocol_version: "2"
capabilities:
  - relay_hook.v1
config_schema:
  version: "1"
  fields:
    - key: enhancement_max_body_bytes
      label: 增强请求体上限
      widget: number
      required: true
`
	if err := os.WriteFile(filepath.Join(installDir, "manifest.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "config.yaml"), []byte("enhancement_max_body_bytes: 8388608\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := New(config.PluginsConfig{Dir: pluginDir}, "error")
	if err := manager.writeRuntimeState(id, runtimeState{Enabled: false, InstalledAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	var values map[string]any
	if err := json.Unmarshal([]byte(`{"enhancement_max_body_bytes":8388608}`), &values); err != nil {
		t.Fatal(err)
	}
	if _, ok := values["enhancement_max_body_bytes"].(float64); !ok {
		t.Fatalf("测试前置条件失败：JSON 数字应解码为 float64，实际 %T", values["enhancement_max_body_bytes"])
	}
	if err := manager.UpdateConfigForm(context.Background(), id, values); err != nil {
		t.Fatalf("保存 JSON 整型浮点配置失败: %v", err)
	}

	configText, err := manager.GetConfig(id)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(configText), "e+") {
		t.Fatalf("持久化配置不应使用科学计数法: %q", configText)
	}

	loaded, err := loadPluginConfig(filepath.Join(installDir, "config.yaml"), "error")
	if err != nil {
		t.Fatal(err)
	}
	got := loaded["enhancement_max_body_bytes"]
	if got != "8388608" {
		t.Fatalf("传给插件的整型配置 = %q, want 8388608", got)
	}
	if _, err := strconv.Atoi(got); err != nil {
		t.Fatalf("插件 strconv.Atoi(%q) 失败: %v", got, err)
	}
}
