package server

import (
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/plugin"
)

// DynamicRouter 动态路由表
// Gin 不支持动态移除路由，因此使用 catch-all handler + 内部路由表实现
type DynamicRouter struct {
	forwarder *plugin.Forwarder
	mu        sync.RWMutex
	routes    map[string]bool // "METHOD /path" → true（用于路由存在性检查）
}

// NewDynamicRouter 创建动态路由器
func NewDynamicRouter(forwarder *plugin.Forwarder) *DynamicRouter {
	return &DynamicRouter{
		forwarder: forwarder,
		routes:    make(map[string]bool),
	}
}

// Handle catch-all 路由处理器
// 所有携带 API Key 的请求都进入这里，先检查路由是否已注册，再转发到插件
func (dr *DynamicRouter) Handle(c *gin.Context) {
	if dr.forwarder == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "插件系统未就绪"})
		return
	}

	path := c.Param("path")
	if path == "" {
		path = c.Request.URL.Path
	}
	isWebSocket := isWebSocketUpgrade(c.Request)

	dr.mu.RLock()
	hasRoutes := len(dr.routes) > 0
	matched := dynamicRouteMatches(dr.routes, c.Request.Method, path, isWebSocket)
	dr.mu.RUnlock()

	if hasRoutes && !matched {
		c.JSON(http.StatusNotFound, gin.H{"error": "未知的 API 路径"})
		return
	}

	if isWebSocket {
		dr.forwarder.ForwardWebSocket(c)
		return
	}
	dr.forwarder.Forward(c)
}

func dynamicRouteMatches(routes map[string]bool, method, path string, isWebSocket bool) bool {
	if len(routes) == 0 {
		return true
	}
	if isWebSocket {
		// Plugins declare WebSocket endpoints with the synthetic method "WS",
		// while the wire-level handshake is an HTTP GET.
		return routes["WS "+path] || routes[method+" "+path]
	}
	return routes[method+" "+path]
}

func isWebSocketUpgrade(r *http.Request) bool {
	if r == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") {
		return false
	}
	for _, token := range strings.Split(r.Header.Get("Connection"), ",") {
		if strings.EqualFold(strings.TrimSpace(token), "upgrade") {
			return true
		}
	}
	return false
}

// AddRoutes 注册插件路由
func (dr *DynamicRouter) AddRoutes(pluginName string, routes []routeEntry) {
	dr.mu.Lock()
	defer dr.mu.Unlock()

	for _, r := range routes {
		key := r.Method + " " + r.Path
		dr.routes[key] = true
	}
}

// RemoveRoutes 移除插件路由
func (dr *DynamicRouter) RemoveRoutes(pluginName string, routes []routeEntry) {
	dr.mu.Lock()
	defer dr.mu.Unlock()

	for _, r := range routes {
		key := r.Method + " " + r.Path
		delete(dr.routes, key)
	}
}

// routeEntry 路由条目
type routeEntry struct {
	Method string
	Path   string
}
