// Package protocol 定义 AirGate Core 与独立进程插件之间的最小通信契约。
package protocol

import (
	"context"

	goplugin "github.com/hashicorp/go-plugin"
)

const (
	// ProtocolVersion 是当前 AirGate 插件协议版本。
	ProtocolVersion = "1"
	// PluginKey 是 go-plugin 握手后的通用插件服务名。
	PluginKey = "plugin"
	// CapabilityRelayHookV1 表示插件实现 Relay Hook v1 请求处理能力。
	CapabilityRelayHookV1 = "relay_hook.v1"
	// ConfigKeyLogLevel 是 Core 传给插件的日志级别配置键。
	ConfigKeyLogLevel = "log_level"
	// MaxMessageBytes 限制单次插件 RPC 消息大小。
	MaxMessageBytes = 64 << 20
)

// Handshake 是 Core 与插件进程共同使用的 go-plugin 握手配置。
var Handshake = goplugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "AIRGATE_PLUGIN",
	MagicCookieValue: "airgate-plugin-v1",
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
