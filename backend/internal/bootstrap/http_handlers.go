package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"entgo.io/ent/dialect"
	"github.com/redis/go-redis/v9"

	"github.com/DouDOU-start/airgate-core/internal/pkg/logx"

	"github.com/DouDOU-start/airgate-core/ent"
	appaccount "github.com/DouDOU-start/airgate-core/internal/app/account"
	appannouncement "github.com/DouDOU-start/airgate-core/internal/app/announcement"
	appapikey "github.com/DouDOU-start/airgate-core/internal/app/apikey"
	appauth "github.com/DouDOU-start/airgate-core/internal/app/auth"
	appbookmark "github.com/DouDOU-start/airgate-core/internal/app/bookmark"
	appchannel "github.com/DouDOU-start/airgate-core/internal/app/channel"
	appdashboard "github.com/DouDOU-start/airgate-core/internal/app/dashboard"
	appgroup "github.com/DouDOU-start/airgate-core/internal/app/group"
	appinvite "github.com/DouDOU-start/airgate-core/internal/app/invite"
	appmodelprice "github.com/DouDOU-start/airgate-core/internal/app/modelprice"
	appoauth "github.com/DouDOU-start/airgate-core/internal/app/oauth"
	apppayment "github.com/DouDOU-start/airgate-core/internal/app/payment"
	appproxy "github.com/DouDOU-start/airgate-core/internal/app/proxy"
	appredemption "github.com/DouDOU-start/airgate-core/internal/app/redemption"
	appriskcontrol "github.com/DouDOU-start/airgate-core/internal/app/riskcontrol"
	appsettings "github.com/DouDOU-start/airgate-core/internal/app/settings"
	apptier "github.com/DouDOU-start/airgate-core/internal/app/tier"
	appupstreamlog "github.com/DouDOU-start/airgate-core/internal/app/upstreamlog"
	appusage "github.com/DouDOU-start/airgate-core/internal/app/usage"
	appuser "github.com/DouDOU-start/airgate-core/internal/app/user"
	appwallet "github.com/DouDOU-start/airgate-core/internal/app/wallet"
	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/config"
	"github.com/DouDOU-start/airgate-core/internal/infra/mailer"
	"github.com/DouDOU-start/airgate-core/internal/infra/store"
	"github.com/DouDOU-start/airgate-core/internal/moderation"
	"github.com/DouDOU-start/airgate-core/internal/probe"
	"github.com/DouDOU-start/airgate-core/internal/requestaudit"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"
	"github.com/DouDOU-start/airgate-core/internal/server/handler"
)

// HTTPDependencies 描述 HTTP 处理器装配所需依赖。
type HTTPDependencies struct {
	Config      *config.Config
	DB          *ent.Client
	Redis       *redis.Client
	JWTMgr      *auth.JWTManager
	Concurrency *scheduler.ConcurrencyManager
}

