// Package hookv2 定义 AirGate 旧版通用插件协议 v2 的最小兼容契约。
//
// 该包只用于兼容声明 relay_hook.v1 能力的独立进程插件；新的插件仍应优先
// 使用 airgate-sdk。兼容层不解释任何供应商语义，也不授予账号调度权限。
package hookv2

import (
	"context"

	goplugin "github.com/hashicorp/go-plugin"
)

const (
	ProtocolVersion                  = "2"
	PluginKey                        = "plugin"
	CapabilityRelayHookV1            = "relay_hook.v1"
	CapabilityAccountTestTransformV1 = "account_test_transform.v1"
	ConfigKeyLogLevel                = "log_level"
	MaxMessageBytes                  = 64 << 20
)

// Handshake 是旧版通用插件协议 v2 的 go-plugin 握手配置。
var Handshake = goplugin.HandshakeConfig{
	ProtocolVersion:  2,
	MagicCookieKey:   "AIRGATE_PLUGIN",
	MagicCookieValue: "airgate-plugin-v2",
}

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

type ConfigSchema struct {
	Version string        `json:"version"`
	Fields  []ConfigField `json:"fields"`
}

type ConfigField struct {
	Key         string            `json:"key"`
	FallbackKey string            `json:"fallback_key,omitempty"`
	Label       string            `json:"label"`
	Description string            `json:"description,omitempty"`
	Widget      string            `json:"widget"`
	DataSource  string            `json:"data_source,omitempty"`
	Required    bool              `json:"required,omitempty"`
	Default     any               `json:"default,omitempty"`
	Filter      map[string]string `json:"filter,omitempty"`
}

type Request struct {
	Method string              `json:"method"`
	Path   string              `json:"path"`
	Query  string              `json:"query,omitempty"`
	Header map[string][]string `json:"header,omitempty"`
	Body   []byte              `json:"body,omitempty"`
}

type Response struct {
	StatusCode int                 `json:"status_code"`
	Header     map[string][]string `json:"header,omitempty"`
	Body       []byte              `json:"body,omitempty"`
}

type Plugin interface {
	Info() PluginInfo
	Init(context.Context, map[string]string) error
	Start(context.Context) error
	Stop(context.Context) error
	Handle(context.Context, Request) (Response, error)
}
