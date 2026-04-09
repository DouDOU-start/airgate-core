package handler

import (
	"errors"
	"log/slog"
	"strconv"

	appuser "github.com/DouDOU-start/airgate-core/internal/app/user"
)

// UserHandler 用户管理 Handler。
type UserHandler struct {
	service *appuser.Service
}

// NewUserHandler 创建 UserHandler。
func NewUserHandler(service *appuser.Service) *UserHandler {
	return &UserHandler{service: service}
}

func parseUserID(raw string) (int, error) {
	return strconv.Atoi(raw)
}

func (h *UserHandler) handleError(logMessage, publicMessage string, err error) (int, string) {
	switch {
	case errors.Is(err, appuser.ErrUserNotFound):
		return 404, err.Error()
	case errors.Is(err, appuser.ErrEmailAlreadyExists),
		errors.Is(err, appuser.ErrOldPasswordMismatch),
		errors.Is(err, appuser.ErrInsufficientBalance),
		errors.Is(err, appuser.ErrInvalidBalanceAction),
		errors.Is(err, appuser.ErrDeleteAdminForbidden),
		errors.Is(err, appuser.ErrInvalidRateMultiplier):
		return 400, err.Error()
	default:
		slog.Error(logMessage, "error", err)
		return 500, publicMessage
	}
}
