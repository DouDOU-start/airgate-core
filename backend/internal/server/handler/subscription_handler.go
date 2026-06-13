package handler

import (
	"errors"
	"log/slog"

	appsubscription "github.com/DouDOU-start/airgate-core/internal/app/subscription"
)

// SubscriptionHandler 订阅管理 Handler。
type SubscriptionHandler struct {
	service *appsubscription.Service
}

// NewSubscriptionHandler 创建 SubscriptionHandler。
func NewSubscriptionHandler(service *appsubscription.Service) *SubscriptionHandler {
	return &SubscriptionHandler{service: service}
}

// parseSubscriptionID 解析订阅 ID，委托给公共 ParseID。
var parseSubscriptionID = ParseID

func (h *SubscriptionHandler) handleError(logMessage, publicMessage string, err error) (int, string) {
	switch {
	case errors.Is(err, appsubscription.ErrSubscriptionNotFound):
		return 404, err.Error()
	case errors.Is(err, appsubscription.ErrInvalidExpiresAt),
		errors.Is(err, appsubscription.ErrInvalidAdjustExpiresAt):
		return 400, err.Error()
	default:
		slog.Error(logMessage, "error", err)
		return 500, publicMessage
	}
}
