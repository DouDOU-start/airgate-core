package main

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/DouDOU-start/airgate-core/internal/pluginruntime/protocol"
)

type fixturePlugin struct{}

// 以下变量允许测试通过 -ldflags 构建不同 ID 和版本的真实插件进程。
var (
	fixturePluginID      = "fixture-hook"
	fixturePluginVersion = "0.0.1"
)

func (fixturePlugin) Info() protocol.PluginInfo {
	return protocol.PluginInfo{
		ID:              fixturePluginID,
		Name:            "测试 Relay Hook",
		Version:         fixturePluginVersion,
		ProtocolVersion: protocol.ProtocolVersion,
		Type:            "middleware",
		Priority:        100,
		Capabilities:    []string{protocol.CapabilityRelayHookV1},
		ConfigSchema: &protocol.ConfigSchema{
			Version: "1",
			Fields: []protocol.ConfigField{
				{Key: "group_ids", Label: "生效分组", Widget: "multi_select", DataSource: "groups", Required: true},
			},
		},
	}
}

func (fixturePlugin) Init(context.Context, map[string]string) error { return nil }
func (fixturePlugin) Start(context.Context) error                   { return nil }
func (fixturePlugin) Stop(context.Context) error                    { return nil }

func (fixturePlugin) Handle(_ context.Context, request protocol.Request) (protocol.Response, error) {
	if request.Method != http.MethodPost || request.Path != "/relay-hook/v1/before-dispatch" {
		return protocol.Response{StatusCode: http.StatusNotFound}, nil
	}
	body, _ := json.Marshal(map[string]any{
		"version": "v1",
		"route": map[string]any{
			"account_ids": []int{7},
			"fallback":    "core",
		},
	})
	return protocol.Response{
		StatusCode: http.StatusOK,
		Header:     map[string][]string{"Content-Type": {"application/json"}},
		Body:       body,
	}, nil
}

func main() {
	protocol.Serve(fixturePlugin{})
}
