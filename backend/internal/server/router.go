package server

import (
	"io/fs"
	"log/slog"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
	"github.com/DouDOU-start/airgate-core/internal/setup"
	webfs "github.com/DouDOU-start/airgate-core/internal/web"
)

// registerRoutes 注册所有 API 路由
func (s *Server) registerRoutes() {
	r := s.engine
	handlers := s.handlers

	// 全局中间件
	r.Use(middleware.I18n())

	// 健康检查（无需认证，供 docker / k8s healthcheck 使用）
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// 安装向导路由（无需认证）
	setup.RegisterRoutes(r)

	// API v1 路由组
	v1 := r.Group("/api/v1")

	// === 公共路由（无需认证） ===
	v1.GET("/settings/public", handlers.Settings.GetPublicSettings)

	// === 认证路由（无需 JWT，带限流保护） ===
	authGroup := v1.Group("/auth")
	authGroup.Use(middleware.RateLimit(s.limiter))
	{
		authGroup.POST("/login", handlers.Auth.Login)
		authGroup.POST("/login-apikey", handlers.Auth.LoginByAPIKey)
		authGroup.POST("/register", handlers.Auth.Register)
		authGroup.POST("/send-verify-code", handlers.Auth.SendVerifyCode)
	}

	// === 用户路由（需要 JWT 认证） ===
	userGroup := v1.Group("")
	userGroup.Use(middleware.JWTAuth(s.jwtMgr))
	{
		// Token 刷新
		userGroup.POST("/auth/refresh", handlers.Auth.RefreshToken)

		// 用户资料
		userGroup.GET("/users/me", handlers.User.GetMe)
		userGroup.PUT("/users/me", handlers.User.UpdateProfile)
		userGroup.POST("/users/me/password", handlers.User.ChangePassword)
		userGroup.PUT("/users/me/balance-alert", handlers.User.UpdateBalanceAlert)

		// API Key 管理
		userGroup.GET("/api-keys", handlers.APIKey.ListKeys)
		userGroup.POST("/api-keys", handlers.APIKey.CreateKey)
		userGroup.PUT("/api-keys/:id", handlers.APIKey.UpdateKey)
		userGroup.DELETE("/api-keys/:id", handlers.APIKey.DeleteKey)
		userGroup.GET("/api-keys/:id/reveal", handlers.APIKey.RevealKey)

		// 分组
		userGroup.GET("/groups", handlers.Group.ListAvailableGroups)

		// 订阅
		userGroup.GET("/subscriptions", handlers.Subscription.UserSubscriptions)
		userGroup.GET("/subscriptions/active", handlers.Subscription.ActiveSubscriptions)
		userGroup.GET("/subscriptions/progress", handlers.Subscription.SubscriptionProgress)

		// 使用记录
		userGroup.GET("/usage", handlers.Usage.UserUsage)
		userGroup.GET("/usage/stats", handlers.Usage.UserUsageStats)
		userGroup.GET("/usage/trend", handlers.Usage.UserUsageTrend)

		// 插件菜单（精简元信息：仅返回 name + frontend_pages，所有登录用户可访问，
		// 用于前端 AppShell 渲染插件提供的页面菜单项）
		userGroup.GET("/plugins/menu", handlers.Plugin.ListPluginMenu)
	}

	// === 管理员路由（需要 JWT + AdminOnly，支持管理员 API Key） ===
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

		// 账号管理
		adminGroup.GET("/accounts", handlers.Account.ListAccounts)
		adminGroup.GET("/accounts/usage", handlers.Account.GetAccountUsage)
		adminGroup.GET("/accounts/export", handlers.Account.ExportAccounts)
		adminGroup.POST("/accounts/import", handlers.Account.ImportAccounts)
		adminGroup.POST("/accounts/bulk-update", handlers.Account.BulkUpdateAccounts)
		adminGroup.POST("/accounts/bulk-delete", handlers.Account.BulkDeleteAccounts)
		adminGroup.POST("/accounts/bulk-refresh-quota", handlers.Account.BulkRefreshQuota)
		adminGroup.POST("/accounts", handlers.Account.CreateAccount)
		adminGroup.PUT("/accounts/:id", handlers.Account.UpdateAccount)
		adminGroup.DELETE("/accounts/:id", handlers.Account.DeleteAccount)
		adminGroup.POST("/accounts/:id/test", handlers.Account.TestAccount)
		adminGroup.PATCH("/accounts/:id/toggle", handlers.Account.ToggleScheduling)
		adminGroup.GET("/accounts/:id/models", handlers.Account.GetAccountModels)
		adminGroup.GET("/accounts/credentials-schema/:platform", handlers.Account.GetCredentialsSchema)
		adminGroup.POST("/accounts/:id/refresh-quota", handlers.Account.RefreshQuota)
		adminGroup.GET("/accounts/:id/stats", handlers.Account.GetAccountStats)

		// 分组管理
		adminGroup.GET("/groups", handlers.Group.ListGroups)
		adminGroup.POST("/groups", handlers.Group.CreateGroup)
		adminGroup.GET("/groups/:id", handlers.Group.GetGroup)
		adminGroup.PUT("/groups/:id", handlers.Group.UpdateGroup)
		adminGroup.DELETE("/groups/:id", handlers.Group.DeleteGroup)

		// 分组专属倍率管理（reverse 视角：某个分组下哪些用户有专属倍率）
		adminGroup.GET("/groups/:id/rate-overrides", handlers.User.ListGroupRateOverrides)
		adminGroup.PUT("/groups/:id/rate-overrides/:userId", handlers.User.SetGroupRateOverride)
		adminGroup.DELETE("/groups/:id/rate-overrides/:userId", handlers.User.DeleteGroupRateOverride)

		// API 密钥管理（管理员）
		adminGroup.PUT("/api-keys/:id", handlers.APIKey.AdminUpdateKey)

		// 订阅管理
		adminGroup.GET("/subscriptions", handlers.Subscription.AdminListSubscriptions)
		adminGroup.POST("/subscriptions/assign", handlers.Subscription.AdminAssign)
		adminGroup.POST("/subscriptions/bulk-assign", handlers.Subscription.AdminBulkAssign)
		adminGroup.PUT("/subscriptions/:id/adjust", handlers.Subscription.AdminAdjust)

		// 代理池管理
		adminGroup.GET("/proxies", handlers.Proxy.ListProxies)
		adminGroup.POST("/proxies", handlers.Proxy.CreateProxy)
		adminGroup.PUT("/proxies/:id", handlers.Proxy.UpdateProxy)
		adminGroup.DELETE("/proxies/:id", handlers.Proxy.DeleteProxy)
		adminGroup.POST("/proxies/:id/test", handlers.Proxy.TestProxy)

		// 使用记录（管理员）
		adminGroup.GET("/usage", handlers.Usage.AdminUsage)
		adminGroup.GET("/usage/stats", handlers.Usage.AdminUsageStats)
		adminGroup.GET("/usage/trend", handlers.Usage.AdminUsageTrend)

		// 插件管理
		adminGroup.GET("/plugins", handlers.Plugin.ListPlugins)
		adminGroup.GET("/plugins/:name/config", handlers.Plugin.GetPluginConfig)
		adminGroup.PUT("/plugins/:name/config", handlers.Plugin.UpdatePluginConfig)
		adminGroup.POST("/plugins/upload", handlers.Plugin.UploadPlugin)
		adminGroup.POST("/plugins/install-github", handlers.Plugin.InstallFromGithub)
		adminGroup.POST("/plugins/:name/uninstall", handlers.Plugin.UninstallPlugin)
		adminGroup.POST("/plugins/:name/reload", handlers.Plugin.ReloadPlugin)
		adminGroup.Any("/plugins/:name/rpc/*action", handlers.Plugin.ProxyRequest)

		// 插件市场
		adminGroup.GET("/marketplace/plugins", handlers.Plugin.ListMarketplace)
		adminGroup.POST("/marketplace/refresh", handlers.Plugin.RefreshMarketplace)

		// 系统设置
		adminGroup.GET("/settings", handlers.Settings.GetSettings)
		adminGroup.PUT("/settings", handlers.Settings.UpdateSettings)
		adminGroup.POST("/settings/test-smtp", handlers.Settings.TestSMTP)
		adminGroup.POST("/settings/upload", handlers.Settings.UploadFile)

		// 管理员 API Key
		adminGroup.GET("/settings/admin-api-key", handlers.Settings.GetAdminAPIKey)
		adminGroup.POST("/settings/admin-api-key", handlers.Settings.GenerateAdminAPIKey)
		adminGroup.DELETE("/settings/admin-api-key", handlers.Settings.DeleteAdminAPIKey)

		// 仪表盘（管理员）
		adminGroup.GET("/dashboard/stats", handlers.Dashboard.Stats)
		adminGroup.GET("/dashboard/trend", handlers.Dashboard.Trend)
	}

	// === Extension 插件 API 路由（JWT 认证 + 管理员权限，支持管理员 API Key） ===
	extGroup := r.Group("/api/v1/ext")
	extGroup.Use(middleware.JWTAuth(s.jwtMgr, s.db), middleware.AdminOnly())
	{
		extGroup.Any("/:pluginName/*path", s.extensionProxy.Handle)
	}

	// === Extension 插件用户级 API 路由（仅 JWT，普通用户可访问） ===
	// 用于支付插件等面向用户的扩展，让普通用户能调用插件接口（创建充值订单、查询自己订单等）。
	// 插件需自行根据 X-Airgate-User-ID 头识别用户，并校验数据归属。
	extUserGroup := r.Group("/api/v1/ext-user")
	extUserGroup.Use(middleware.JWTAuth(s.jwtMgr, s.db))
	{
		extUserGroup.Any("/:pluginName/*path", s.extensionProxy.Handle)
	}

	// === 支付回调路由（无需认证，由插件自行验签） ===
	// 第三方支付平台异步通知（epay/支付宝/微信等）通过此路径转发到对应插件。
	r.Any("/api/v1/payment-callback/:pluginName/*path", s.extensionProxy.Handle)

	// === 公开状态页路由 ===
	// 设计：
	//   - GET /status            → 返回前端 SPA 的 index.html，由 SPA 内的 StatusPage 组件渲染
	//                              （登录前后均可访问，体验与其他页面一致）
	//   - GET /status/*path      → 转发到 airgate-health 插件（API + assets）
	//                              注意：通配符 *path 必须捕获完整的子路径，例如
	//                              /status/api/summary → 插件看到 /api/summary
	//                              所以这里仍然走整段 catchall，不能拆成 /status/api/*path
	//                              （否则 *path 只捕获 /summary，插件路由无法匹配）
	statusProxy := s.extensionProxy.HandleNamed("airgate-health", "public")

	// 加载嵌入的前端 SPA：所有静态资源通过 //go:embed 打进二进制
	distFS, err := webfs.FS()
	if err != nil {
		slog.Error("加载嵌入前端失败", "error", err)
		os.Exit(1)
	}
	indexHTML, _ := webfs.IndexHTML()
	assetsFS, err := fs.Sub(distFS, "assets")
	if err != nil {
		slog.Error("嵌入前端缺少 assets 子目录", "error", err)
		os.Exit(1)
	}

	r.GET("/status", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
	})
	r.GET("/status/*path", statusProxy)

	// === OpenClaw 一键接入（公共路由，无需认证） ===
	// 设计：install.sh 通过 `curl | bash` 分发，因此必须公开；models/info
	// 也无需鉴权，内容均为管理员已标记为 "可公开" 的元信息。
	// 注意：这些路由必须在 NoRoute 之前注册，否则带 Bearer 的请求会被 NoRoute
	// 的 API Key 转发逻辑吃掉。
	openclawGroup := r.Group("/openclaw")
	{
		openclawGroup.GET("/install.sh", handlers.OpenClaw.HandleInstallScript)
		openclawGroup.GET("/models", handlers.OpenClaw.HandleModels)
		openclawGroup.GET("/info", handlers.OpenClaw.HandleInfo)
	}

	// 上传文件静态服务（这部分仍然在磁盘上，因为是用户上传的运行时数据）
	r.Static("/uploads", "data/uploads")

	// 插件前端静态资源（/plugins/{pluginName}/assets/index.js）
	pluginDir := s.cfg.Plugins.Dir
	if pluginDir == "" {
		pluginDir = "data/plugins"
	}
	r.Static("/plugins", pluginDir)

	// 静态文件服务（前端 SPA）
	r.StaticFS("/assets", http.FS(assetsFS))

	// NoRoute: 携带 API Key 的请求转发到插件系统，其余返回前端 index.html
	apiKeyAuth := middleware.APIKeyAuth(s.db)
	r.NoRoute(func(c *gin.Context) {
		// 检查是否携带 Bearer token（API Key 调用）
		auth := c.GetHeader("Authorization")
		if len(auth) > 7 && auth[:7] == "Bearer " {
			apiKeyAuth(c)
			if c.IsAborted() {
				return
			}
			c.Params = append(c.Params, gin.Param{Key: "path", Value: c.Request.URL.Path})
			s.dynamicRouter.Handle(c)
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
	})
}
