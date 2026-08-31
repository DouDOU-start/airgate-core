// Package protocol 定义 AirGate Core 与独立进程插件之间的最小通信契约。
package protocol

import (
	"context"

	goplugin "github.com/hashicorp/go-plugin"
)

const (
	// ProtocolVersion 是当前 AirGate 插件协议版本。
	ProtocolVersion = "2"
	// PluginKey 是 go-plugin 握手后的通用插件服务名。
	PluginKey = "plugin"
	// CapabilityRelayHookV1 表示插件实现 Relay Hook v1 请求处理能力。
	CapabilityRelayHookV1 = "relay_hook.v1"
	// CapabilityProviderAttemptTransformV1 marks a request-body transform that
	// runs after Core has selected one concrete account. Its output is scoped to
	// that attempt and cannot influence routing.
	CapabilityProviderAttemptTransformV1 = "provider_attempt_transform.v1"
	// CapabilityAccountTestTransformV1 表示插件可按测试模式改写账号连接测试请求。
	CapabilityAccountTestTransformV1 = "account_test_transform.v1"
	// CapabilityAccountAutofillV1 表示插件会在独立后台任务中自动补充上游账号。
	// 此能力由插件自行调度，Core 负责生命周期管理和配置托管。
	CapabilityAccountAutofillV1 = "account_autofill.v1"
	// CapabilityAccountProviderManagementV1 表示插件提供账号供应商管理动作，
	// 包括余额、库存报价和手动取货订单。
	CapabilityAccountProviderManagementV1 = "account_provider_management.v1"
	// ConfigKeyLogLevel 是 Core 传给插件的日志级别配置键。
	ConfigKeyLogLevel = "log_level"
	// ConfigKeyCoreBaseURL 是 Core 仅向受支持宿主能力注入的本机访问地址。
	// 该值不会写入插件配置文件，也不应出现在插件配置表单中。
	ConfigKeyCoreBaseURL = "_airgate_core_base_url"
	// ConfigKeyCorePluginToken 是 Core 仅向受支持宿主能力注入的进程期访问令牌。
	// 令牌只驻留内存，Core 重启后自动失效。
	ConfigKeyCorePluginToken = "_airgate_core_plugin_token"
	// CorePluginTokenHeader 是插件访问 Core 内部接口时使用的认证头。
	CorePluginTokenHeader = "X-AirGate-Plugin-Token"
	// MaxMessageBytes 限制单次插件 RPC 消息大小。
	MaxMessageBytes = 64 << 20
)

// Handshake 是 Core 与插件进程共同使用的 go-plugin 握手配置。
var Handshake = goplugin.HandshakeConfig{
	ProtocolVersion:  2,
	MagicCookieKey:   "AIRGATE_PLUGIN",
	MagicCookieValue: "airgate-plugin-v2",
}

// PluginInfo 描述插件身份、类型与所提供的能力。
// Type 仅用于分类展示；Core 根据 Capabilities 决定把插件接入哪些能力驱动器。
type PluginInfo struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Version         string            `json:"version"`
	ProtocolVersion string            `json:"protocol_version"`
	Description     string            `json:"description"`
	Author          string            `json:"author"`
	Type            string            `json:"type"`
	Priority        int32             `json:"priority"`
	Capabilities    []string          `json:"capabilities"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	ConfigSchema    *ConfigSchema     `json:"config_schema,omitempty"`
}

// ConfigSchema 描述管理页可动态渲染的插件配置表单。
type ConfigSchema struct {
	Version string        `json:"version" yaml:"version"`
	Fields  []ConfigField `json:"fields" yaml:"fields"`
}

// ConfigField 是一个通用配置字段。Widget 决定前端控件，DataSource 决定选项来源。
type ConfigField struct {
	Key string `json:"key" yaml:"key"`
	// FallbackKey 用于配置字段改名后的无感迁移；新字段不存在时读取旧字段值。
	FallbackKey string `json:"fallback_key,omitempty" yaml:"fallback_key,omitempty"`
	Label       string `json:"label" yaml:"label"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	// Widget 支持：multi_select / single_select / ordered_select / string_list /
	// text / number / textarea / switch。
	Widget     string `json:"widget" yaml:"widget"`
	DataSource string `json:"data_source,omitempty" yaml:"data_source,omitempty"`
	Required   bool   `json:"required,omitempty" yaml:"required,omitempty"`
	// Secret 表示配置值只能写入，管理页读取时只返回固定掩码。
	Secret  bool              `json:"secret,omitempty" yaml:"secret,omitempty"`
	Default any               `json:"default,omitempty" yaml:"default,omitempty"`
	Min     *float64          `json:"min,omitempty" yaml:"min,omitempty"`
	Max     *float64          `json:"max,omitempty" yaml:"max,omitempty"`
	Step    *float64          `json:"step,omitempty" yaml:"step,omitempty"`
	Filter  map[string]string `json:"filter,omitempty" yaml:"filter,omitempty"`
	// Options 是 single_select 的静态选项；与 DataSource 二选一。
	Options []ConfigOption `json:"options,omitempty" yaml:"options,omitempty"`
}

// ConfigOption 是插件配置下拉框的一个可选项。
type ConfigOption struct {
	Value       string `json:"value" yaml:"value"`
	Label       string `json:"label" yaml:"label"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// Request 是能力驱动器发给插件的通用请求。
type Request struct {
	Method string              `json:"method"`
	Path   string              `json:"path"`
	Query  string              `json:"query,omitempty"`
	Header map[string][]string `json:"header,omitempty"`
	Body   []byte              `json:"body,omitempty"`
}

// Response 是插件返回给能力驱动器的通用响应。
type Response struct {
	StatusCode int                 `json:"status_code"`
	Header     map[string][]string `json:"header,omitempty"`
	Body       []byte              `json:"body,omitempty"`
}

// Plugin 是独立进程必须实现的最小生命周期与请求处理接口。
type Plugin interface {
	Info() PluginInfo
	Init(context.Context, map[string]string) error
	Start(context.Context) error
	Stop(context.Context) error
	Handle(context.Context, Request) (Response, error)
}
