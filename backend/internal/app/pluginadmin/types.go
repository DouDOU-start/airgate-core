package pluginadmin

import (
	"context"
	"net/http"

	"github.com/DouDOU-start/airgate-core/internal/plugin"
	sdk "github.com/DouDOU-start/airgate-sdk"
)

// Manager 定义插件管理服务所需能力。
type Manager interface {
	GetAllPluginMeta() []plugin.PluginMeta
	InstallFromBinary(context.Context, string, []byte) error
	InstallFromGithub(context.Context, string) error
	Uninstall(context.Context, string) error
	ReloadDev(context.Context, string) error
	IsDev(string) bool
	GetInstance(string) *plugin.PluginInstance
}

// MarketplaceReader 定义插件市场读取能力。
type MarketplaceReader interface {
	ListAvailable(context.Context) ([]plugin.MarketplacePlugin, error)
	SyncFromGithub(context.Context) error
}

// PluginMeta 插件元信息。
type PluginMeta struct {
	Name               string
	DisplayName        string
	Version            string
	Author             string
	Type               string
	Platform           string
	AccountTypes       []sdk.AccountType
	FrontendPages      []sdk.FrontendPage
	InstructionPresets []string
	HasWebAssets       bool
	IsDev              bool
}

// MarketplacePlugin 市场插件条目。
type MarketplacePlugin struct {
	Name        string
	Version     string
	Description string
	Author      string
	Type        string
	GithubRepo  string
	Installed   bool
}

// ProxyInput 表示插件代理输入。
type ProxyInput struct {
	Name    string
	Method  string
	Action  string
	Query   string
	Headers http.Header
	Body    []byte
}

// ProxyResult 表示插件代理结果。
type ProxyResult struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}
