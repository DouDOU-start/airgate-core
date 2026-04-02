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
