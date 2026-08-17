package server

import (
	"io/fs"
	"log/slog"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
	webfs "github.com/DouDOU-start/airgate-core/internal/web"
)

// registerRoutes 注册所有 API 路由
func (s *Server) registerRoutes() {
	r := s.engine
	handlers := s.handlers

	// 全局中间件：CORS → Recovery → RequestLogger → 业务
	r.Use(middleware.CORS(middleware.CORSConfig{
		// 默认不设置 AllowOrigins，仅同源可访问。
		// 如需跨域请配置具体来源，例如：AllowOrigins: []string{"https://example.com"}
	}))
	r.Use(middleware.Recovery())
	r.Use(middleware.RequestLogger())

	// 健康检查（无需认证，供 docker / k8s healthcheck 使用）
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// API v1 路由组
	v1 := r.Group("/api/v1")

	// === 公共路由（无需认证） ===
	v1.GET("/settings/public", handlers.Settings.GetPublicSettings)

	// 模型广场（未登录可见的模型价格 + 倍率区间）：IP 限流防刷。
	modelMarketRL := middleware.NewIPRateLimit(60)
	s.modelMarketRateLimiter = modelMarketRL.Limiter
	v1.GET("/model-market", modelMarketRL.Handler, handlers.ModelPrice.PublicListModelMarket)

	// === 认证路由（无需 JWT） ===
	//
	// 基于客户端 IP 的速率限制（10 req/min），防止暴力破解和验证码滥用。
	// 替代原来依赖 CtxKeyUserID 的用户级限流（登录前无 user_id，实际空转已移除）。
	authGroup := v1.Group("/auth")
	ipRL := middleware.NewIPRateLimit(10)
	s.ipRateLimiter = ipRL.Limiter
	authGroup.Use(ipRL.Handler)
	{
		authGroup.POST("/login", handlers.Auth.Login)
		authGroup.POST("/login-apikey", handlers.Auth.LoginByAPIKey)
		authGroup.POST("/register", handlers.Auth.Register)
		authGroup.POST("/send-verify-code", handlers.Auth.SendVerifyCode)
		authGroup.POST("/verify-code", handlers.Auth.VerifyCode)
	}

	// Token 刷新（独立于 JWT 中间件，允许过期 token 刷新）
	v1.POST("/auth/refresh", handlers.Auth.RefreshToken)

	// === 用户路由（需要 JWT 认证） ===
	userGroup := v1.Group("")
	userGroup.Use(middleware.JWTAuth(s.jwtMgr))
	{
		accountGroup := userGroup.Group("")
		accountGroup.Use(middleware.RequireRoles("admin", "user"))

		// 用户资料
		userGroup.GET("/users/me", handlers.User.GetMe)
		accountGroup.PUT("/users/me", handlers.User.UpdateProfile)
		accountGroup.POST("/users/me/password", handlers.User.ChangePassword)
		accountGroup.PUT("/users/me/balance-alert", handlers.User.UpdateBalanceAlert)
		accountGroup.GET("/users/me/balance-history", handlers.User.GetMyBalanceHistory)

		// API Key 管理
		accountGroup.GET("/api-keys", handlers.APIKey.ListKeys)
		accountGroup.POST("/api-keys", handlers.APIKey.CreateKey)
		accountGroup.PUT("/api-keys/:id", handlers.APIKey.UpdateKey)
		accountGroup.DELETE("/api-keys/:id", handlers.APIKey.DeleteKey)
		accountGroup.GET("/api-keys/:id/reveal", handlers.APIKey.RevealKey)

		// 分组
		accountGroup.GET("/groups", handlers.Group.ListAvailableGroups)

		// 渠道状态（仅当前用户可访问且 status_visible=true 的分组，脱敏只读）
		accountGroup.GET("/channel-status/overview", handlers.Healthmon.UserOverview)
		accountGroup.GET("/channel-status/groups", handlers.Healthmon.UserGroups)

		// 公告（用户端：查看 + 标已读）
		accountGroup.GET("/announcements", handlers.Announcement.ListMyAnnouncements)
		accountGroup.POST("/announcements/read-all", handlers.Announcement.MarkAllAnnouncementsRead)
		accountGroup.POST("/announcements/:id/read", handlers.Announcement.MarkAnnouncementRead)

		// 使用记录
		userGroup.GET("/usage", handlers.Usage.UserUsage)
		userGroup.GET("/usage/stats", handlers.Usage.UserUsageStats)
		userGroup.GET("/usage/trend", handlers.Usage.UserUsageTrend)
		// 用户视角失败请求（脱敏：不含渠道/重试链/IP/UA）
		userGroup.GET("/usage/upstream-logs", handlers.UpstreamLog.UserList)

		// 在线充值（仅真实用户会话；API Key 登录的 end customer 不可充值）
		accountGroup.GET("/payment/methods", handlers.Payment.ListMethods)
		accountGroup.POST("/payment/orders", handlers.Payment.CreateOrder)
		accountGroup.GET("/payment/orders", handlers.Payment.ListUserOrders)
		accountGroup.GET("/payment/orders/:out_trade_no", handlers.Payment.GetUserOrder)

		// 兑换码充值（同充值：仅真实用户会话）
		accountGroup.POST("/redeem", handlers.Redemption.Redeem)

		// 邀请返利（用户端）
		accountGroup.GET("/invite/me", handlers.Invite.GetMe)
		accountGroup.GET("/invite/invitees", handlers.Invite.ListMyInvitees)
		accountGroup.GET("/invite/logs", handlers.Invite.ListMyRebateLogs)
		accountGroup.POST("/invite/transfer", handlers.Invite.Transfer)

		// OAuth 应用授权（仅真实用户会话；SPA 授权页转发）+ 应用导航入口
		accountGroup.GET("/oauth/authorize-info", handlers.OAuth.GetAuthorizeInfo)
		accountGroup.POST("/oauth/authorize", handlers.OAuth.Authorize)
		accountGroup.GET("/apps", handlers.OAuth.ListApps)
	}

	// === 管理员路由（需要管理员 JWT + AdminOnly，支持 admin- 管理员 API Key） ===
	adminGroup := v1.Group("/admin")
	adminGroup.Use(middleware.JWTAuth(s.jwtMgr, s.db), middleware.AdminOnly())
	{
		// 用户管理
		adminGroup.GET("/users", handlers.User.ListUsers)
		adminGroup.POST("/users", handlers.User.CreateUser)
		adminGroup.PUT("/users/:id", handlers.User.UpdateUser)
		adminGroup.DELETE("/users/:id", handlers.User.DeleteUser)
		adminGroup.PATCH("/users/:id/toggle", handlers.User.ToggleUserStatus)
		adminGroup.POST("/users/:id/balance", handlers.User.AdjustBalance)
		adminGroup.GET("/users/:id/balance-history", handlers.User.GetUserBalanceHistory)
		adminGroup.GET("/users/:id/api-keys", handlers.User.AdminListUserKeys)

		// 分组管理
		adminGroup.GET("/groups", handlers.Group.ListGroups)
		adminGroup.POST("/groups", handlers.Group.CreateGroup)
		adminGroup.GET("/groups/:id", handlers.Group.GetGroup)
		adminGroup.PUT("/groups/:id", handlers.Group.UpdateGroup)
		adminGroup.DELETE("/groups/:id", handlers.Group.DeleteGroup)

		// 公告管理
		adminGroup.GET("/announcements", handlers.Announcement.ListAnnouncements)
		adminGroup.POST("/announcements", handlers.Announcement.CreateAnnouncement)
		adminGroup.GET("/announcements/:id", handlers.Announcement.GetAnnouncement)
		adminGroup.PUT("/announcements/:id", handlers.Announcement.UpdateAnnouncement)
		adminGroup.DELETE("/announcements/:id", handlers.Announcement.DeleteAnnouncement)

		// 用户等级管理（等级×分组倍率，批量分层定价）
		adminGroup.GET("/tiers", handlers.Tier.ListTiers)
		adminGroup.POST("/tiers", handlers.Tier.CreateTier)
		adminGroup.GET("/tiers/:id", handlers.Tier.GetTier)
		adminGroup.PUT("/tiers/:id", handlers.Tier.UpdateTier)
		adminGroup.DELETE("/tiers/:id", handlers.Tier.DeleteTier)

		// 分组专属倍率管理（reverse 视角：某个分组下哪些用户有专属倍率）
		adminGroup.GET("/groups/:id/rate-overrides", handlers.User.ListGroupRateOverrides)
		adminGroup.PUT("/groups/:id/rate-overrides/:userId", handlers.User.SetGroupRateOverride)
		adminGroup.DELETE("/groups/:id/rate-overrides/:userId", handlers.User.DeleteGroupRateOverride)

		// 专属分组用户管理：某个专属分组下开放了哪些用户访问
		adminGroup.GET("/groups/:id/allowed-users", handlers.Group.ListAllowedUsers)
		adminGroup.POST("/groups/:id/allowed-users/:userId", handlers.Group.GrantAllowedUser)
		adminGroup.DELETE("/groups/:id/allowed-users/:userId", handlers.Group.RevokeAllowedUser)

		// 分组渠道 key 绑定管理：绑定/解绑；查询用现成的 GET /channels/keys?group_id= 即可
		adminGroup.POST("/groups/:id/channel-keys/:keyId", handlers.Group.BindChannelKey)
		adminGroup.DELETE("/groups/:id/channel-keys/:keyId", handlers.Group.UnbindChannelKey)

		// API 密钥管理（管理员）
		adminGroup.GET("/api-keys", handlers.APIKey.AdminListKeys)
		adminGroup.PUT("/api-keys/:id", handlers.APIKey.AdminUpdateKey)

		// 渠道管理
		adminGroup.GET("/channels", handlers.Channel.ListChannels)
		// 密钥视图：跨渠道平铺分页（静态段，先于 /channels/:id 匹配）
		adminGroup.GET("/channels/keys", handlers.Channel.ListChannelKeys)
		// 渠道导出 / 导入（静态段，先于 /channels/:id 匹配）
		adminGroup.GET("/channels/export", handlers.Channel.ExportChannels)
		adminGroup.POST("/channels/import", handlers.Channel.ImportChannels)
		adminGroup.POST("/channels", handlers.Channel.CreateChannel)
		adminGroup.PUT("/channels/:id", handlers.Channel.UpdateChannel)
		adminGroup.DELETE("/channels/:id", handlers.Channel.DeleteChannel)
		// 渠道下新增一把 key
		adminGroup.POST("/channels/:id/keys", handlers.Channel.AddChannelKey)
		// 密钥端点级操作（:id 为 channel_key_id）：更新 / 删除 / 测试 / 拉模型 / 刷余额
		adminGroup.PUT("/channels/keys/:id", handlers.Channel.UpdateChannelKey)
		adminGroup.DELETE("/channels/keys/:id", handlers.Channel.DeleteChannelKey)
		adminGroup.POST("/channels/keys/:id/test", handlers.Channel.TestChannel)
		adminGroup.POST("/channels/keys/:id/fetch-models", handlers.Channel.FetchChannelModels)
		adminGroup.POST("/channels/keys/:id/balance", handlers.Channel.RefreshChannelBalance)
		adminGroup.POST("/channels/keys/:id/upstream-rate", handlers.Channel.RefreshUpstreamRate)
		// 预览拉取：key 未保存时按表单连接参数试拉模型（静态段，先于 :id 匹配）
		adminGroup.POST("/channels/fetch-models", handlers.Channel.FetchChannelModelsPreview)
		adminGroup.POST("/channels/bulk-update", handlers.Channel.BulkUpdateChannels)
		// 渠道近 N 分钟失败计数（errlog Redis 分钟桶，渠道页监控列）
		adminGroup.GET("/channels/failure-stats", handlers.UpstreamLog.ChannelFailureStats)

		// 模型价格
		adminGroup.GET("/model-prices", handlers.ModelPrice.ListModelPrices)
		adminGroup.GET("/model-prices/sync-candidates", handlers.ModelPrice.ListModelPriceSyncCandidates)
		adminGroup.POST("/model-prices/sync", handlers.ModelPrice.SyncModelPrices)
		adminGroup.POST("/model-prices/sync-selected", handlers.ModelPrice.SyncSelectedModelPrices)
		adminGroup.POST("/model-prices/bulk-update", handlers.ModelPrice.BulkUpdateModelPrices)
		adminGroup.POST("/model-prices", handlers.ModelPrice.CreateModelPrice)
		adminGroup.PUT("/model-prices/:id", handlers.ModelPrice.UpdateModelPrice)
		adminGroup.DELETE("/model-prices/:id", handlers.ModelPrice.DeleteModelPrice)

		// 模型标签（家族归类，归属模型管理）
		adminGroup.GET("/model-tags", handlers.ModelPrice.ListModelTags)
		adminGroup.POST("/model-tags", handlers.ModelPrice.CreateModelTag)
		adminGroup.PUT("/model-tags/:id", handlers.ModelPrice.UpdateModelTag)
		adminGroup.DELETE("/model-tags/:id", handlers.ModelPrice.DeleteModelTag)

		// 使用记录（管理员）
		adminGroup.GET("/usage", handlers.Usage.AdminUsage)
		adminGroup.GET("/usage/stats", handlers.Usage.AdminUsageStats)
		adminGroup.GET("/usage/trend", handlers.Usage.AdminUsageTrend)

		// 上游请求日志（失败留痕/渠道测试/拉模型，仅管理员）
		adminGroup.GET("/upstream-logs", handlers.UpstreamLog.AdminList)
		// 完整请求审计（解密后的 Header/Body 仅管理员可见）
		adminGroup.GET("/request-audits", handlers.RequestAudit.List)
		adminGroup.GET("/request-audits/stats", handlers.RequestAudit.Stats)
		adminGroup.DELETE("/request-audits", handlers.RequestAudit.Clear)
		adminGroup.GET("/request-audits/:id", handlers.RequestAudit.Get)

		// 健康监测（真实流量统计，只读观测面）
		adminGroup.GET("/health-monitor/overview", handlers.Healthmon.Overview)
		adminGroup.GET("/health-monitor/entities", handlers.Healthmon.Entities)

		// 风控中心（内容审核）：配置/状态/探活/日志/解封/命中哈希管理
		adminGroup.GET("/risk-control/config", handlers.RiskControl.GetConfig)
		adminGroup.PUT("/risk-control/config", handlers.RiskControl.UpdateConfig)
		adminGroup.GET("/risk-control/status", handlers.RiskControl.GetStatus)
		adminGroup.POST("/risk-control/api-keys/test", handlers.RiskControl.TestAPIKeys)
		adminGroup.GET("/risk-control/logs", handlers.RiskControl.ListLogs)
		adminGroup.DELETE("/risk-control/logs", handlers.RiskControl.ClearLogs)
		adminGroup.POST("/risk-control/users/:id/unban", handlers.RiskControl.UnbanUser)
		adminGroup.DELETE("/risk-control/hashes", handlers.RiskControl.DeleteHash)
		adminGroup.DELETE("/risk-control/hashes/all", handlers.RiskControl.ClearHashes)

		// 代理管理
		adminGroup.GET("/proxies", handlers.Proxy.ListProxies)
		adminGroup.POST("/proxies", handlers.Proxy.CreateProxy)
		adminGroup.PUT("/proxies/:id", handlers.Proxy.UpdateProxy)
		adminGroup.DELETE("/proxies/:id", handlers.Proxy.DeleteProxy)
		adminGroup.POST("/proxies/:id/test", handlers.Proxy.TestProxy)

		// 上游账号管理（静态段先于 :id）
		adminGroup.GET("/accounts", handlers.Account.ListAccounts)
		adminGroup.GET("/accounts/platforms", handlers.Account.ListAccountPlatforms)
		adminGroup.GET("/accounts/export", handlers.Account.ExportAccounts)
		adminGroup.POST("/accounts/import", handlers.Account.ImportAccounts)
		adminGroup.POST("/accounts/bulk-update", handlers.Account.BulkUpdateAccounts)
		adminGroup.POST("/accounts/bulk-delete", handlers.Account.BulkDeleteAccounts)
		adminGroup.GET("/accounts/credentials-schema/:platform", handlers.Account.GetCredentialsSchema)
		adminGroup.GET("/accounts/oauth/:platform/hints", handlers.Account.GetOAuthHints)
		adminGroup.POST("/accounts/oauth/:platform/start", handlers.Account.StartOAuth)
		adminGroup.GET("/accounts/oauth/sessions/:sessionId", handlers.Account.GetOAuthSession)
		adminGroup.POST("/accounts/oauth/sessions/:sessionId/complete", handlers.Account.CompleteOAuth)
		// OAuth 专用导入（浏览器授权走 oauth/:platform/start）
		adminGroup.POST("/accounts/oauth/codex/import-refresh", handlers.Account.ImportCodexRefresh)
		adminGroup.POST("/accounts/oauth/codex/import-access-token", handlers.Account.ImportCodexAccessToken)
		adminGroup.POST("/accounts/oauth/codex/import-session", handlers.Account.ImportCodexSession)
		adminGroup.POST("/accounts/oauth/antigravity/import-refresh", handlers.Account.ImportAntigravityRefresh)
		adminGroup.POST("/accounts", handlers.Account.CreateAccount)
		adminGroup.PUT("/accounts/:id", handlers.Account.UpdateAccount)
		adminGroup.DELETE("/accounts/:id", handlers.Account.DeleteAccount)
		adminGroup.PATCH("/accounts/:id/toggle", handlers.Account.ToggleScheduling)
		adminGroup.POST("/accounts/:id/usage/refresh", handlers.Account.RefreshAccountUsage)
		adminGroup.POST("/accounts/:id/usage/reset", handlers.Account.ConsumeAccountUsageReset)
		adminGroup.GET("/accounts/:id/stats", handlers.Account.GetAccountUsageStats)
		adminGroup.GET("/accounts/:id/models", handlers.Account.ListAccountTestModels)
		adminGroup.POST("/accounts/:id/test", handlers.Account.TestAccountConnection)

		// 备忘录
		adminGroup.GET("/bookmarks", handlers.Bookmark.ListBookmarks)
		adminGroup.POST("/bookmarks", handlers.Bookmark.CreateBookmark)
		adminGroup.PUT("/bookmarks/:id", handlers.Bookmark.UpdateBookmark)
		adminGroup.DELETE("/bookmarks/:id", handlers.Bookmark.DeleteBookmark)

		// 插件管理（文件系统安装 + 多实例独立进程运行，不接入业务数据库）
		adminGroup.GET("/plugins", s.pluginHandler.ListPlugins)
		adminGroup.POST("/plugins/upload", s.pluginHandler.UploadPlugin)
		adminGroup.POST("/plugins/install-url", s.pluginHandler.InstallPluginFromURL)
		adminGroup.POST("/plugins/:id/upload", s.pluginHandler.UpdatePlugin)
		adminGroup.GET("/plugins/:id/config", s.pluginHandler.GetPluginConfig)
		adminGroup.PUT("/plugins/:id/config", s.pluginHandler.UpdatePluginConfig)
		adminGroup.PATCH("/plugins/:id/enabled", s.pluginHandler.SetPluginEnabled)
		adminGroup.POST("/plugins/:id/reload", s.pluginHandler.ReloadPlugin)
		adminGroup.DELETE("/plugins/:id", s.pluginHandler.UninstallPlugin)

		// 系统设置
		adminGroup.GET("/settings", handlers.Settings.GetSettings)
		adminGroup.PUT("/settings", handlers.Settings.UpdateSettings)
		adminGroup.POST("/settings/test-smtp", handlers.Settings.TestSMTP)
		adminGroup.POST("/settings/test-bark", handlers.Settings.TestBark)
		adminGroup.POST("/settings/upload", handlers.Settings.UploadFile)

		// 管理员 API Key
		adminGroup.GET("/settings/admin-api-key", handlers.Settings.GetAdminAPIKey)
		adminGroup.POST("/settings/admin-api-key", handlers.Settings.GenerateAdminAPIKey)
		adminGroup.DELETE("/settings/admin-api-key", handlers.Settings.DeleteAdminAPIKey)

		// OAuth 应用接入管理
		adminGroup.GET("/oauth-clients", handlers.OAuth.ListOAuthClients)
		adminGroup.POST("/oauth-clients", handlers.OAuth.CreateOAuthClient)
		adminGroup.PUT("/oauth-clients/:id", handlers.OAuth.UpdateOAuthClient)
		adminGroup.DELETE("/oauth-clients/:id", handlers.OAuth.DeleteOAuthClient)
		adminGroup.POST("/oauth-clients/:id/reset-secret", handlers.OAuth.ResetOAuthClientSecret)

		// 仪表盘（管理员）
		adminGroup.GET("/dashboard/stats", handlers.Dashboard.Stats)
		adminGroup.GET("/dashboard/trend", handlers.Dashboard.Trend)

		// core 版本信息（仅管理员可见，避免对外暴露版本指纹）
		adminGroup.GET("/version", handlers.Version.GetVersion)

		// 支付管理：订单总览 + 服务商实例配置（保存即热加载）
		adminGroup.GET("/payment/orders", handlers.Payment.AdminListOrders)
		adminGroup.GET("/payment/providers", handlers.Payment.AdminListProviders)
		adminGroup.POST("/payment/providers", handlers.Payment.AdminUpsertProvider)
		adminGroup.DELETE("/payment/providers/:id", handlers.Payment.AdminDeleteProvider)

		// 兑换码管理
		adminGroup.GET("/redemption-codes", handlers.Redemption.AdminListCodes)
		adminGroup.GET("/redemption-codes/stats", handlers.Redemption.AdminStats)
		adminGroup.POST("/redemption-codes", handlers.Redemption.AdminGenerateCodes)
		adminGroup.PATCH("/redemption-codes/:id/status", handlers.Redemption.AdminUpdateStatus)
		adminGroup.DELETE("/redemption-codes/:id", handlers.Redemption.AdminDeleteCode)

		// 邀请返利管理：专属比例覆盖 + 全量邀请关系/流水（总开关+全局比例走通用 /admin/settings?group=invite）
		adminGroup.GET("/invite/overrides", handlers.Invite.AdminListOverrides)
		adminGroup.PATCH("/invite/overrides/:userID", handlers.Invite.AdminSetOverride)
		adminGroup.GET("/invite/invitees", handlers.Invite.AdminListInvitees)
		adminGroup.GET("/invite/logs", handlers.Invite.AdminListRebateLogs)
	}

	// 加载嵌入的前端 SPA：所有静态资源通过 //go:embed 打进二进制
	distFS, err := webfs.FS()
	if err != nil {
		slog.Error("加载嵌入前端失败", "error", err)
		os.Exit(1)
	}
	indexHTML, _ := webfs.IndexHTML()
	ogIndex := newIndexHTMLRenderer(indexHTML, handlers.SettingsService)
	assetsFS, err := fs.Sub(distFS, "assets")
	if err != nil {
		slog.Error("嵌入前端缺少 assets 子目录", "error", err)
		os.Exit(1)
	}

	// === 对外网关路由（sk- API Key 鉴权，纯透传：入站端点按协议分树） ===
	// 显式静态注册（先于 NoRoute），错误体按入口协议出原生形态（errfmt 按 EntryProtocol 分发）。
	relayGroup := r.Group("/v1", middleware.APIKeyAuth(s.db))
	{
		// OpenAI 协议（openai_compatible 渠道）
		relayGroup.POST("/chat/completions", s.relay.HandleChatCompletions)
		relayGroup.POST("/responses", s.relay.HandleResponses)
		// codex CLI 内置联网搜索（openai 协议，POST 非流式，按次计费）
		relayGroup.POST("/alpha/search", s.relay.HandleAlphaSearch)
		relayGroup.POST("/images/generations", s.relay.HandleImagesGenerations)
		relayGroup.POST("/images/edits", s.relay.HandleImagesEdits)
		relayGroup.GET("/models", s.relay.HandleModels)
		// AirGate 计费信息端点：下游网关探测上游倍率用（级联场景）
		relayGroup.GET("/airgate/billing", s.handleBilling)
		// Anthropic 协议（anthropic 渠道）
		relayGroup.POST("/messages", s.relay.HandleMessages)
		relayGroup.POST("/messages/count_tokens", s.relay.HandleMessagesCountTokens)
		// OpenAI 视频任务（openai_video 渠道，Sora 形态；异步任务子系统 relay/task）
		relayGroup.POST("/videos", s.taskFlow.HandleVideoSubmit)
		relayGroup.POST("/videos/generations", s.taskFlow.HandleXAIVideoSubmit)
		relayGroup.GET("/videos/:task_id", s.taskFlow.HandleVideoGet)
		relayGroup.GET("/videos/:task_id/content", s.taskFlow.HandleVideoContent)
	}
	// 无 /v1 前缀别名：部分客户端（如 Codex）base_url 不带版本号，直接拼接
	// /chat/completions 等路径。与上面同协议、同 handler，纯路径别名，不新增业务逻辑。
	noPrefixGroup := r.Group("", middleware.APIKeyAuth(s.db))
	{
		noPrefixGroup.POST("/chat/completions", s.relay.HandleChatCompletions)
		noPrefixGroup.POST("/responses", s.relay.HandleResponses)
		noPrefixGroup.POST("/alpha/search", s.relay.HandleAlphaSearch)
		noPrefixGroup.POST("/images/generations", s.relay.HandleImagesGenerations)
		noPrefixGroup.POST("/images/edits", s.relay.HandleImagesEdits)
		noPrefixGroup.GET("/models", s.relay.HandleModels)
		noPrefixGroup.POST("/videos", s.taskFlow.HandleVideoSubmit)
		noPrefixGroup.POST("/videos/generations", s.taskFlow.HandleXAIVideoSubmit)
		noPrefixGroup.GET("/videos/:task_id", s.taskFlow.HandleVideoGet)
		noPrefixGroup.GET("/videos/:task_id/content", s.taskFlow.HandleVideoContent)
	}
	// Suno 音乐任务（suno 渠道，Suno-API 社区协议；错误体 {"code":"fail",...}）
	sunoGroup := r.Group("/suno", middleware.APIKeyAuth(s.db))
	{
		sunoGroup.POST("/submit/:action", s.taskFlow.HandleSunoSubmit)
		sunoGroup.POST("/fetch", s.taskFlow.HandleSunoFetch)
		sunoGroup.GET("/fetch/:task_id", s.taskFlow.HandleSunoFetchByID)
	}
	// Gemini 协议（gemini 渠道）：路径形如 /v1beta/models/{model}:generateContent，
	// ':' 在 gin 路由里只有段首才是参数语法、段中不是分隔符——用单参数段承载
	// "model:action"，handler 内自行解析（见 pipeline.HandleGenerateContent，
	// 动词含 generateContent / streamGenerateContent / predict / countTokens）。
	geminiGroup := r.Group("/v1beta", middleware.APIKeyAuth(s.db))
	{
		geminiGroup.POST("/models/:modelAction", s.relay.HandleGenerateContent)
		// Gemini 原生形态模型列表（{"models":[{"name":"models/<id>",...}]}）。
		geminiGroup.GET("/models", s.relay.HandleGeminiModels)
	}

	// === cc-switch 通用模板兼容端点（使用 sk-xxx API Key 自鉴权） ===
	// AirGate 安装脚本和 cc-switch 通用脚本可使用 /v1/usage 做 Key 校验和
	// 余额查询。该路径由 Core 直接处理，返回真实可用余额。
	// 实现见 cc_compat.go。端点自带轻量鉴权且逐次查库，挂 IP 限流防刷。
	// 限额取 300/min：NAT / 未透传 XFF 的反代场景下大量用户共享同一出口 IP，
	// 60/min 会被整体误伤为 429。
	ccUsageRL := middleware.NewIPRateLimit(300)
	s.ccUsageRateLimiter = ccUsageRL.Limiter
	r.GET("/v1/usage", ccUsageRL.Handler, s.handleCCCompatUserBalance)

	// === 支付平台异步回调（公开路由，验签在 provider 实现内完成） ===
	// 易支付系走 form/GET，微信 V3 / easypay 走 JSON body + header 签名。
	r.POST("/api/v1/payment/notify/:provider_id", handlers.Payment.HandleCallback)
	r.GET("/api/v1/payment/notify/:provider_id", handlers.Payment.HandleCallback)

	// OAuth 协议端点（公开，供外部应用后端调用，RFC 6749 原始响应形态）。
	// GET /oauth/authorize 是 SPA 路由（授权页），经 NoRoute 落到前端。
	// token 端点带 IP 限流，防 client_secret 爆破。
	oauthRL := middleware.NewIPRateLimit(60)
	s.oauthRateLimiter = oauthRL.Limiter
	r.POST("/oauth/token", oauthRL.Handler, handlers.OAuth.Token)
	r.GET("/oauth/userinfo", handlers.OAuth.UserInfo)
	r.POST("/oauth/provision-key", handlers.OAuth.ProvisionKey)
	r.GET("/oauth/wallet", handlers.OAuth.Wallet)
	r.GET("/oauth/wallet/history", handlers.OAuth.WalletHistory)
	r.POST("/oauth/wallet/debits", handlers.OAuth.DebitWallet)
	r.POST("/oauth/wallet/refunds", handlers.OAuth.RefundWallet)
	r.GET("/oauth/payment/methods", handlers.OAuth.PaymentMethods)
	r.POST("/oauth/payment/orders", handlers.OAuth.CreatePaymentOrder)
	r.GET("/oauth/payment/orders", handlers.OAuth.ListPaymentOrders)
	r.GET("/oauth/payment/orders/:out_trade_no", handlers.OAuth.GetPaymentOrder)

	// 上传文件静态服务（这部分仍然在磁盘上，因为是用户上传的运行时数据）
	//
	// ⚠️ 安全说明：此路径公开可访问，无需认证。上传的文件（如站点 logo）可能
	// 被嵌入外部链接中分享，因此保持公开。文件名使用 UUID 生成，不可枚举。
	// 如未来需要访问控制，应替换为带鉴权的路由组。
	// X-Content-Type-Options: nosniff——上传目录是用户可控内容，禁止浏览器
	// MIME 嗅探把文件当脚本/HTML 执行（配合上传侧的扩展名白名单纵深防御）。
	uploadsGroup := r.Group("/uploads", func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Next()
	})
	uploadsGroup.Static("", "data/uploads")

	// 静态文件服务（前端 SPA）
	r.StaticFS("/assets", http.FS(assetsFS))
	// og:image 等分享卡片用的根目录静态文件不在 assets/ 下，未显式注册会落进
	// NoRoute 兜底、被当成 index.html（text/html）吐回去——图片抓取方（微信等）
	// 拿到的不是真图，卡片配图会失效。这里显式暴露一个 embed.FS 的根文件。
	r.StaticFileFS("/og-cover.png", "og-cover.png", http.FS(distFS))

	// NoRoute: 未匹配路径回退前端 index.html。
	// P1 起对外网关路由（/v1/chat/completions 等）走显式注册，不再经 NoRoute 分发。
	r.NoRoute(func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", ogIndex.Bytes(c.Request.Context()))
	})
}
