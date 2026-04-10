package handler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	appopenclaw "github.com/DouDOU-start/airgate-core/internal/app/openclaw"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// loadConfig 读取 openclaw 配置并在 BaseURL 为空时根据当前请求 Host 推导一个兜底值。
//
// 优先级：
//  1. setting openclaw.base_url
//  2. setting site.site_base_url （由 service.Load 回退）
//  3. c.Request Scheme + Host
func (h *OpenClawHandler) loadConfig(c *gin.Context) (appopenclaw.Config, error) {
	cfg, err := h.service.Load(c.Request.Context())
	if err != nil {
		return cfg, err
	}
	if cfg.BaseURL == "" {
		scheme := "http"
		if c.Request.TLS != nil || strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https") {
			scheme = "https"
		}
		host := forwardedHost(c)
		if host == "" {
			host = "localhost"
		}
		cfg.BaseURL = fmt.Sprintf("%s://%s", scheme, host)
	}
	return cfg, nil
}

func forwardedHost(c *gin.Context) string {
	if raw := strings.TrimSpace(c.GetHeader("X-Forwarded-Host")); raw != "" {
		if idx := strings.IndexByte(raw, ','); idx >= 0 {
			raw = raw[:idx]
		}
		return strings.TrimSpace(raw)
	}
	return strings.TrimSpace(c.Request.Host)
}

// ensureEnabled 如果管理员关闭了 openclaw.enabled，则返回 404。
func (h *OpenClawHandler) ensureEnabled(c *gin.Context, cfg appopenclaw.Config) bool {
	if !cfg.Enabled {
		response.NotFound(c, "openclaw integration disabled")
		return false
	}
	return true
}

// HandleInstallScript 返回动态渲染好的 bash 安装脚本。
//
// 用法（用户终端）：
//
//	curl -fsSL https://<airgate-host>/openclaw/install.sh -o openclaw-install.sh && bash openclaw-install.sh
func (h *OpenClawHandler) HandleInstallScript(c *gin.Context) {
	cfg, err := h.loadConfig(c)
	if err != nil {
		slog.Error("openclaw: 加载配置失败", "error", err)
		c.String(http.StatusInternalServerError, "failed to load openclaw config")
		return
	}
	if !h.ensureEnabled(c, cfg) {
		return
	}

	script, err := h.service.RenderInstallScript(cfg)
	if err != nil {
		slog.Error("openclaw: 渲染安装脚本失败", "error", err)
		c.String(http.StatusInternalServerError, "failed to render install script")
		return
	}

	// 用 text/x-shellscript 让浏览器直接下载；curl | bash 时 content-type 无影响。
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "text/x-shellscript; charset=utf-8", []byte(script))
}

// HandleModels 返回管理员预设的模型清单 JSON。
//
// 公共无鉴权：清单本身是 "可以用哪些模型" 的元信息，不含 apikey 等敏感内容。
func (h *OpenClawHandler) HandleModels(c *gin.Context) {
	cfg, err := h.loadConfig(c)
	if err != nil {
		slog.Error("openclaw: 加载配置失败", "error", err)
		response.InternalError(c, "failed to load openclaw config")
		return
	}
	if !h.ensureEnabled(c, cfg) {
		return
	}

	// 先校验 JSON 可解析；管理员可能填了非法字符串，这里直接返回 500 +
	// 错误提示，而不是把坏 JSON 吐给脚本，避免让用户终端去排查。
	var parsed interface{}
	if err := json.Unmarshal([]byte(cfg.ModelsPresetJSON), &parsed); err != nil {
		slog.Error("openclaw: models_preset JSON 无效", "error", err)
		response.InternalError(c, "models_preset is not valid JSON; please fix it in admin settings")
		return
	}

	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "application/json; charset=utf-8", []byte(cfg.ModelsPresetJSON))
}

// HandleInfo 聚合 base_url / provider / memory_search 等元信息，供前端管理面板展示
// "一键命令" 卡片时使用。
func (h *OpenClawHandler) HandleInfo(c *gin.Context) {
	cfg, err := h.loadConfig(c)
	if err != nil {
		slog.Error("openclaw: 加载配置失败", "error", err)
		response.InternalError(c, "failed to load openclaw config")
		return
	}
	if !h.ensureEnabled(c, cfg) {
		return
	}

	response.Success(c, gin.H{
		"enabled":         cfg.Enabled,
		"provider_name":   cfg.ProviderName,
		"base_url":        cfg.BaseURL,
		"site_name":       cfg.SiteName,
		"install_command": fmt.Sprintf("curl -fsSL %s/openclaw/install.sh -o openclaw-install.sh && bash openclaw-install.sh", cfg.BaseURL),
		"memory_search": gin.H{
			"enabled": cfg.MemorySearchEnabled,
			"model":   cfg.MemorySearchModel,
		},
	})
}
