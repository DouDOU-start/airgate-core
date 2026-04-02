package handler

import (
	"log/slog"

	appusage "github.com/DouDOU-start/airgate-core/internal/app/usage"
)

// UsageHandler 使用记录 Handler。
type UsageHandler struct {
	service *appusage.Service
}

// NewUsageHandler 创建 UsageHandler。
func NewUsageHandler(service *appusage.Service) *UsageHandler {
	return &UsageHandler{service: service}
}

func handleUsageError(logMessage string, err error) {
	slog.Error(logMessage, "error", err)
}
