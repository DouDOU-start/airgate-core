package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	apppluginadmin "github.com/DouDOU-start/airgate-core/internal/app/pluginadmin"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// ListPlugins 获取已加载的插件列表。
func (h *PluginHandler) ListPlugins(c *gin.Context) {
	list := h.service.List()
	resp := make([]dto.PluginResp, 0, len(list))
	for _, item := range list {
		resp = append(resp, toPluginResp(item))
	}
	response.Success(c, response.PagedData(resp, int64(len(resp)), 1, len(resp)))
}

// GetPluginConfig 读取插件已持久化的配置（用于编辑配置 UI 回显）。
func (h *PluginHandler) GetPluginConfig(c *gin.Context) {
	name := c.Param("name")
	cfg, err := h.service.GetConfig(c.Request.Context(), name)
	if err != nil {
		response.InternalError(c, "读取插件配置失败: "+err.Error())
		return
	}
	if cfg == nil {
		cfg = map[string]string{}
	}
	response.Success(c, dto.PluginConfigResp{Config: cfg})
}

// UpdatePluginConfig 更新插件配置并触发 reload。
func (h *PluginHandler) UpdatePluginConfig(c *gin.Context) {
	name := c.Param("name")
	var req dto.PluginConfigUpdateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求格式错误: "+err.Error())
		return
	}
	if req.Config == nil {
		req.Config = map[string]string{}
	}
	if err := h.service.UpdateConfig(c.Request.Context(), name, req.Config); err != nil {
		response.InternalError(c, "更新插件配置失败: "+err.Error())
		return
	}
	response.Success(c, nil)
}

// ListPluginMenu 返回精简的插件元信息（仅含 name + type + frontend_pages）。
// 普通登录用户即可访问，前端 AppShell 据此渲染插件提供的页面菜单。
// 不会泄露插件配置或账号类型等敏感信息。
//
// 注意：**所有运行中的插件都会被列出**，包括那些 FrontendPages 为空的插件。
// 前端有两种使用场景：
//  1. 菜单渲染：循环时按 frontend_pages 是否为空来过滤
//  2. "某插件是否在线" 探测：例如 AppShell 用 list.some(p=>p.name==='airgate-health')
//     来决定要不要在顶栏显示 /status 入口
//
// 如果这里按 frontend_pages 过滤就会让场景 2 误判，已经被 airgate-health 删除
// admin 前端页面的场景命中过一次。
func (h *PluginHandler) ListPluginMenu(c *gin.Context) {
	list := h.service.List()
	resp := make([]dto.PluginResp, 0, len(list))
	for _, item := range list {
		menuItem := dto.PluginResp{Name: item.Name, Type: item.Type}
		for _, page := range item.FrontendPages {
			menuItem.FrontendPages = append(menuItem.FrontendPages, dto.FrontendPageResp{
				Path:        page.Path,
				Title:       page.Title,
				Icon:        page.Icon,
				Description: page.Description,
				Audience:    page.Audience,
			})
		}
		resp = append(resp, menuItem)
	}
	response.Success(c, response.PagedData(resp, int64(len(resp)), 1, len(resp)))
}

// UploadPlugin 上传安装插件。
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
	defer func() {
		_ = f.Close()
	}()

	binary, err := io.ReadAll(f)
	if err != nil {
		response.InternalError(c, "读取文件内容失败")
		return
	}

	name := c.PostForm("name")
	if name == "" {
		name = strings.TrimSuffix(file.Filename, ".exe")
	}

	if err := h.service.Upload(c.Request.Context(), name, binary); err != nil {
		response.InternalError(c, "安装插件失败: "+err.Error())
		return
	}

	response.Success(c, nil)
}

// InstallFromGithub 从 GitHub Release 安装插件。
func (h *PluginHandler) InstallFromGithub(c *gin.Context) {
	var req dto.InstallGithubReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数无效")
		return
	}

	if err := h.service.InstallFromGithub(c.Request.Context(), req.Repo); err != nil {
		response.InternalError(c, "从 GitHub 安装失败: "+err.Error())
		return
	}

	response.Success(c, nil)
}

// UninstallPlugin 卸载插件。
func (h *PluginHandler) UninstallPlugin(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		response.BadRequest(c, "插件名称无效")
		return
	}

	if err := h.service.Uninstall(c.Request.Context(), name); err != nil {
		response.InternalError(c, "卸载插件失败: "+err.Error())
		return
	}

	response.Success(c, nil)
}

// ReloadPlugin 热加载开发模式插件。
func (h *PluginHandler) ReloadPlugin(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		response.BadRequest(c, "插件名称无效")
		return
	}

	if err := h.service.Reload(c.Request.Context(), name); err != nil {
		if err == apppluginadmin.ErrPluginNotDev {
			response.BadRequest(c, err.Error())
			return
		}
		response.InternalError(c, "热加载插件失败: "+err.Error())
		return
	}

	response.Success(c, nil)
}

// ProxyRequest 通用插件请求代理。
func (h *PluginHandler) ProxyRequest(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		response.BadRequest(c, "插件名称无效")
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		response.BadRequest(c, "读取请求体失败")
		return
	}

	result, err := h.service.Proxy(c.Request.Context(), apppluginadmin.ProxyInput{
		Name:    name,
		Method:  c.Request.Method,
		Action:  strings.TrimPrefix(c.Param("action"), "/"),
		Query:   c.Request.URL.RawQuery,
		Headers: c.Request.Header,
		Body:    body,
	})
	if err != nil {
		switch err {
		case apppluginadmin.ErrPluginUnavailable:
			response.NotFound(c, err.Error())
		default:
			response.InternalError(c, "插件请求失败: "+err.Error())
		}
		return
	}

	for key, values := range result.Headers {
		for _, value := range values {
			c.Header(key, value)
		}
	}

	if result.StatusCode >= http.StatusOK && result.StatusCode < http.StatusBadRequest {
		var data any
		if err := json.Unmarshal(result.Body, &data); err != nil {
			data = string(result.Body)
		}
		response.Success(c, data)
		return
	}

	var errResp struct {
		Error string `json:"error"`
	}
	message := "插件请求失败"
	if json.Unmarshal(result.Body, &errResp) == nil && errResp.Error != "" {
		message = errResp.Error
	}
	response.Error(c, result.StatusCode, -1, message)
}

// RefreshMarketplace 强制从 GitHub 同步市场列表。
func (h *PluginHandler) RefreshMarketplace(c *gin.Context) {
	if err := h.service.RefreshMarketplace(c.Request.Context()); err != nil {
		response.InternalError(c, "刷新插件市场失败: "+err.Error())
		return
	}
	response.Success(c, nil)
}

// ListMarketplace 列出市场可用插件。
func (h *PluginHandler) ListMarketplace(c *gin.Context) {
	list, err := h.service.ListMarketplace(c.Request.Context())
	if err != nil {
		response.InternalError(c, "查询插件市场失败")
		return
	}

	resp := make([]dto.MarketplacePluginResp, 0, len(list))
	for _, item := range list {
		resp = append(resp, toMarketplacePluginResp(item))
	}
	response.Success(c, response.PagedData(resp, int64(len(resp)), 1, len(resp)))
}
