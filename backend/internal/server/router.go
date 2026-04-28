package server

import (
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/ent/setting"
	"github.com/DouDOU-start/airgate-core/internal/plugin"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
	"github.com/DouDOU-start/airgate-core/internal/setup"
	webfs "github.com/DouDOU-start/airgate-core/internal/web"
)

// registerRoutes 注册所有 API 路由
func (s *Server) registerRoutes() {
	r := s.engine
	handlers := s.handlers

	// 全局中间件：Recovery → RequestLogger → I18n → 业务
	r.Use(middleware.Recovery())
	r.Use(middleware.RequestLogger())
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

	// === 认证路由（无需 JWT） ===
	//
	// 注：原本这里挂了 middleware.RateLimit，但那个中间件依赖 c.Get(CtxKeyUserID)，
	// 而登录/注册这些前置鉴权阶段根本还没设 user_id，中间件直接 c.Next() 放行，
	// 实际是空转。随同硬编码 60 req/min 的用户限流一起移除了。
	authGroup := v1.Group("/auth")
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
		adminGroup.DELETE("/accounts/:id/family-cooldowns", handlers.Account.ClearFamilyCooldowns)
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

		// core 版本信息（仅管理员可见，避免对外暴露版本指纹）
		adminGroup.GET("/version", handlers.Version.GetVersion)

		// 系统更新（仅管理员；run 接口在 systemd 模式下生效，Docker 模式只返回升级指令）
		adminGroup.GET("/upgrade/info", handlers.Upgrade.GetInfo)
		adminGroup.GET("/upgrade/status", handlers.Upgrade.GetStatus)
		adminGroup.POST("/upgrade/run", handlers.Upgrade.Run)
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
	// 设计：core 完全不维护一份状态页前端，所有 /status* 请求一律反代到
	// airgate-health 插件，由插件内部 standalone 打包的 status.html + status-XXX.js
	// 渲染。这样状态页的 UI / 数据 / 粒度都由健康监控插件单点维护，避免 core
	// 与插件出现两份重复实现（之前 core 自己有个 React StatusPage 组件并维护
	// 90 天日级方格图，与 health 插件的 standalone 页严重重复，移除）。
	//
	// 反代规则：
	//   - GET /status            → 插件看到 /        → handlePublicIndex 返回 status.html
	//   - GET /status/*path      → 插件看到 /<path> → API + 静态资源
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

	// /status 与 /status/*path 都走 statusProxy 反代到 airgate-health 插件
	r.GET("/status", statusProxy)
	r.GET("/status/*path", statusProxy)

	// === cc-switch 通用模板兼容端点（使用 sk-xxx API Key 自鉴权） ===
	// cc-switch（https://github.com/farion1231/cc-switch）的"通用模板"会
	// 打 GET {baseUrl}/v1/usage，extractor 依次读 response.remaining /
	// response.quota.remaining / response.balance 作为剩余额度，并读
	// response.is_active 作为 key 状态。这里注册 /v1/usage 返回
	// { is_active, balance } 即可命中。
	// 必须注册在 NoRoute 之前，否则会被插件动态路由吃掉。
	// 实现见 cc_compat.go。
	r.GET("/v1/usage", s.handleCCCompatUserBalance)

	// === OpenClaw 一键接入（公共路由，无需认证） ===
	// 设计：install.sh 通过 `curl | bash` 分发，因此必须公开；models/info
	// 也无需鉴权，内容均为管理员已标记为 "可公开" 的元信息。
	// 注意：这些路由必须在 NoRoute 之前注册，否则带 Bearer 的请求会被 NoRoute
	// 的 API Key 转发逻辑吃掉。
	openclawGroup := r.Group("/openclaw")
	{
		openclawGroup.GET("/install.sh", handlers.OpenClaw.HandleInstallScript)
		openclawGroup.GET("/install.ps1", handlers.OpenClaw.HandleInstallScriptPowerShell)
		openclawGroup.GET("/models", handlers.OpenClaw.HandleModels)
		openclawGroup.GET("/models.txt", handlers.OpenClaw.HandleModelsText)
		openclawGroup.POST("/render-config", handlers.OpenClaw.HandleRenderConfig)
		openclawGroup.GET("/info", handlers.OpenClaw.HandleInfo)
	}

	// 上传文件静态服务（这部分仍然在磁盘上，因为是用户上传的运行时数据）
	r.Static("/uploads", "data/uploads")
	r.GET("/assets-runtime/*path", s.handleRuntimeAsset)

	// 插件前端静态资源（/plugins/{pluginName}/assets/*）
	//
	// 与 r.Static 不同：这是一个 dev-aware handler，对每个请求按以下顺序查找：
	//   1. 如果该插件是 dev 模式 → 从 <plugin_src>/web/dist/ 读 vite watch 实时产物
	//   2. fallback 到 data/plugins/<id>/assets/ —— 生产模式或 vite 还没构建好
	//
	// 这样所有插件的 vite watch 都可以统一输出到自己的 web/dist，不需要再让
	// vite watch --outDir 写到 core 的 plugin assets dir。
	pluginDir := s.cfg.Plugins.Dir
	if pluginDir == "" {
		pluginDir = "data/plugins"
	}
	r.GET("/plugins/:name/assets/*path", servePluginAsset(s.pluginMgr, pluginDir))

	// 静态文件服务（前端 SPA）
	r.StaticFS("/assets", http.FS(assetsFS))

	// NoRoute: 携带 API Key 的请求转发到插件系统，其余返回前端 index.html
	// 支持 Authorization: Bearer 和 x-api-key 两种认证方式（兼容 Anthropic 标准格式）
	apiKeyAuth := middleware.APIKeyAuth(s.db)
	r.NoRoute(func(c *gin.Context) {
		if middleware.HasAPIKey(c) {
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

// servePluginAsset 处理 /plugins/<name>/assets/* 请求。
//
// 双模式：
//   - dev 模式：从 <plugin_src>/web/dist/<rel> 读 vite watch 实时构建产物。
//     这样 openai/epay/health 都可以让 vite watch 输出到自己的 web/dist，
//     core 透明地从那里读，不再需要让 vite watch --outDir 写到 core 内部目录。
//   - production 模式：fallback 到 data/plugins/<name>/assets/<rel>，
//     由 core 启动时通过 GetWebAssets() 把插件 binary embed 的 webdist 提取出来。
//
// 路径穿越防御：clean 后检查不允许 ".."。
func (s *Server) handleRuntimeAsset(c *gin.Context) {
	localDir := plugin.DefaultAssetStorageDir
	if item, err := s.db.Setting.Query().Where(setting.GroupEQ("storage"), setting.KeyEQ("local_storage_dir")).Only(c.Request.Context()); err == nil && strings.TrimSpace(item.Value) != "" {
		localDir = strings.TrimSpace(item.Value)
	}
	rel := strings.TrimPrefix(path.Clean("/"+c.Param("path")), "/")
	if rel == "" || rel == "." || strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") {
		c.Status(http.StatusBadRequest)
		return
	}
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.File(filepath.Join(localDir, filepath.FromSlash(rel)))
}

func servePluginAsset(mgr *plugin.Manager, baseDir string) gin.HandlerFunc {
	return func(c *gin.Context) {
		name := c.Param("name")
		rel := strings.TrimPrefix(c.Param("path"), "/")

		// 路径穿越防御
		clean := filepath.Clean("/" + rel)
		if strings.Contains(clean, "..") {
			c.Status(http.StatusBadRequest)
			return
		}
		rel = strings.TrimPrefix(clean, "/")

		// 优先尝试 dev 路径
		if devDir, ok := mgr.DevWebDistPath(name); ok {
			full := filepath.Join(devDir, rel)
			if data, err := os.ReadFile(full); err == nil {
				c.Data(http.StatusOK, contentTypeFromExt(rel), data)
				return
			}
		}

		// fallback 到 production 路径
		full := filepath.Join(baseDir, name, "assets", rel)
		data, err := os.ReadFile(full)
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		c.Data(http.StatusOK, contentTypeFromExt(rel), data)
	}
}

// contentTypeFromExt 按扩展名返回 Content-Type。覆盖插件资源里常见的几种文件，
// 未知扩展名退回 application/octet-stream。
func contentTypeFromExt(name string) string {
	switch {
	case strings.HasSuffix(name, ".html"):
		return "text/html; charset=utf-8"
	case strings.HasSuffix(name, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(name, ".js"), strings.HasSuffix(name, ".mjs"):
		return "application/javascript; charset=utf-8"
	case strings.HasSuffix(name, ".json"):
		return "application/json"
	case strings.HasSuffix(name, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(name, ".png"):
		return "image/png"
	case strings.HasSuffix(name, ".woff2"):
		return "font/woff2"
	default:
		return "application/octet-stream"
	}
}
