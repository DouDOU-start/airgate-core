package handler

import (
	"errors"
	"log/slog"

	apptier "github.com/DouDOU-start/airgate-core/internal/app/tier"
)

// TierHandler 用户等级管理 Handler。
type TierHandler struct {
	service *apptier.Service
}

// NewTierHandler 创建 TierHandler。
func NewTierHandler(service *apptier.Service) *TierHandler {
	return &TierHandler{service: service}
}

func (h *TierHandler) handleError(logMessage, publicMessage string, err error) (int, string) {
	var hasUsers *apptier.TierHasUsersError
	switch {
	case errors.Is(err, apptier.ErrTierNotFound):
		return 404, err.Error()
	case errors.Is(err, apptier.ErrTierNameExists),
		errors.Is(err, apptier.ErrInvalidRate),
		errors.As(err, &hasUsers):
		return 400, err.Error()
	default:
		slog.Error(logMessage, "error", err)
		return 500, publicMessage
	}
}
