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

// HandleInstallScriptPowerShell 返回 Windows PowerShell 版安装脚本。
//
// 用法（用户 PowerShell 终端）：
//
//	iwr -useb https://<airgate-host>/openclaw/install.ps1 | iex
//
// 与 bash 版相对，PowerShell 版的 Content-Type 用 text/plain —— PowerShell 对
// MIME type 并不敏感，但 text/plain 能让浏览器直接预览而不是强制下载。
func (h *OpenClawHandler) HandleInstallScriptPowerShell(c *gin.Context) {
	cfg, err := h.loadConfig(c)
	if err != nil {
		slog.Error("openclaw: 加载配置失败", "error", err)
		c.String(http.StatusInternalServerError, "failed to load openclaw config")
		return
	}
	if !h.ensureEnabled(c, cfg) {
		return
	}

	script, err := h.service.RenderInstallScriptPowerShell(cfg)
	if err != nil {
		slog.Error("openclaw: 渲染 PowerShell 安装脚本失败", "error", err)
		c.String(http.StatusInternalServerError, "failed to render install.ps1")
		return
	}

	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(script))
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

// HandleModelsText 返回 pipe 分隔的模型清单，供 install.sh 里纯 bash 解析。
//
// 与 HandleModels 的区别：
//   - /openclaw/models 返回原始 JSON（给前端管理面板用）
//   - /openclaw/models.txt 返回已预处理的 `idx|id|label|api|caps` 纯文本，bash
//     `while IFS='|' read` 一行搞定，避免在客户端依赖 python3/jq
func (h *OpenClawHandler) HandleModelsText(c *gin.Context) {
	cfg, err := h.loadConfig(c)
	if err != nil {
		slog.Error("openclaw: 加载配置失败", "error", err)
		c.String(http.StatusInternalServerError, "failed to load openclaw config")
		return
	}
	if !h.ensureEnabled(c, cfg) {
		return
	}

	text, err := h.service.BuildModelsText(cfg)
	if err != nil {
		slog.Error("openclaw: 渲染 models.txt 失败", "error", err)
		c.String(http.StatusInternalServerError, "models_preset is not valid JSON; please fix it in admin settings")
		return
	}

	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(text))
}

// HandleRenderConfig 根据客户端提交的 API Key + 选中的模型 ID，服务端渲染出完整的 openclaw.json。
//
// 把这段逻辑从 install.sh 里的 python3 搬到服务端，让安装脚本彻底摆脱 python 依赖。
// 注意：服务端不做 API Key 有效性校验（install.sh 在更早的步骤已经调 /v1/usage 校过了），
// 也不落库保存，只做 JSON 组装并直接回写给客户端。
func (h *OpenClawHandler) HandleRenderConfig(c *gin.Context) {
	cfg, err := h.loadConfig(c)
	if err != nil {
		slog.Error("openclaw: 加载配置失败", "error", err)
		c.String(http.StatusInternalServerError, "failed to load openclaw config")
		return
	}
	if !h.ensureEnabled(c, cfg) {
		return
	}

	var req appopenclaw.RenderConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.String(http.StatusBadRequest, "invalid request body: %s", err)
		return
	}

	out, err := h.service.BuildOpenClawConfig(cfg, req)
	if err != nil {
		// 客户端错误（unknown model id / 空 api_key 等）直接回 400，带原始错误信息；
		// models_preset 坏掉之类的服务端问题由 service 层向上冒泡，这里也统一作为 400
		// 返回，因为脚本对所有渲染失败的处理都是 die，不需要区分 4xx/5xx。
		slog.Warn("openclaw: 渲染 openclaw.json 失败", "error", err)
		c.String(http.StatusBadRequest, "render failed: %s", err)
		return
	}

	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "application/json; charset=utf-8", out)
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

	installCmdBash := fmt.Sprintf("curl -fsSL %s/openclaw/install.sh -o openclaw-install.sh && bash openclaw-install.sh", cfg.BaseURL)
	installCmdPowerShell := fmt.Sprintf("iwr -useb %s/openclaw/install.ps1 | iex", cfg.BaseURL)

	response.Success(c, gin.H{
		"enabled":       cfg.Enabled,
		"provider_name": cfg.ProviderName,
		"base_url":      cfg.BaseURL,
		"site_name":     cfg.SiteName,
		// install_command 保留兼容老前端（指向 bash 版），新前端请用下面两个分系统命令。
		"install_command":            installCmdBash,
		"install_command_bash":       installCmdBash,
		"install_command_powershell": installCmdPowerShell,
		"memory_search": gin.H{
			"enabled": cfg.MemorySearchEnabled,
			"model":   cfg.MemorySearchModel,
		},
	})
}
