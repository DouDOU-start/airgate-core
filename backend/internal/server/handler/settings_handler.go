package handler

import (
	appsettings "github.com/DouDOU-start/airgate-core/internal/app/settings"
)

// SettingsHandler 系统设置 Handler。
type SettingsHandler struct {
	service      *appsettings.Service
	apiKeySecret string // AES-GCM 加密密钥
}

// NewSettingsHandler 创建 SettingsHandler。
func NewSettingsHandler(service *appsettings.Service, apiKeySecret string) *SettingsHandler {
	return &SettingsHandler{service: service, apiKeySecret: apiKeySecret}
}
