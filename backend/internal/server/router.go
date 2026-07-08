package server

import (
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/asset"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
	"github.com/DouDOU-start/airgate-core/internal/setup"
	webfs "github.com/DouDOU-start/airgate-core/internal/web"
)

// registerRoutes 注册所有 API 路由
func (s *Server) registerRoutes() {
	r := s.engine
	handlers := s.handlers

	// 全局中间件：CORS → Recovery → RequestLogger → I18n → 业务
	r.Use(middleware.CORS(middleware.CORSConfig{
		// 默认不设置 AllowOrigins，仅同源可访问。
		// 如需跨域请配置具体来源，例如：AllowOrigins: []string{"https://example.com"}
	}))
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

		// 公告（用户端：查看 + 标已读）
		accountGroup.GET("/announcements", handlers.Announcement.ListMyAnnouncements)
		accountGroup.POST("/announcements/read-all", handlers.Announcement.MarkAllAnnouncementsRead)
		accountGroup.POST("/announcements/:id/read", handlers.Announcement.MarkAnnouncementRead)

		// 使用记录
		userGroup.GET("/usage", handlers.Usage.UserUsage)
		userGroup.GET("/usage/stats", handlers.Usage.UserUsageStats)
		userGroup.GET("/usage/trend", handlers.Usage.UserUsageTrend)
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

		// 分组专属倍率管理（reverse 视角：某个分组下哪些用户有专属倍率）
		adminGroup.GET("/groups/:id/rate-overrides", handlers.User.ListGroupRateOverrides)
		adminGroup.PUT("/groups/:id/rate-overrides/:userId", handlers.User.SetGroupRateOverride)
		adminGroup.DELETE("/groups/:id/rate-overrides/:userId", handlers.User.DeleteGroupRateOverride)

		// API 密钥管理（管理员）
		adminGroup.GET("/api-keys", handlers.APIKey.AdminListKeys)
		adminGroup.PUT("/api-keys/:id", handlers.APIKey.AdminUpdateKey)

		// 渠道管理
		adminGroup.GET("/channels", handlers.Channel.ListChannels)
		adminGroup.POST("/channels", handlers.Channel.CreateChannel)
		adminGroup.PUT("/channels/:id", handlers.Channel.UpdateChannel)
		adminGroup.DELETE("/channels/:id", handlers.Channel.DeleteChannel)
		adminGroup.POST("/channels/:id/test", handlers.Channel.TestChannel)
		adminGroup.POST("/channels/:id/fetch-models", handlers.Channel.FetchChannelModels)
		// 预览拉取：渠道未保存时按表单连接参数试拉模型（静态段，先于 :id 匹配）
		adminGroup.POST("/channels/fetch-models", handlers.Channel.FetchChannelModelsPreview)
		adminGroup.POST("/channels/bulk-update", handlers.Channel.BulkUpdateChannels)

		// 模型价格
		adminGroup.GET("/model-prices", handlers.ModelPrice.ListModelPrices)
		adminGroup.POST("/model-prices", handlers.ModelPrice.CreateModelPrice)
		adminGroup.PUT("/model-prices/:id", handlers.ModelPrice.UpdateModelPrice)
		adminGroup.DELETE("/model-prices/:id", handlers.ModelPrice.DeleteModelPrice)
		adminGroup.POST("/model-prices/import", handlers.ModelPrice.ImportModelPrices)

		// 使用记录（管理员）
		adminGroup.GET("/usage", handlers.Usage.AdminUsage)
		adminGroup.GET("/usage/stats", handlers.Usage.AdminUsageStats)
		adminGroup.GET("/usage/trend", handlers.Usage.AdminUsageTrend)

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

	// === 对外网关路由（sk- API Key 鉴权，OpenAI 兼容） ===
	// 显式静态注册（先于 NoRoute），错误体统一走 relay 的 OpenAI 形态 errfmt。
	relayGroup := r.Group("/v1", middleware.APIKeyAuth(s.db))
	{
		relayGroup.POST("/chat/completions", s.relay.HandleChatCompletions)
		relayGroup.POST("/responses", s.relay.HandleResponses)
		relayGroup.GET("/models", s.relay.HandleModels)
	}

	// === cc-switch 通用模板兼容端点（使用 sk-xxx API Key 自鉴权） ===
	// AirGate 安装脚本和 cc-switch 通用脚本可使用 /v1/usage 做 Key 校验和
	// 余额查询。该路径由 Core 直接处理，返回真实可用余额。
	// 实现见 cc_compat.go。
	r.GET("/v1/usage", s.handleCCCompatUserBalance)

	// 上传文件静态服务（这部分仍然在磁盘上，因为是用户上传的运行时数据）
	//
	// ⚠️ 安全说明：此路径公开可访问，无需认证。上传的文件（如头像、聊天图片）可能
	// 被嵌入外部链接中分享，因此保持公开。文件名使用 UUID 生成，不可枚举。
	// 如未来需要访问控制，应替换为带鉴权的路由组。
	r.Static("/uploads", "data/uploads")
	r.GET("/assets-runtime/*path", s.handleRuntimeAsset)

	// 静态文件服务（前端 SPA）
	r.StaticFS("/assets", http.FS(assetsFS))

	// NoRoute: 纯 SPA fallback，未匹配的路径一律返回前端 index.html。
	// P1 起对外网关路由（/v1/chat/completions 等）走显式注册，不再经 NoRoute 分发。
	r.NoRoute(func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
	})
}