// HTTPHandlers 聚合所有 HTTP 处理器。
type HTTPHandlers struct {
	Auth         *handler.AuthHandler
	User         *handler.UserHandler
	Group        *handler.GroupHandler
	Tier         *handler.TierHandler
	Announcement *handler.AnnouncementHandler
	APIKey       *handler.APIKeyHandler
	Usage        *handler.UsageHandler
	UpstreamLog  *handler.UpstreamLogHandler
	Channel      *handler.ChannelHandler
	ModelPrice   *handler.ModelPriceHandler
	Settings     *handler.SettingsHandler
	Dashboard    *handler.DashboardHandler
	Payment      *handler.PaymentHandler
	Redemption   *handler.RedemptionHandler
	Invite       *handler.InviteHandler
	Version      *handler.VersionHandler
	OAuth        *handler.OAuthHandler
	RiskControl  *handler.RiskControlHandler
	Bookmark     *handler.BookmarkHandler
	Account      *handler.AccountHandler
	Proxy        *handler.ProxyHandler
	RequestAudit *handler.RequestAuditHandler

	// ChannelService / ModelPriceService / SettingsService 暴露给 server.go：
	// ChannelService 充当渠道注册表的 Loader/Persister 并接收 Reloader/Tester 注入，
	// ModelPriceService 充当 pricing 缓存的 Loader 并接收 Invalidator 注入，
	// SettingsService 供 relay 管线的 gateway 设置读取器使用，
	// UpstreamLogService 接收 errlog 失败计数读取器注入（渠道页监控列）。
	ChannelService     *appchannel.Service
	ModelPriceService  *appmodelprice.Service
	SettingsService    *appsettings.Service
	UpstreamLogService *appupstreamlog.Service
	// PaymentService 暴露给 server.go：启动时装载支付服务商 + 拉起订单过期清理循环。
	PaymentService *apppayment.Service
	// UserService 暴露给 server.go：任务子系统余额动账（预扣/结算/退款）适配器用。
	UserService *appuser.Service
	// TaskStore 暴露给 server.go：任务子系统（relay/task）的持久化实现。
	TaskStore *store.TaskStore
	// ModerationEngine 暴露给 server.go：注入 relay 管线/任务子系统的审核预检，
	// 并由 StartBackground 拉起 worker 池与日志 TTL 清理。
	ModerationEngine *moderation.Engine
	// ChannelStore 暴露给 server.go：探针引擎的健康状态持久化与余额同步目标查询。
	ChannelStore *store.ChannelStore
	// AccountService 暴露给 server.go：注入账号注册表 Reloader。
	AccountService *appaccount.Service
	// ProxyService 暴露给 server.go：代理变更后重载账号注册表。
	ProxyService *appproxy.Service
	// RequestAuditService 同时注入 relay 写路径与管理员查询 Handler。
	RequestAuditService *requestaudit.Service
	// ChannelHealthNotifier 接收探针状态变化并按设置发送 Bark 等外推提醒。
	ChannelHealthNotifier probe.Notifier
}

