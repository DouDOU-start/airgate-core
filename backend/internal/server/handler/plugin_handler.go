// Package handler 提供 HTTP API 处理器
package handler

import (
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/DouDOU-start/airgate-core/internal/plugin"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
	sdk "github.com/DouDOU-start/airgate-sdk"
	"github.com/gin-gonic/gin"
)

// PluginHandler 插件管理 API
type PluginHandler struct {
	manager     *plugin.Manager
	marketplace *plugin.Marketplace
}

// NewPluginHandler 创建插件管理 Handler
func NewPluginHandler(manager *plugin.Manager, marketplace *plugin.Marketplace) *PluginHandler {
	return &PluginHandler{
		manager:     manager,
		marketplace: marketplace,
	}
}

// ListPlugins 获取已加载的插件列表
// GET /api/v1/admin/plugins
func (h *PluginHandler) ListPlugins(c *gin.Context) {
	allMeta := h.manager.GetAllPluginMeta()

	list := make([]dto.PluginResp, 0, len(allMeta))
	for _, m := range allMeta {
		resp := dto.PluginResp{
			Name:        m.Name,
			DisplayName: m.DisplayName,
			Version:     m.Version,
			Author:      m.Author,
			Type:        m.Type,
			Platform:    m.Platform,
		}
		for _, at := range m.AccountTypes {
			resp.AccountTypes = append(resp.AccountTypes, dto.AccountTypeResp{
				Key: at.Key, Label: at.Label, Description: at.Description,
			})
		}
		for _, fp := range m.FrontendPages {
			resp.FrontendPages = append(resp.FrontendPages, dto.FrontendPageResp{
				Path: fp.Path, Title: fp.Title, Icon: fp.Icon, Description: fp.Description,
			})
		}
		resp.HasWebAssets = m.HasWebAssets
		resp.IsDev = m.IsDev
		list = append(list, resp)
	}

	response.Success(c, response.PagedData(list, int64(len(list)), 1, len(list)))
}

// UploadPlugin 上传安装插件
// POST /api/v1/admin/plugins/upload
func (h *PluginHandler) UploadPlugin(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		response.BadRequest(c, "请上传插件文件")
		return
	}

	f, err := file.Open()
	if err != nil {
		response.InternalError(c, "读取上传文件失败")
		return
	}
	defer f.Close()

	binary, err := io.ReadAll(f)
	if err != nil {
		response.InternalError(c, "读取文件内容失败")
		return
	}

	name := c.PostForm("name")
	if name == "" {
		name = strings.TrimSuffix(file.Filename, ".exe")
	}

	if err := h.manager.InstallFromBinary(c.Request.Context(), name, binary); err != nil {
		response.InternalError(c, "安装插件失败: "+err.Error())
		return
	}

	response.Success(c, nil)
}

// InstallFromGithub 从 GitHub Release 安装插件
// POST /api/v1/admin/plugins/install-github
func (h *PluginHandler) InstallFromGithub(c *gin.Context) {
	var req dto.InstallGithubReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数无效")
		return
	}

	if err := h.manager.InstallFromGithub(c.Request.Context(), req.Repo); err != nil {
		response.InternalError(c, "从 GitHub 安装失败: "+err.Error())
		return
	}

	response.Success(c, nil)
}

// UninstallPlugin 卸载插件
// POST /api/v1/admin/plugins/:name/uninstall
func (h *PluginHandler) UninstallPlugin(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		response.BadRequest(c, "插件名称无效")
		return
	}

	if err := h.manager.Uninstall(c.Request.Context(), name); err != nil {
		response.InternalError(c, "卸载插件失败: "+err.Error())
		return
	}

	response.Success(c, nil)
}

// ReloadPlugin 热加载开发模式插件
// POST /api/v1/admin/plugins/:name/reload
func (h *PluginHandler) ReloadPlugin(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		response.BadRequest(c, "插件名称无效")
		return
	}

	if !h.manager.IsDev(name) {
		response.BadRequest(c, "仅开发模式插件支持热加载")
		return
	}

	if err := h.manager.ReloadDev(c.Request.Context(), name); err != nil {
		response.InternalError(c, "热加载插件失败: "+err.Error())
		return
	}

	response.Success(c, nil)
}

// StartOAuth 发起插件 OAuth 授权
// POST /api/v1/admin/plugins/:name/oauth/start
func (h *PluginHandler) StartOAuth(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		response.BadRequest(c, "插件名称无效")
		return
	}

	inst := h.manager.GetInstance(name)
	if inst == nil || inst.Gateway == nil {
		response.NotFound(c, "插件未运行或不存在")
		return
	}

	result, err := inst.Gateway.StartOAuth(c.Request.Context(), &sdk.OAuthStartRequest{})
	if err != nil {
		response.InternalError(c, "发起 OAuth 授权失败: "+err.Error())
		return
	}

	response.Success(c, dto.PluginOAuthStartResp{
		AuthorizeURL: result.AuthorizeURL,
		State:        result.State,
	})
}

// ExchangeOAuth 使用回调 URL 完成插件 OAuth token 交换
// POST /api/v1/admin/plugins/:name/oauth/exchange
func (h *PluginHandler) ExchangeOAuth(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		response.BadRequest(c, "插件名称无效")
		return
	}

	inst := h.manager.GetInstance(name)
	if inst == nil || inst.Gateway == nil {
		response.NotFound(c, "插件未运行或不存在")
		return
	}

	var req dto.PluginOAuthExchangeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数无效")
		return
	}

	code, state, err := parseOAuthCallbackURL(req.CallbackURL)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	result, err := inst.Gateway.HandleOAuthCallback(c.Request.Context(), &sdk.OAuthCallbackRequest{
		Code:  code,
		State: state,
	})
	if err != nil {
		response.InternalError(c, "OAuth 回调交换失败: "+err.Error())
		return
	}

	response.Success(c, dto.PluginOAuthExchangeResp{
		AccountType: result.AccountType,
		AccountName: result.AccountName,
		Credentials: result.Credentials,
	})
}

// ListMarketplace 列出市场可用插件
// GET /api/v1/admin/marketplace/plugins
func (h *PluginHandler) ListMarketplace(c *gin.Context) {
	plugins, err := h.marketplace.ListAvailable(c.Request.Context())
	if err != nil {
		response.InternalError(c, "查询插件市场失败")
		return
	}

	list := make([]dto.MarketplacePluginResp, 0, len(plugins))
	for _, p := range plugins {
		list = append(list, dto.MarketplacePluginResp{
			Name:        p.Name,
			Version:     p.Version,
			Description: p.Description,
			Author:      p.Author,
			Type:        p.Type,
		})
	}

	response.Success(c, response.PagedData(list, int64(len(list)), 1, len(list)))
}

func parseOAuthCallbackURL(raw string) (code, state string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("请粘贴完整的回调 URL")
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("回调 URL 格式无效")
	}

	code = strings.TrimSpace(parsed.Query().Get("code"))
	state = strings.TrimSpace(parsed.Query().Get("state"))
	if code == "" || state == "" {
		return "", "", fmt.Errorf("回调 URL 中缺少 code 或 state 参数")
	}

	return code, state, nil
}
