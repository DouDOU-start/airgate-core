// Package plugin 提供插件生命周期管理、市场和请求转发。
package plugin

import (
	"strings"
	"sync"

	goplugin "github.com/hashicorp/go-plugin"

	sdk "github.com/DouDOU-start/airgate-sdk"
	sdkgrpc "github.com/DouDOU-start/airgate-sdk/grpc"
)

// PluginInstance 运行中的插件实例。
type PluginInstance struct {
	Name        string
	SourceName  string
	BinaryDir   string
	DisplayName string
	Version     string
	Author      string
	Platform    string
	Type        string // "gateway", "extension"
	Client      *goplugin.Client
	Gateway     *sdkgrpc.GatewayGRPCClient
	Extension   *sdkgrpc.ExtensionGRPCClient
}

// Manager 插件管理器。
type Manager struct {
	pluginDir string
	logLevel  string

	mu        sync.RWMutex
	instances map[string]*PluginInstance
	aliases   map[string]string
	devPaths  map[string]string

	modelCache        map[string][]sdk.ModelInfo
	routeCache        map[string][]sdk.RouteDefinition
	credCache         map[string][]sdk.CredentialField
	accountTypeCache  map[string][]sdk.AccountType
	frontendPageCache map[string][]sdk.FrontendPage
}

// PluginMeta 插件运行时元信息。
type PluginMeta struct {
	Name          string
	DisplayName   string
	Version       string
	Author        string
	Type          string
	Platform      string
	AccountTypes  []sdk.AccountType
	FrontendPages []sdk.FrontendPage
	HasWebAssets  bool
	IsDev         bool
}

// NewManager 创建插件管理器。
func NewManager(pluginDir, logLevel string) *Manager {
	return &Manager{
		pluginDir:         pluginDir,
		logLevel:          logLevel,
		instances:         make(map[string]*PluginInstance),
		aliases:           make(map[string]string),
		devPaths:          make(map[string]string),
		modelCache:        make(map[string][]sdk.ModelInfo),
		routeCache:        make(map[string][]sdk.RouteDefinition),
		credCache:         make(map[string][]sdk.CredentialField),
		accountTypeCache:  make(map[string][]sdk.AccountType),
		frontendPageCache: make(map[string][]sdk.FrontendPage),
	}
}

func normalizePluginName(name string) string {
	return strings.TrimSpace(name)
}

func canonicalPluginName(info sdk.PluginInfo, fallback string) string {
	if id := normalizePluginName(info.ID); id != "" {
		return id
	}
	return normalizePluginName(fallback)
}