// NewHTTPHandlers 统一构造 HTTP 处理器。
func NewHTTPHandlers(dep HTTPDependencies) *HTTPHandlers {
	requestAuditService := requestaudit.New(dep.DB, dep.Config.APIKeySecret())
	apiKeyStore := store.NewAPIKeyStore(dep.DB)
	apiKeyService := appapikey.NewService(apiKeyStore, dep.Config.APIKeySecret())
	authStore := store.NewAuthStore(dep.DB)
	auth.SetAPIKeyCacheRedis(dep.Redis)
	authService := appauth.NewService(authStore, dep.JWTMgr)
	verifyCodeStore := mailer.NewVerifyCodeStore()
	// 设置和验证码依赖延迟到 settingsService 创建后注入
	// RPM 计数器为无状态 Redis 包装，user / group / channel 三个域共享一个实例。
	rpmCounter := scheduler.NewRPMCounter(dep.Redis)
	groupStore := store.NewGroupStore(dep.DB)
	groupService := appgroup.NewService(groupStore, dep.Concurrency)
	groupService.SetRPMReader(rpmCounter)
	tierStore := store.NewTierStore(dep.DB)
	tierService := apptier.NewService(tierStore)
	announcementStore := store.NewAnnouncementStore(dep.DB)
	announcementService := appannouncement.NewService(announcementStore)
	channelStore := store.NewChannelStore(dep.DB)
	channelService := appchannel.NewService(channelStore, dep.Config.APIKeySecret())
	channelService.SetRuntimeStatsReaders(dep.Concurrency, rpmCounter)
	channelService.SetStatsReader(channelStore)
	modelPriceStore := store.NewModelPriceStore(dep.DB)
	modelPriceService := appmodelprice.NewService(modelPriceStore)
	// 模型广场倍率区间：只取非专属分组，避免泄露专属谈价倍率
	modelPriceService.SetGroupRateReader(groupService)
	dashboardStore := store.NewDashboardStore(dep.DB, dialect.Postgres, dep.Redis)
	dashboardService := appdashboard.NewService(dashboardStore, dep.Redis)
	settingsStore := store.NewSettingsStore(dep.DB)
	settingsService := appsettings.NewService(settingsStore, dep.Config.APIKeySecret())
	channelHealthNotifier := newChannelBarkNotifier(settingsService, channelStore, dep.Redis)
	settingsService.SetBarkTester(channelHealthNotifier)

	inviteStore := store.NewInviteStore(dep.DB)
	inviteService := appinvite.NewService(inviteStore)
	inviteService.SetSettingsLister(inviteSettingsAdapter{settingsService})

	// 注入 auth 服务的设置/验证码/邮件/邀请返利依赖
	authService.SetSettingsLister(&settingsAdapter{settingsService})
	authService.SetVerifyCodeStore(verifyCodeStore)
	authService.SetMailerFactory(buildMailerFactory(settingsService))
	authService.SetInviteBinder(inviteService)

	userStore := store.NewUserStore(dep.DB)
	userService := appuser.NewService(userStore)
	// 用户视角的可用分组列表解析实际倍率（用户专属 > 等级 > 分组档位）
	groupService.SetUserRatesReader(userService)
	// 用户列表的并发/RPM 观测：并发读用户槽（dep.Concurrency），RPM 读取器
	// 与 relay 管线共用同一套 Redis key（rpm:user:*），实例无状态可各建各的。
	userService.SetRuntimeStatsReaders(dep.Concurrency, rpmCounter)

	// 余额预警回调：从设置读取 SMTP 配置发送邮件
	userService.SetBalanceAlertCallback(func(email string, balance float64, threshold float64) error {
		return balanceAlertSendEmail(settingsService, email, balance, threshold)
	})
	// 生产环境固定 Postgres（见 cmd/server/main.go），显式传入方言以启用趋势聚合下推
	usageStore := store.NewUsageStore(dep.DB, dialect.Postgres)
	usageService := appusage.NewService(usageStore, dep.Redis)
	upstreamLogStore := store.NewUpstreamLogStore(dep.DB)
	upstreamLogService := appupstreamlog.NewService(upstreamLogStore)

	paymentStore := store.NewPaymentStore(dep.DB)
	paymentService := apppayment.NewService(paymentStore, paymentSettingsAdapter{settingsService}, dep.Config.APIKeySecret())
	// 充值成功邮件：首次入账成功后从设置读取 SMTP + 模板发送
	paymentService.SetRechargeSuccessCallback(func(email string, amount, balance float64) {
		rechargeSuccessSendEmail(settingsService, email, amount, balance)
	})
	// 邀请返利：仅真实付款到账触发计提
	paymentService.SetRebateAccruer(inviteService)

	redemptionStore := store.NewRedemptionStore(dep.DB)
	redemptionService := appredemption.NewService(redemptionStore)

	// 风控中心：service 兼任审核引擎的 ConfigSource（读配置+解密）与
	// UserBanner（自动封禁，管理员豁免）；hash 缓存走 Redis（未配置则禁用该能力）。
	moderationLogStore := store.NewModerationLogStore(dep.DB)
	var moderationHashCache moderation.HashCache
	if dep.Redis != nil {
		moderationHashCache = moderation.NewRedisHashCache(dep.Redis)
	}
	riskControlService := appriskcontrol.NewService(settingsStore, moderationLogStore, moderationHashCache, userStore, dep.Config.APIKeySecret())
	moderationEngine := moderation.NewEngine(riskControlService, moderationLogStore, moderationHashCache, riskControlService)
	moderationEngine.SetNotifier(newModerationNotifier(settingsService))
	riskControlService.SetEngine(moderationEngine)

	bookmarkStore := store.NewBookmarkStore(dep.DB)
	bookmarkService := appbookmark.NewService(bookmarkStore)

	proxyStore := store.NewProxyStore(dep.DB, dep.Config.APIKeySecret())
	proxyService := appproxy.NewService(proxyStore)
	accountStore := store.NewAccountStore(dep.DB)
	accountService := appaccount.NewService(accountStore, dep.Config.APIKeySecret())
	// 账号统计复用 usage 仓储（usage_logs.account_id 聚合）
	accountService.SetUsageStatsRepo(usageStore)
	// 账号列表实时并发 / RPM：与 relay 账号闸门共用 Redis key
	accountService.SetRuntimeStatsReaders(dep.Concurrency, rpmCounter)

	// OAuth 应用接入：客户端仓储兼任 UserReader，授权码/令牌走 Redis，
	// provision-key 复用 apikey 服务的 get-or-create，可用分组适配 group 服务。
	oauthClientStore := store.NewOAuthClientStore(dep.DB)
	oauthGrantStore := store.NewOAuthGrantStore(dep.Redis)
	oauthService := appoauth.NewService(oauthClientStore, oauthGrantStore, oauthClientStore, oauthGroupAdapter{groupService}, apiKeyService)
	walletService := appwallet.NewService(store.NewWalletStore(dep.DB))
	oauthService.SetWalletManager(oauthWalletAdapter{svc: walletService})
	oauthService.SetPaymentManager(oauthPaymentAdapter{svc: paymentService})
	oauthService.SetBalanceLogReader(oauthBalanceLogAdapter{svc: userService})

	return &HTTPHandlers{
		Auth:         handler.NewAuthHandler(authService, dep.JWTMgr),
		User:         handler.NewUserHandler(userService, settingsService),
		Group:        handler.NewGroupHandler(groupService),
		Tier:         handler.NewTierHandler(tierService),
		Announcement: handler.NewAnnouncementHandler(announcementService),
		APIKey:       handler.NewAPIKeyHandler(apiKeyService),
		Usage:        handler.NewUsageHandler(usageService),
		UpstreamLog:  handler.NewUpstreamLogHandler(upstreamLogService),
		Channel:      handler.NewChannelHandler(channelService),
		ModelPrice:   handler.NewModelPriceHandler(modelPriceService),
		Settings:     handler.NewSettingsHandler(settingsService),
		Dashboard:    handler.NewDashboardHandler(dashboardService),
		Payment:      handler.NewPaymentHandler(paymentService),
		Redemption:   handler.NewRedemptionHandler(redemptionService),
		Invite:       handler.NewInviteHandler(inviteService),
		Version:      handler.NewVersionHandler(),
		OAuth:        handler.NewOAuthHandler(oauthService),
		RiskControl:  handler.NewRiskControlHandler(riskControlService),
		Bookmark:     handler.NewBookmarkHandler(bookmarkService),
		Account:      handler.NewAccountHandler(accountService),
		Proxy:        handler.NewProxyHandler(proxyService),
		RequestAudit: handler.NewRequestAuditHandler(requestAuditService),

		ChannelService:        channelService,
		AccountService:        accountService,
		ProxyService:          proxyService,
		ModelPriceService:     modelPriceService,
		SettingsService:       settingsService,
		UpstreamLogService:    upstreamLogService,
		PaymentService:        paymentService,
		UserService:           userService,
		TaskStore:             store.NewTaskStore(dep.DB),
		ModerationEngine:      moderationEngine,
		ChannelStore:          channelStore,
		ChannelHealthNotifier: channelHealthNotifier,
		RequestAuditService:   requestAuditService,
	}
}

