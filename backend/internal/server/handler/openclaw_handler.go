package handler

import (
	appopenclaw "github.com/DouDOU-start/airgate-core/internal/app/openclaw"
)

// OpenClawHandler 负责 /openclaw/* 一键接入相关的公共路由。
type OpenClawHandler struct {
	service *appopenclaw.Service
}

// NewOpenClawHandler 创建 OpenClawHandler。
func NewOpenClawHandler(service *appopenclaw.Service) *OpenClawHandler {
	return &OpenClawHandler{service: service}
}
