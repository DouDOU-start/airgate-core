package server

import (
	"net/http"
	"sync"

	"github.com/DouDOU-start/airgate-core/internal/plugin"
	"github.com/gin-gonic/gin"
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
// 所有 /v1/* 请求都进入这里，先检查路由是否已注册，再转发到插件
func (dr *DynamicRouter) Handle(c *gin.Context) {
	if dr.forwarder == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "插件系统未就绪"})
		return
	}

	// 检查路由是否已注册
	dr.mu.RLock()
	hasRoutes := len(dr.routes) > 0
	dr.mu.RUnlock()

	// 如果有注册路由，则检查当前请求是否匹配
	if hasRoutes {
		key := c.Request.Method + " " + c.Request.URL.Path
		dr.mu.RLock()
		matched := dr.routes[key]
		dr.mu.RUnlock()
		if !matched {
			c.JSON(http.StatusNotFound, gin.H{"error": "未知的 API 路径"})
			return
		}
	}

	dr.forwarder.Forward(c)
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