// oauthWalletAdapter 将 wallet.Service 的领域结果映射到 OAuth 协议域。
type oauthWalletAdapter struct {
	svc *appwallet.Service
}

// oauthBalanceLogAdapter 将 user.Service 的余额历史映射到 OAuth 协议域。
type oauthBalanceLogAdapter struct {
	svc *appuser.Service
}

func (a oauthBalanceLogAdapter) ListBalanceLogs(ctx context.Context, userID, page, pageSize int) (appoauth.BalanceLogList, error) {
	result, err := a.svc.ListBalanceLogs(ctx, userID, page, pageSize)
	if err != nil {
		return appoauth.BalanceLogList{}, err
	}
	list := make([]appoauth.BalanceLog, 0, len(result.List))
	for _, item := range result.List {
		list = append(list, appoauth.BalanceLog{
			ID: item.ID, Action: item.Action, Amount: item.Amount,
			BeforeBalance: item.BeforeBalance, AfterBalance: item.AfterBalance,
			Remark: item.Remark, CreatedAt: item.CreatedAt,
		})
	}
	return appoauth.BalanceLogList{List: list, Total: result.Total, Page: result.Page, PageSize: result.PageSize}, nil
}

// oauthPaymentAdapter 将 payment.Service 的领域结果映射到 OAuth 协议域。
type oauthPaymentAdapter struct {
	svc *apppayment.Service
}

