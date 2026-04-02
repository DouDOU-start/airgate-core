// Package handler 提供 HTTP API 处理器。
package handler

import (
	apppluginadmin "github.com/DouDOU-start/airgate-core/internal/app/pluginadmin"
)

// PluginHandler 插件管理 API。
type PluginHandler struct {
	service *apppluginadmin.Service
}

// NewPluginHandler 创建插件管理 Handler。
func NewPluginHandler(service *apppluginadmin.Service) *PluginHandler {
	return &PluginHandler{
		service: service,
	}
}
