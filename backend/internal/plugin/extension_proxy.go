package plugin

import (
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	pb "github.com/DouDOU-start/airgate-sdk/proto"

	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
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
// 支持三种入口：
//
//	/api/v1/ext/:pluginName/*path             — 管理员级（X-Airgate-Entry: admin）
//	/api/v1/ext-user/:pluginName/*path        — 用户级（X-Airgate-Entry: user）
//	/api/v1/payment-callback/:pluginName/*path — 公开回调（X-Airgate-Entry: callback，无用户身份）
func (ep *ExtensionProxy) Handle(c *gin.Context) {
	pluginName := c.Param("pluginName")
	subPath := c.Param("path")
	if subPath == "" {
		subPath = "/"
	}

	// 根据完整路径前缀推断 entry 类型
	entry := "admin"
	switch {
	case strings.HasPrefix(c.FullPath(), "/api/v1/payment-callback"):
		entry = "callback"
	case strings.HasPrefix(c.FullPath(), "/api/v1/ext-user"):
		entry = "user"
	}

	ep.handle(c, pluginName, subPath, entry)
}

// HandleNamed 返回一个固定 pluginName + entry 的代理 handler。
//
// 用于 path 不携带 :pluginName 的特殊路由（例如对外公开的 /status / /status/* 反向代理
// 到 airgate-health 插件）。entry 会被原样写入 X-Airgate-Entry 头，插件路由层据此鉴权。
//
// 与 Handle 的差异：
//   - pluginName 来自调用方传入的参数（不读 :pluginName param）
//   - 子路径来自 :path wildcard：路由声明的 /xxx/*path 在请求 /xxx/ 时 wildcard
//     会捕获到 "/"，请求 /xxx/foo 时捕获到 "/foo"。对于裸的 /xxx 路由（无 *path），
//     wildcard 为空，我们将其归一化为 "/"，让插件不用区分这两种情况。
//   - entry 由调用方决定（典型值：public）
func (ep *ExtensionProxy) HandleNamed(pluginName, entry string) gin.HandlerFunc {
	return func(c *gin.Context) {
		subPath := c.Param("path")
		if subPath == "" {
			subPath = "/"
		}
		ep.handle(c, pluginName, subPath, entry)
	}
}

// handle 是 Handle / HandleNamed 的共享实现：把 HTTP 请求序列化成 pb.HttpRequest
// 通过 gRPC 转发给目标插件，再把响应写回 client。
func (ep *ExtensionProxy) handle(c *gin.Context, pluginName, subPath, entry string) {
	slog.Debug("ExtensionProxy 收到请求", "pluginName", pluginName, "subPath", subPath, "entry", entry, "method", c.Request.Method, "fullPath", c.Request.URL.Path)

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

	// 注入 airgate 内部头：让插件知道入口类型与请求用户身份
	// 这些头由 core 强制覆盖，外部无法伪造（因为客户端发送的同名头会被 c.Request.Header 中的最新值覆盖）
	headers["x-airgate-entry"] = &pb.HeaderValues{Values: []string{entry}}
	if uid, ok := c.Get(middleware.CtxKeyUserID); ok {
		if id, ok := uid.(int); ok {
			headers["x-airgate-user-id"] = &pb.HeaderValues{Values: []string{strconv.Itoa(id)}}
		}
	}
	if role, ok := c.Get(middleware.CtxKeyRole); ok {
		if r, ok := role.(string); ok {
			headers["x-airgate-role"] = &pb.HeaderValues{Values: []string{r}}
		}
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
		// 仅对内部入口（admin / user）回显原始错误，便于定位问题；
		// 对外部入口（callback / public）保持泛化，避免向第三方泄漏内部细节。
		msg := "extension 插件请求失败"
		if entry == "admin" || entry == "user" {
			msg = msg + ": " + err.Error()
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": msg})
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