func (a oauthPaymentAdapter) AvailableMethods(ctx context.Context) appoauth.PaymentMethodsResult {
	result := a.svc.AvailableMethods(ctx)
	methods := make([]appoauth.PaymentMethod, 0, len(result.Methods))
	for _, method := range result.Methods {
		methods = append(methods, appoauth.PaymentMethod{
			Key: method.Key, Label: method.Label, Icon: method.Icon, Description: method.Description,
		})
	}
	return appoauth.PaymentMethodsResult{Methods: methods, Configured: result.Configured}
}

func (a oauthPaymentAdapter) CreateOrder(ctx context.Context, userID int, amount float64, method, subject, clientIP, returnURL string) (appoauth.PaymentOrder, error) {
	order, err := a.svc.CreateOrder(ctx, apppayment.CreateOrderInput{
		UserID: userID, Amount: amount, Method: method, Subject: subject, ClientIP: clientIP, ReturnURL: returnURL,
	})
	if err != nil {
		return appoauth.PaymentOrder{}, err
	}
	return toOAuthPaymentOrder(order), nil
}

func (a oauthPaymentAdapter) GetUserOrder(ctx context.Context, userID int, outTradeNo string) (appoauth.PaymentOrder, error) {
	order, err := a.svc.GetUserOrder(ctx, userID, outTradeNo)
	if err != nil {
		return appoauth.PaymentOrder{}, err
	}
	return toOAuthPaymentOrder(order), nil
}

func (a oauthPaymentAdapter) ListUserOrders(ctx context.Context, userID, page, pageSize int) ([]appoauth.PaymentOrder, int64, error) {
	orders, total, err := a.svc.ListUserOrders(ctx, apppayment.UserOrderFilter{UserID: userID, Page: page, PageSize: pageSize})
	if err != nil {
		return nil, 0, err
	}
	out := make([]appoauth.PaymentOrder, 0, len(orders))
	for _, order := range orders {
		out = append(out, toOAuthPaymentOrder(order))
	}
	return out, total, nil
}

func toOAuthPaymentOrder(order apppayment.Order) appoauth.PaymentOrder {
	return appoauth.PaymentOrder{
		OutTradeNo: order.OutTradeNo, Method: order.Method, ProviderID: order.ProviderID,
		Amount: order.Amount, Status: order.Status, Subject: order.Subject,
		PaymentURL: order.PaymentURL, QRCodeContent: order.QRCodeContent, PaidAt: order.PaidAt,
		ExpiresAt: order.ExpiresAt, CreatedAt: order.CreatedAt, UpdatedAt: order.UpdatedAt,
	}
}

func (a oauthWalletAdapter) Debit(ctx context.Context, userID int, clientID, externalOrderNo, amount, subject string) (appoauth.WalletTransaction, error) {
	result, err := a.svc.Debit(ctx, userID, clientID, externalOrderNo, amount, subject)
	if err != nil {
		return appoauth.WalletTransaction{}, err
	}
	return appoauth.WalletTransaction{
		TransactionID: result.TransactionID,
		Balance:       result.AfterBalance,
		Idempotent:    result.Idempotent,
	}, nil
}

func (a oauthWalletAdapter) Refund(ctx context.Context, userID int, clientID, externalRefundNo, relatedTransactionID, amount, reason string) (appoauth.WalletTransaction, error) {
	result, err := a.svc.Refund(ctx, userID, clientID, externalRefundNo, relatedTransactionID, amount, reason)
	if err != nil {
		return appoauth.WalletTransaction{}, err
	}
	return appoauth.WalletTransaction{
		TransactionID: result.TransactionID,
		Balance:       result.AfterBalance,
		Idempotent:    result.Idempotent,
	}, nil
}