// handleRuntimeAsset 处理 /assets-runtime/* 运行时资产请求。
//
// 路径穿越防御：clean 后检查不允许 ".."。
func (s *Server) handleRuntimeAsset(c *gin.Context) {
	rel := strings.TrimPrefix(path.Clean("/"+c.Param("path")), "/")
	if rel == "" || rel == "." || strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") {
		c.Status(http.StatusBadRequest)
		return
	}
	storage, err := asset.NewAssetStorage(c.Request.Context(), s.db)
	if err != nil {
		slog.Warn("runtime_asset_storage_init_failed", "error", err)
		c.Status(http.StatusInternalServerError)
		return
	}
	localPath, err := storage.LocalPath(rel)
	if err != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	c.Header("Cache-Control", "public, max-age=31536000, immutable")

	if width := resolveThumbWidth(c.Query("w")); width > 0 && thumbnailableExt(rel) {
		cachePath := thumbCachePath(localPath, width)
		if data, err := os.ReadFile(cachePath); err == nil {
			c.Data(http.StatusOK, "image/jpeg", data)
			return
		}
		data, contentType, err := storage.GetBytes(c.Request.Context(), rel)
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		thumb, thumbErr := generateThumbnailFromBytes(data, cachePath, width)
		if thumbErr == nil {
			c.Data(http.StatusOK, "image/jpeg", thumb)
			return
		}
		if contentType == "" || contentType == "application/octet-stream" {
			contentType = contentTypeFromExt(rel)
		}
		c.Data(http.StatusOK, contentType, data)
		return
	}

	data, contentType, err := storage.GetBytes(c.Request.Context(), rel)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	if contentType == "" || contentType == "application/octet-stream" {
		contentType = contentTypeFromExt(rel)
	}
	c.Data(http.StatusOK, contentType, data)
}

// contentTypeFromExt 按扩展名返回 Content-Type。覆盖运行时资产里常见的几种文件，
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
	case strings.HasSuffix(name, ".jpg"), strings.HasSuffix(name, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(name, ".webp"):
		return "image/webp"
	case strings.HasSuffix(name, ".gif"):
		return "image/gif"
	case strings.HasSuffix(name, ".mp4"):
		return "video/mp4"
	case strings.HasSuffix(name, ".mp3"):
		return "audio/mpeg"
	case strings.HasSuffix(name, ".woff2"):
		return "font/woff2"
	default:
		return "application/octet-stream"
	}
}
