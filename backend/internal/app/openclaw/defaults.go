// Package openclaw 提供 OpenClaw 一键接入相关的配置与资源。
//
// 本包只依赖 settings 域，不引入 HTTP 层。所有默认值集中在这里，
// 任何 setting key 未配置时走默认值回退，这样：
//   - 管理员面板看到的是"空即默认"的体验
//   - /openclaw/* 路由无需访问数据库也能给出可用的脚本与元信息
package openclaw

import _ "embed"

// Setting key 常量。统一加 "openclaw." 前缀，便于在 Setting 表中按前缀筛选。
const (
	GroupName = "openclaw"

	KeyEnabled             = "openclaw.enabled"
	KeyProviderName        = "openclaw.provider_name"
	KeyBaseURL             = "openclaw.base_url"
	KeyModelsPreset        = "openclaw.models_preset"
	KeyMemorySearchEnabled = "openclaw.memory_search_enabled"
	KeyMemorySearchModel   = "openclaw.memory_search_model"
	KeyGatewayMode         = "openclaw.gateway_mode"
)

// DefaultProviderName 是写入 openclaw.json 的默认 provider 键名。
const DefaultProviderName = "airgate"

// DefaultGatewayMode 是写入 openclaw.json 的 gateway.mode 默认值。
// 新版 openclaw gateway 启动时强校验此字段缺失会拒绝启动。
const DefaultGatewayMode = "local"

// DefaultMemorySearchModel 是 memorySearch 用的默认 embedding 模型。
const DefaultMemorySearchModel = "text-embedding-3-small"

// DefaultModelsPresetJSON 是管理员未配置时展示给用户挑选的模型预设。
//
// 字段与 openclaw.json 里 provider.models[] 的形状一致（id/api/reasoning/input），
// 额外带一个 label 供脚本展示给用户。
const DefaultModelsPresetJSON = `[
  {
    "id": "gpt-5.4",
    "label": "GPT-5.4 (推荐)",
    "api": "openai-responses",
    "reasoning": true,
    "input": ["text", "image"]
  },
  {
    "id": "claude-sonnet-4-6",
    "label": "Claude Sonnet 4.6",
    "api": "anthropic-messages",
    "reasoning": true,
    "input": ["text", "image"]
  },
  {
    "id": "claude-opus-4-6",
    "label": "Claude Opus 4.6",
    "api": "anthropic-messages",
    "reasoning": true,
    "input": ["text", "image"]
  }
]`

// installScriptTemplate 是 /openclaw/install.sh 返回的脚本模板，由 go:embed 打进二进制。
//
//go:embed assets/install.sh.tmpl
var installScriptTemplate string

// InstallScriptTemplate 返回安装脚本模板原文。
func InstallScriptTemplate() string {
	return installScriptTemplate
}
