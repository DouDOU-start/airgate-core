package plugin

import (
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	pb "github.com/DouDOU-start/airgate-sdk/proto"
)

// 请求体大小限制（100MB）
const maxExtensionBodySize = 100 << 20

// 禁止插件设置的响应头（安全黑名单）
var blockedResponseHeaders = map[string]bool{
	"transfer-encoding":   true,
	"content-length":      true,
	"connection":          true,
	"keep-alive":          true,
	"upgrade":             true,
	"proxy-authenticate":  true,
	"proxy-authorization": true,
}

// ExtensionProxy 将 HTTP 请求代理到 extension 类型插件
type ExtensionProxy struct {
	manager *Manager
}

// NewExtensionProxy 创建 extension 代理
func NewExtensionProxy(manager *Manager) *ExtensionProxy {
	return &ExtensionProxy{manager: manager}
}

// Handle 处理 extension 插件的 HTTP 请求
// 路由格式：/api/v1/ext/:pluginName/*path
func (ep *ExtensionProxy) Handle(c *gin.Context) {
	pluginName := c.Param("pluginName")
	subPath := c.Param("path")
	if subPath == "" {
		subPath = "/"
	}

	slog.Debug("ExtensionProxy 收到请求", "pluginName", pluginName, "subPath", subPath, "method", c.Request.Method, "fullPath", c.Request.URL.Path)

	ext := ep.manager.GetExtensionByName(pluginName)
	if ext == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "extension 插件未找到或未运行"})
		return
	}

	// 限制请求体大小，防止恶意大请求导致内存溢出
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxExtensionBodySize)

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "读取请求体失败（可能超过大小限制）"})
		return
	}

	headers := make(map[string]*pb.HeaderValues)
	for k, v := range c.Request.Header {
		headers[strings.ToLower(k)] = &pb.HeaderValues{Values: v}
	}

	req := &pb.HttpRequest{
		Method:     c.Request.Method,
		Path:       subPath,
		Query:      c.Request.URL.RawQuery,
		Headers:    headers,
		Body:       body,
		RemoteAddr: c.ClientIP(),
	}

	resp, err := ext.HandleHTTPRequest(c.Request.Context(), req)
	if err != nil {
		slog.Error("extension 插件请求失败", "plugin", pluginName, "path", subPath, "error", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "extension 插件请求失败"})
		return
	}

	// 写回响应头（过滤掉安全黑名单中的头部）
	for k, vals := range resp.Headers {
		if blockedResponseHeaders[strings.ToLower(k)] {
			continue
		}
		for _, v := range vals.Values {
			c.Writer.Header().Add(k, v)
		}
	}

	// 确保 Content-Type 有默认值
	contentType := c.Writer.Header().Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// 验证状态码范围
	statusCode := int(resp.StatusCode)
	if statusCode < 100 || statusCode > 599 {
		statusCode = http.StatusBadGateway
	}

	c.Data(statusCode, contentType, resp.Body)
}
