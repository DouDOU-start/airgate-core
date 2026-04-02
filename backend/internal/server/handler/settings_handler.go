package handler

import (
	appsettings "github.com/DouDOU-start/airgate-core/internal/app/settings"
)

// SettingsHandler 系统设置 Handler。
type SettingsHandler struct {
	service *appsettings.Service
}

// NewSettingsHandler 创建 SettingsHandler。
func NewSettingsHandler(service *appsettings.Service) *SettingsHandler {
	return &SettingsHandler{service: service}
}
