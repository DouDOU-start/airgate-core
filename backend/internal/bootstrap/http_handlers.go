package bootstrap

import (
	"github.com/DouDOU-start/airgate-core/ent"
	appaccount "github.com/DouDOU-start/airgate-core/internal/app/account"
	appapikey "github.com/DouDOU-start/airgate-core/internal/app/apikey"
	appauth "github.com/DouDOU-start/airgate-core/internal/app/auth"
	appdashboard "github.com/DouDOU-start/airgate-core/internal/app/dashboard"
	appgroup "github.com/DouDOU-start/airgate-core/internal/app/group"
	apppluginadmin "github.com/DouDOU-start/airgate-core/internal/app/pluginadmin"
	appproxy "github.com/DouDOU-start/airgate-core/internal/app/proxy"
	appsettings "github.com/DouDOU-start/airgate-core/internal/app/settings"
	appsubscription "github.com/DouDOU-start/airgate-core/internal/app/subscription"
	appusage "github.com/DouDOU-start/airgate-core/internal/app/usage"
	appuser "github.com/DouDOU-start/airgate-core/internal/app/user"
	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/config"
	"github.com/DouDOU-start/airgate-core/internal/infra/store"
	"github.com/DouDOU-start/airgate-core/internal/plugin"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"
	"github.com/DouDOU-start/airgate-core/internal/server/handler"
)

// HTTPDependencies 描述 HTTP 处理器装配所需依赖。
type HTTPDependencies struct {
	Config      *config.Config
	DB          *ent.Client
	JWTMgr      *auth.JWTManager
	PluginMgr   *plugin.Manager
	Marketplace *plugin.Marketplace
	Concurrency *scheduler.ConcurrencyManager
}

// HTTPHandlers 聚合所有 HTTP 处理器。
type HTTPHandlers struct {
	Auth         *handler.AuthHandler
	User         *handler.UserHandler
	Account      *handler.AccountHandler
	Group        *handler.GroupHandler
	APIKey       *handler.APIKeyHandler
	Subscription *handler.SubscriptionHandler
	Usage        *handler.UsageHandler
	Proxy        *handler.ProxyHandler
	Settings     *handler.SettingsHandler
	Dashboard    *handler.DashboardHandler
	Plugin       *handler.PluginHandler
}

// NewHTTPHandlers 统一构造 HTTP 处理器。
func NewHTTPHandlers(dep HTTPDependencies) *HTTPHandlers {
	apiKeyStore := store.NewAPIKeyStore(dep.DB)
	apiKeyService := appapikey.NewService(apiKeyStore, dep.Config.APIKeySecret())
	authStore := store.NewAuthStore(dep.DB)
	authService := appauth.NewService(authStore, dep.JWTMgr)
	accountStore := store.NewAccountStore(dep.DB)
	accountService := appaccount.NewService(accountStore, dep.PluginMgr, dep.Concurrency)
	groupStore := store.NewGroupStore(dep.DB)
	groupService := appgroup.NewService(groupStore, dep.Concurrency)
	proxyStore := store.NewProxyStore(dep.DB)
	proxyService := appproxy.NewService(proxyStore)
	subscriptionStore := store.NewSubscriptionStore(dep.DB)
	subscriptionService := appsubscription.NewService(subscriptionStore)
	dashboardStore := store.NewDashboardStore(dep.DB)
	dashboardService := appdashboard.NewService(dashboardStore)
	pluginAdminService := apppluginadmin.NewService(dep.PluginMgr, dep.Marketplace)
	settingsStore := store.NewSettingsStore(dep.DB)
	settingsService := appsettings.NewService(settingsStore)
	userStore := store.NewUserStore(dep.DB)
	userService := appuser.NewService(userStore)
	usageStore := store.NewUsageStore(dep.DB)
	usageService := appusage.NewService(usageStore)

	return &HTTPHandlers{
		Auth:         handler.NewAuthHandler(authService),
		User:         handler.NewUserHandler(userService),
		Account:      handler.NewAccountHandler(accountService),
		Group:        handler.NewGroupHandler(groupService),
		APIKey:       handler.NewAPIKeyHandler(apiKeyService),
		Subscription: handler.NewSubscriptionHandler(subscriptionService),
		Usage:        handler.NewUsageHandler(usageService),
		Proxy:        handler.NewProxyHandler(proxyService),
		Settings:     handler.NewSettingsHandler(settingsService),
		Dashboard:    handler.NewDashboardHandler(dashboardService),
		Plugin:       handler.NewPluginHandler(pluginAdminService),
	}
}
