package handler

import (
	"errors"
	"log/slog"

	appriskcontrol "github.com/DouDOU-start/airgate-core/internal/app/riskcontrol"
	"github.com/DouDOU-start/airgate-core/internal/moderation"
)

// RiskControlHandler 风控中心管理 Handler。
type RiskControlHandler struct {
	service *appriskcontrol.Service
}

// NewRiskControlHandler 创建 RiskControlHandler。
func NewRiskControlHandler(service *appriskcontrol.Service) *RiskControlHandler {
	return &RiskControlHandler{service: service}
}

func (h *RiskControlHandler) handleError(logMessage, publicMessage string, err error) (int, string) {
	switch {
	case errors.Is(err, appriskcontrol.ErrUserNotFound):
		return 404, err.Error()
	case errors.Is(err, appriskcontrol.ErrInvalidConfig),
		errors.Is(err, appriskcontrol.ErrInvalidHash),
		errors.Is(err, moderation.ErrBadInput):
		return 400, err.Error()
	default:
		slog.Error(logMessage, "error", err)
		return 500, publicMessage
	}
}
