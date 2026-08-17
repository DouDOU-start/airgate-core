package handler

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/pluginruntime"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

const pluginUploadRequestOverhead int64 = 2 << 20

// PluginHandler 提供通用独立进程插件的管理接口。
type PluginHandler struct {
	manager    *pluginruntime.Manager
	httpClient *http.Client
}

// NewPluginHandler 创建插件管理 Handler。
func NewPluginHandler(manager *pluginruntime.Manager) *PluginHandler {
	return &PluginHandler{
		manager: manager,
		httpClient: &http.Client{
			Timeout: 2 * time.Minute,
			CheckRedirect: func(_ *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("插件下载重定向次数过多")
				}
				return nil
			},
		},
	}
}

// ListPlugins 查询文件系统中已安装的插件。
func (h *PluginHandler) ListPlugins(c *gin.Context) {
	items, err := h.manager.ListInstalled()
	if err != nil {
		slog.Error("查询插件列表失败", "error", err)
		response.InternalError(c, "查询插件列表失败")
		return
	}
	response.Success(c, items)
}

// UploadPlugin 上传并安装当前系统可执行的插件二进制。
func (h *PluginHandler) UploadPlugin(c *gin.Context) {
	limit := pluginruntime.MaxPluginBinarySize + pluginUploadRequestOverhead
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		response.BadRequest(c, "请选择插件二进制文件")
		return
	}
	defer func() { _ = file.Close() }()
	if header.Size <= 0 {
		response.BadRequest(c, "插件二进制不能为空")
		return
	}
	if header.Size > pluginruntime.MaxPluginBinarySize {
		response.BadRequest(c, "插件二进制不能超过 500MB")
		return
	}

	requestedID := strings.TrimSpace(c.PostForm("id"))
	configText := c.PostForm("config")
	source := "upload:" + filepath.Base(header.Filename)
	item, err := h.manager.InstallBinary(c.Request.Context(), requestedID, source, file, configText)
	if err != nil {
		h.handleOperationError(c, "上传安装插件失败", err)
		return
	}
	response.Success(c, item)
}

// UpdatePlugin 上传新二进制并原地更新插件，保留现有配置和启停状态。
func (h *PluginHandler) UpdatePlugin(c *gin.Context) {
	limit := pluginruntime.MaxPluginBinarySize + pluginUploadRequestOverhead
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		response.BadRequest(c, "请选择插件二进制文件")
		return
	}
	defer func() { _ = file.Close() }()
	if header.Size <= 0 {
		response.BadRequest(c, "插件二进制不能为空")
		return
	}
	if header.Size > pluginruntime.MaxPluginBinarySize {
		response.BadRequest(c, "插件二进制不能超过 500MB")
		return
	}

	source := "upload:" + filepath.Base(header.Filename)
	item, err := h.manager.UpdateBinary(c.Request.Context(), c.Param("id"), source, file)
	if err != nil {
		h.handleOperationError(c, "更新插件失败", err)
		return
	}
	response.Success(c, item)
}

type installPluginURLRequest struct {
	URL    string `json:"url" binding:"required"`
	ID     string `json:"id"`
	Config string `json:"config"`
}

// InstallPluginFromURL 从 HTTP/HTTPS 地址下载并安装插件二进制。
func (h *PluginHandler) InstallPluginFromURL(c *gin.Context) {
	var input installPluginURLRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BindError(c, err)
		return
	}
	parsed, err := url.Parse(strings.TrimSpace(input.URL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		response.BadRequest(c, "插件地址必须是有效的 HTTP 或 HTTPS URL")
		return
	}

	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, parsed.String(), nil)
	if err != nil {
		response.BadRequest(c, "插件下载地址无效")
		return
	}
	req.Header.Set("User-Agent", "AirGate-Plugin-Installer/1.0")
	download, err := h.httpClient.Do(req)
	if err != nil {
		h.handleOperationError(c, "下载插件失败", err)
		return
	}
	defer func() { _ = download.Body.Close() }()
	if download.StatusCode < 200 || download.StatusCode >= 300 {
		response.BadRequest(c, fmt.Sprintf("下载插件失败，上游返回 HTTP %d", download.StatusCode))
		return
	}
	if download.ContentLength > pluginruntime.MaxPluginBinarySize {
		response.BadRequest(c, "插件二进制不能超过 500MB")
		return
	}

	item, err := h.manager.InstallBinary(
		c.Request.Context(), strings.TrimSpace(input.ID), sanitizedPluginSource(download.Request.URL), download.Body, input.Config,
	)
	if err != nil {
		h.handleOperationError(c, "地址安装插件失败", err)
		return
	}
	response.Success(c, item)
}