// oauthGroupAdapter 将 appgroup.Service 适配为 appoauth.GroupReader 接口。
type oauthGroupAdapter struct {
	svc *appgroup.Service
}

func (a oauthGroupAdapter) AvailableForUser(ctx context.Context, userID int) ([]appoauth.GroupInfo, error) {
	list, err := a.svc.AvailableForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]appoauth.GroupInfo, len(list))
	for i, g := range list {
		// 对外部应用展示解析后的实际倍率（用户专属 > 等级 > 分组档位），未解析时回退分组档位
		rate := g.RateMultiplier
		if g.EffectiveRate > 0 {
			rate = g.EffectiveRate
		}
		out[i] = appoauth.GroupInfo{ID: g.ID, Name: g.Name, RateMultiplier: rate, Note: g.Note}
	}
	return out, nil
}

// paymentSettingsAdapter 将 appsettings.Service 适配为 apppayment.SettingsLister 接口。
type paymentSettingsAdapter struct {
	svc *appsettings.Service
}

func (a paymentSettingsAdapter) List(ctx context.Context, group string) ([]apppayment.SettingItem, error) {
	items, err := a.svc.List(ctx, group)
	if err != nil {
		return nil, err
	}
	out := make([]apppayment.SettingItem, len(items))
	for i, item := range items {
		out[i] = apppayment.SettingItem{Key: item.Key, Value: item.Value}
	}
	return out, nil
}

// inviteSettingsAdapter 将 appsettings.Service 适配为 appinvite.SettingsLister 接口。
type inviteSettingsAdapter struct {
	svc *appsettings.Service
}

func (a inviteSettingsAdapter) List(ctx context.Context, group string) ([]appinvite.SettingItem, error) {
	items, err := a.svc.List(ctx, group)
	if err != nil {
		return nil, err
	}
	out := make([]appinvite.SettingItem, len(items))
	for i, item := range items {
		out[i] = appinvite.SettingItem{Key: item.Key, Value: item.Value}
	}
	return out, nil
}

// settingsAdapter 将 appsettings.Service 适配为 appauth.SettingsLister 接口。
type settingsAdapter struct {
	svc *appsettings.Service
}

func (a *settingsAdapter) List(ctx context.Context, group string) ([]appauth.Setting, error) {
	items, err := a.svc.List(ctx, group)
	if err != nil {
		return nil, err
	}
	result := make([]appauth.Setting, len(items))
	for i, item := range items {
		result[i] = appauth.Setting{Key: item.Key, Value: item.Value}
	}
	return result, nil
}

// NewUserDefaults 委托给 settings 服务（新用户默认余额/并发数的唯一解析点）。
func (a *settingsAdapter) NewUserDefaults(ctx context.Context) (float64, int) {
	return a.svc.NewUserDefaults(ctx)
}

