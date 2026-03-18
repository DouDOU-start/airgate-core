package server

import (
	"path/filepath"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/server/handler"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
	"github.com/DouDOU-start/airgate-core/internal/setup"
)

// registerRoutes 注册所有 API 路由
func (s *Server) registerRoutes() {
	r := s.engine

	// 全局中间件
	r.Use(middleware.I18n())

	// 安装向导路由（无需认证）
	setup.RegisterRoutes(r)

	// 初始化所有 Handler
	authHandler := handler.NewAuthHandler(s.db, s.jwtMgr)
	userHandler := handler.NewUserHandler(s.db)
	accountHandler := handler.NewAccountHandler(s.db, s.pluginMgr, s.concurrency)
	groupHandler := handler.NewGroupHandler(s.db)
	apikeyHandler := handler.NewAPIKeyHandler(s.db, s.cfg.JWT.Secret)
	subscriptionHandler := handler.NewSubscriptionHandler(s.db)
	usageHandler := handler.NewUsageHandler(s.db)
	proxyHandler := handler.NewProxyHandler(s.db)
	settingsHandler := handler.NewSettingsHandler(s.db)
	dashboardHandler := handler.NewDashboardHandler(s.db, s.pluginMgr)

	// 插件 Handler（使用 server 持有的组件）
	pluginHandler := handler.NewPluginHandler(s.pluginMgr, s.marketplace)

	// API v1 路由组
	v1 := r.Group("/api/v1")

	// === 认证路由（无需 JWT，带限流保护） ===
	authGroup := v1.Group("/auth")
	authGroup.Use(middleware.RateLimit(s.limiter))
	{
		authGroup.POST("/login", authHandler.Login)
		authGroup.POST("/register", authHandler.Register)
	}

	// === 用户路由（需要 JWT 认证） ===
	userGroup := v1.Group("")
	userGroup.Use(middleware.JWTAuth(s.jwtMgr))
	{
		// Token 刷新
		userGroup.POST("/auth/refresh", authHandler.RefreshToken)

		// TOTP 管理
		userGroup.POST("/auth/totp/setup", authHandler.TOTPSetup)
		userGroup.POST("/auth/totp/verify", authHandler.TOTPVerify)
		userGroup.POST("/auth/totp/disable", authHandler.TOTPDisable)

		// 用户资料
		userGroup.GET("/users/me", userHandler.GetMe)
		userGroup.PUT("/users/me", userHandler.UpdateProfile)
		userGroup.POST("/users/me/password", userHandler.ChangePassword)

		// API Key 管理
		userGroup.GET("/api-keys", apikeyHandler.ListKeys)
		userGroup.POST("/api-keys", apikeyHandler.CreateKey)
		userGroup.PUT("/api-keys/:id", apikeyHandler.UpdateKey)
		userGroup.DELETE("/api-keys/:id", apikeyHandler.DeleteKey)
		userGroup.GET("/api-keys/:id/reveal", apikeyHandler.RevealKey)

		// 订阅
		userGroup.GET("/subscriptions", subscriptionHandler.UserSubscriptions)
		userGroup.GET("/subscriptions/active", subscriptionHandler.ActiveSubscriptions)
		userGroup.GET("/subscriptions/progress", subscriptionHandler.SubscriptionProgress)

		// 使用记录
		userGroup.GET("/usage", usageHandler.UserUsage)

		// 仪表盘
		userGroup.GET("/dashboard/stats", dashboardHandler.Stats)
		userGroup.GET("/dashboard/trend", dashboardHandler.Trend)
	}

	// === 管理员路由（需要 JWT + AdminOnly） ===
	adminGroup := v1.Group("/admin")
	adminGroup.Use(middleware.JWTAuth(s.jwtMgr), middleware.AdminOnly())
	{
		// 用户管理
		adminGroup.GET("/users", userHandler.ListUsers)
		adminGroup.POST("/users", userHandler.CreateUser)
		adminGroup.PUT("/users/:id", userHandler.UpdateUser)
		adminGroup.DELETE("/users/:id", userHandler.DeleteUser)
		adminGroup.PATCH("/users/:id/toggle", userHandler.ToggleUserStatus)
		adminGroup.POST("/users/:id/balance", userHandler.AdjustBalance)
		adminGroup.GET("/users/:id/balance-history", userHandler.GetUserBalanceHistory)
		adminGroup.GET("/users/:id/api-keys", userHandler.AdminListUserKeys)

		// 账号管理
		adminGroup.GET("/accounts", accountHandler.ListAccounts)
		adminGroup.GET("/accounts/usage", accountHandler.GetAccountUsage)
		adminGroup.POST("/accounts", accountHandler.CreateAccount)
		adminGroup.PUT("/accounts/:id", accountHandler.UpdateAccount)
		adminGroup.DELETE("/accounts/:id", accountHandler.DeleteAccount)
		adminGroup.POST("/accounts/:id/test", accountHandler.TestAccount)
		adminGroup.PATCH("/accounts/:id/toggle", accountHandler.ToggleScheduling)
		adminGroup.GET("/accounts/:id/models", accountHandler.GetAccountModels)
		adminGroup.GET("/accounts/credentials-schema/:platform", accountHandler.GetCredentialsSchema)
		adminGroup.POST("/accounts/:id/refresh-quota", accountHandler.RefreshQuota)
		adminGroup.GET("/accounts/:id/stats", accountHandler.GetAccountStats)

		// 分组管理
		adminGroup.GET("/groups", groupHandler.ListGroups)
		adminGroup.POST("/groups", groupHandler.CreateGroup)
		adminGroup.GET("/groups/:id", groupHandler.GetGroup)
		adminGroup.PUT("/groups/:id", groupHandler.UpdateGroup)

		// API 密钥管理（管理员）
		adminGroup.PUT("/api-keys/:id", apikeyHandler.AdminUpdateKey)

		// 订阅管理
		adminGroup.GET("/subscriptions", subscriptionHandler.AdminListSubscriptions)
		adminGroup.POST("/subscriptions/assign", subscriptionHandler.AdminAssign)
		adminGroup.POST("/subscriptions/bulk-assign", subscriptionHandler.AdminBulkAssign)
		adminGroup.PUT("/subscriptions/:id/adjust", subscriptionHandler.AdminAdjust)

		// 代理池管理
		adminGroup.GET("/proxies", proxyHandler.ListProxies)
		adminGroup.POST("/proxies", proxyHandler.CreateProxy)
		adminGroup.PUT("/proxies/:id", proxyHandler.UpdateProxy)
		adminGroup.DELETE("/proxies/:id", proxyHandler.DeleteProxy)
		adminGroup.POST("/proxies/:id/test", proxyHandler.TestProxy)

		// 使用记录（管理员）
		adminGroup.GET("/usage", usageHandler.AdminUsage)
		adminGroup.GET("/usage/stats", usageHandler.AdminUsageStats)

		// 插件管理
		adminGroup.GET("/plugins", pluginHandler.ListPlugins)
		adminGroup.POST("/plugins/upload", pluginHandler.UploadPlugin)
		adminGroup.POST("/plugins/install-github", pluginHandler.InstallFromGithub)
		adminGroup.POST("/plugins/:name/uninstall", pluginHandler.UninstallPlugin)
		adminGroup.POST("/plugins/:name/reload", pluginHandler.ReloadPlugin)
		adminGroup.Any("/plugins/:name/rpc/*action", pluginHandler.ProxyRequest)

		// 插件市场
		adminGroup.GET("/marketplace/plugins", pluginHandler.ListMarketplace)

		// 系统设置
		adminGroup.GET("/settings", settingsHandler.GetSettings)
		adminGroup.PUT("/settings", settingsHandler.UpdateSettings)

		// 仪表盘（管理员）
		adminGroup.GET("/dashboard/stats", dashboardHandler.Stats)
		adminGroup.GET("/dashboard/trend", dashboardHandler.Trend)
	}

	// === Extension 插件 API 路由（JWT 认证 + 管理员权限） ===
	extGroup := r.Group("/api/v1/ext")
	extGroup.Use(middleware.JWTAuth(s.jwtMgr), middleware.AdminOnly())
	{
		extGroup.Any("/:pluginName/*path", s.extensionProxy.Handle)
	}

	// 插件前端静态资源（/plugins/{pluginName}/assets/index.js）
	pluginDir := s.cfg.Plugins.Dir
	if pluginDir == "" {
		pluginDir = "data/plugins"
	}
	r.Static("/plugins", pluginDir)

	// 静态文件服务（前端）
	webDir := s.cfg.Server.WebDir
	if webDir == "" {
		webDir = "web/dist"
	}
	r.Static("/assets", filepath.Join(webDir, "assets"))

	// NoRoute: 携带 API Key 的请求转发到插件系统，其余返回前端 index.html
	indexHTML := filepath.Join(webDir, "index.html")
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
		c.File(indexHTML)
	})
}