// GetPluginConfig 获取插件声明的动态表单和当前值。
func (h *PluginHandler) GetPluginConfig(c *gin.Context) {
	form, err := h.manager.GetConfigForm(c.Param("id"))
	if err != nil {
		h.handleReadError(c, "读取插件配置失败", err)
		return
	}
	response.Success(c, form)
}

type updatePluginConfigRequest struct {
	Values map[string]any `json:"values" binding:"required"`
}

// UpdatePluginConfig 保存表单值，运行中的插件会自动平滑重载。
func (h *PluginHandler) UpdatePluginConfig(c *gin.Context) {
	var input updatePluginConfigRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BindError(c, err)
		return
	}
	if err := h.manager.UpdateConfigForm(c.Request.Context(), c.Param("id"), input.Values); err != nil {
		h.handleOperationError(c, "保存插件配置失败", err)
		return
	}
	response.Success(c, nil)
}

type setPluginEnabledRequest struct {
	Enabled bool `json:"enabled"`
}

// SetPluginEnabled 启用或停用插件进程。
func (h *PluginHandler) SetPluginEnabled(c *gin.Context) {
	var input setPluginEnabledRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BindError(c, err)
		return
	}
	if err := h.manager.SetEnabled(c.Request.Context(), c.Param("id"), input.Enabled); err != nil {
		h.handleOperationError(c, "切换插件状态失败", err)
		return
	}
	response.Success(c, nil)
}

// ReloadPlugin 重载已启用插件。
func (h *PluginHandler) ReloadPlugin(c *gin.Context) {
	if err := h.manager.Reload(c.Request.Context(), c.Param("id")); err != nil {
		h.handleOperationError(c, "重载插件失败", err)
		return
	}
	response.Success(c, nil)
}

// UninstallPlugin 停止并卸载插件。
func (h *PluginHandler) UninstallPlugin(c *gin.Context) {
	if err := h.manager.Uninstall(c.Request.Context(), c.Param("id")); err != nil {
		h.handleOperationError(c, "卸载插件失败", err)
		return
	}
	response.Success(c, nil)
}

func (h *PluginHandler) handleReadError(c *gin.Context, logMessage string, err error) {
	if errors.Is(err, pluginruntime.ErrPluginNotFound) {
		response.NotFound(c, err.Error())
		return
	}
	if errors.Is(err, pluginruntime.ErrInvalidPluginID) {
		response.BadRequest(c, err.Error())
		return
	}
	if errors.Is(err, pluginruntime.ErrPluginConfigUnsupported) {
		response.BadRequest(c, err.Error())
		return
	}
	slog.Error(logMessage, "error", err)
	response.InternalError(c, logMessage)
}

func (h *PluginHandler) handleOperationError(c *gin.Context, logMessage string, err error) {
	switch {
	case errors.Is(err, pluginruntime.ErrPluginNotFound):
		response.NotFound(c, err.Error())
	case errors.Is(err, pluginruntime.ErrPluginExists):
		response.Error(c, http.StatusConflict, http.StatusConflict, err.Error())
	case errors.Is(err, pluginruntime.ErrInvalidPluginID), errors.Is(err, pluginruntime.ErrPluginDisabled), errors.Is(err, pluginruntime.ErrPluginConfigUnsupported), errors.Is(err, pluginruntime.ErrPluginConfigIncomplete):
		response.BadRequest(c, err.Error())
	default:
		// 进程握手、插件初始化和配置解析错误需要直接反馈给管理员，便于修正安装包或表单配置。
		slog.Warn(logMessage, "error", err)
		response.BadRequest(c, err.Error())
	}
}

func sanitizedPluginSource(value *url.URL) string {
	if value == nil {
		return "url"
	}
	clean := &url.URL{Scheme: value.Scheme, Host: value.Host, Path: value.Path}
	return clean.String()
}