// smtpConfigFromSettings 把 smtp 组设置项解析为 mailer.Config（Port 缺省 587）。
// 验证码邮件与余额预警邮件共用此解析，避免两处漂移。
func smtpConfigFromSettings(items []appsettings.Setting) mailer.Config {
	cfg := mailer.Config{}
	for _, s := range items {
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
	if cfg.Port == 0 {
		cfg.Port = 587
	}
	return cfg
}

// buildMailerFactory 返回一个从系统设置构建邮件发送器的工厂函数。
func buildMailerFactory(settingsService *appsettings.Service) appauth.MailSenderFactory {
	return func(ctx context.Context) (appauth.MailSender, error) {
		settings, err := settingsService.List(ctx, "smtp")
		if err != nil {
			return nil, err
		}
		cfg := smtpConfigFromSettings(settings)
		if cfg.Host == "" {
			return nil, fmt.Errorf("SMTP 未配置")
		}
		return mailer.New(cfg), nil
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

// balanceAlertSendEmail 发送余额预警邮件；返回值供调用方判断是否需要
// 回滚 notified 标记以便下次消费重试（SMTP 未配置/发送失败都应可重试）。
func balanceAlertSendEmail(settingsService *appsettings.Service, email string, balance, threshold float64) error {
	ctx := context.Background()

	// 读取 SMTP 配置
	smtpSettings, err := settingsService.List(ctx, "smtp")
	if err != nil {
		slog.Error("balance_alert_smtp_load_failed", logx.LogFieldError, err)
		return err
	}
	cfg := smtpConfigFromSettings(smtpSettings)
	if cfg.Host == "" {
		slog.Warn("mail_disabled_no_config", "context", "balance_alert")
		return fmt.Errorf("SMTP 未配置")
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
		slog.Error("balance_alert_email_failed", "to_hash", store.EmailHash(email), logx.LogFieldError, err)
		return err
	}
	slog.Info("balance_alert_email_sent",
		"to_hash", store.EmailHash(email),
		"balance", balance,
		"threshold", threshold)
	return nil
}

// defaultRechargeBody 充值成功邮件默认正文模板。
const defaultRechargeBody = `<div style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; max-width: 420px; margin: 0 auto; background: #ffffff; border-radius: 8px; border: 1px solid #e5e7eb;">
<div style="padding: 32px 28px;">
<div style="font-size: 16px; font-weight: 600; color: #111; margin-bottom: 20px;">{{site_name}}</div>
<p style="color: #555; font-size: 14px; line-height: 1.6; margin: 0 0 16px;">您的充值已成功到账：</p>
<div style="background: #d1fae5; border: 1px solid #a7f3d0; border-radius: 8px; padding: 16px; margin-bottom: 20px;">
<div style="display: flex; justify-content: space-between; margin-bottom: 8px;">
<span style="color: #065f46; font-size: 13px;">充值金额</span>
<span style="color: #065f46; font-size: 16px; font-weight: 700;">{{amount}}</span>
</div>
<div style="display: flex; justify-content: space-between;">
<span style="color: #065f46; font-size: 13px;">当前余额</span>
<span style="color: #065f46; font-size: 13px;">{{balance}}</span>
</div>
</div>
<p style="color: #999; font-size: 12px; line-height: 1.6; margin: 0;">感谢您的支持，祝您使用愉快。</p>
</div>
<div style="border-top: 1px solid #f0f0f0; padding: 14px 28px;">
<p style="color: #c0c0c0; font-size: 11px; margin: 0; text-align: center;">此邮件由 {{site_name}} 系统自动发送</p>
</div>
</div>`

// rechargeSuccessSendEmail 发送充值成功邮件（首次入账成功后异步调用）。
func rechargeSuccessSendEmail(settingsService *appsettings.Service, email string, amount, balance float64) {
	ctx := context.Background()

	// 读取 SMTP 配置
	smtpSettings, err := settingsService.List(ctx, "smtp")
	if err != nil {
		slog.Error("recharge_email_smtp_load_failed", logx.LogFieldError, err)
		return
	}
	cfg := smtpConfigFromSettings(smtpSettings)
	if cfg.Host == "" {
		slog.Warn("mail_disabled_no_config", "context", "recharge_success")
		return
	}

	// 读取站点名称及充值成功邮件模板
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
		case "recharge_email_subject":
			tplSubject = s.Value
		case "recharge_email_body":
			tplBody = s.Value
		}
	}

	amountStr := fmt.Sprintf("%.2f", amount)
	balanceStr := fmt.Sprintf("$%.4f", balance)

	if tplSubject == "" {
		tplSubject = "{{site_name}} - 充值成功"
	}
	if tplBody == "" {
		tplBody = defaultRechargeBody
	}

	replacer := strings.NewReplacer(
		"{{site_name}}", siteName,
		"{{amount}}", amountStr,
		"{{balance}}", balanceStr,
	)
	subject := replacer.Replace(tplSubject)
	body := replacer.Replace(tplBody)

	m := mailer.New(cfg)
	if err := m.Send(email, subject, body); err != nil {
		slog.Error("recharge_email_failed", "to_hash", store.EmailHash(email), logx.LogFieldError, err)
	} else {
		slog.Info("recharge_email_sent", "to_hash", store.EmailHash(email), "amount", amount, "balance", balance)
	}
}
