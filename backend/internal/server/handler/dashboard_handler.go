package handler

import (
	"log/slog"

	appdashboard "github.com/DouDOU-start/airgate-core/internal/app/dashboard"
)

// DashboardHandler 仪表盘 Handler。
type DashboardHandler struct {
	service *appdashboard.Service
}

// NewDashboardHandler 创建 DashboardHandler。
func NewDashboardHandler(service *appdashboard.Service) *DashboardHandler {
	return &DashboardHandler{service: service}
}

func (h *DashboardHandler) handleError(logMessage string, err error) {
	slog.Error(logMessage, "error", err)
}
