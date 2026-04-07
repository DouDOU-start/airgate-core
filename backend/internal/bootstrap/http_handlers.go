package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

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
	"github.com/DouDOU-start/airgate-core/internal/infra/mailer"
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
	verifyCodeStore := mailer.NewVerifyCodeStore()
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

	// 余额预警回调：从设置读取 SMTP 配置发送邮件
	userService.SetBalanceAlertCallback(func(email string, balance float64, threshold float64) {
		balanceAlertSendEmail(settingsService, email, balance, threshold)
	})
	usageStore := store.NewUsageStore(dep.DB)
	usageService := appusage.NewService(usageStore)

	return &HTTPHandlers{
		Auth:         handler.NewAuthHandler(authService, settingsService, verifyCodeStore, dep.DB, dep.JWTMgr),
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

// defaultBalanceAlertBody 余额预警邮件默认正文模板。
const defaultBalanceAlertBody = `<div style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; max-width: 420px; margin: 0 auto; background: #ffffff; border-radius: 8px; border: 1px solid #e5e7eb;">
<div style="padding: 32px 28px;">
<div style="font-size: 16px; font-weight: 600; color: #111; margin-bottom: 20px;">{{site_name}}</div>
<p style="color: #555; font-size: 14px; line-height: 1.6; margin: 0 0 16px;">您的账户余额已低于预警阈值：</p>
<div style="background: #fef3c7; border: 1px solid #fde68a; border-radius: 8px; padding: 16px; margin-bottom: 20px;">
<div style="display: flex; justify-content: space-between; margin-bottom: 8px;">
<span style="color: #92400e; font-size: 13px;">当前余额</span>
<span style="color: #92400e; font-size: 16px; font-weight: 700;">{{balance}}</span>
</div>
<div style="display: flex; justify-content: space-between;">
<span style="color: #92400e; font-size: 13px;">预警阈值</span>
<span style="color: #92400e; font-size: 13px;">{{threshold}}</span>
</div>
</div>
<p style="color: #999; font-size: 12px; line-height: 1.6; margin: 0;">请及时充值以免影响正常使用。余额回到阈值以上后，预警将自动重置。</p>
</div>
<div style="border-top: 1px solid #f0f0f0; padding: 14px 28px;">
<p style="color: #c0c0c0; font-size: 11px; margin: 0; text-align: center;">此邮件由 {{site_name}} 系统自动发送</p>
</div>
</div>`

// balanceAlertSendEmail 发送余额预警邮件。
func balanceAlertSendEmail(settingsService *appsettings.Service, email string, balance, threshold float64) {
	ctx := context.Background()

	// 读取 SMTP 配置
	smtpSettings, err := settingsService.List(ctx, "smtp")
	if err != nil {
		slog.Error("余额预警：读取 SMTP 设置失败", "error", err)
		return
	}
	cfg := mailer.Config{}
	for _, s := range smtpSettings {
		switch s.Key {
		case "smtp_host":
			cfg.Host = s.Value
		case "smtp_port":
			cfg.Port, _ = strconv.Atoi(s.Value)
		case "smtp_username":
			cfg.Username = s.Value
		case "smtp_password":
			cfg.Password = s.Value
		case "smtp_from_email":
			cfg.FromAddr = s.Value
		case "smtp_from_name":
			cfg.FromName = s.Value
		case "smtp_use_tls":
			cfg.UseTLS = s.Value == "true"
		}
	}
	if cfg.Host == "" {
		slog.Warn("余额预警：SMTP 未配置，跳过发送")
		return
	}
	if cfg.Port == 0 {
		cfg.Port = 587
	}

	// 读取站点名称及余额预警邮件模板
	siteName := "AirGate"
	var tplSubject, tplBody string
	siteSettings, _ := settingsService.List(ctx, "site")
	for _, s := range siteSettings {
		if s.Key == "site_name" && s.Value != "" {
			siteName = s.Value
		}
	}
	for _, s := range smtpSettings {
		switch s.Key {
		case "balance_alert_email_subject":
			tplSubject = s.Value
		case "balance_alert_email_body":
			tplBody = s.Value
		}
	}

	balanceStr := fmt.Sprintf("$%.4f", balance)
	thresholdStr := fmt.Sprintf("$%.2f", threshold)

	// 使用自定义模板或默认模板
	if tplSubject == "" {
		tplSubject = "{{site_name}} - 余额预警"
	}
	if tplBody == "" {
		tplBody = defaultBalanceAlertBody
	}

	replacer := strings.NewReplacer(
		"{{site_name}}", siteName,
		"{{balance}}", balanceStr,
		"{{threshold}}", thresholdStr,
	)
	subject := replacer.Replace(tplSubject)
	body := replacer.Replace(tplBody)

	m := mailer.New(cfg)
	if err := m.Send(email, subject, body); err != nil {
		slog.Error("余额预警邮件发送失败", "email", email, "error", err)
	} else {
		slog.Info("余额预警邮件已发送", "email", email, "balance", balance, "threshold", threshold)
	}
}
