package handler

import (
	"errors"
	"log/slog"

	appgroup "github.com/DouDOU-start/airgate-core/internal/app/group"
)

// GroupHandler 分组管理 Handler。
type GroupHandler struct {
	service *appgroup.Service
}

// NewGroupHandler 创建 GroupHandler。
func NewGroupHandler(service *appgroup.Service) *GroupHandler {
	return &GroupHandler{service: service}
}

// parseGroupID 解析分组 ID，委托给公共 ParseID。
var parseGroupID = ParseID

func (h *GroupHandler) handleError(logMessage, publicMessage string, err error) (int, string) {
	switch {
	case errors.Is(err, appgroup.ErrGroupNotFound):
		return 404, err.Error()
	case errors.Is(err, appgroup.ErrGroupHasSubscriptions):
		return 400, err.Error()
	case errors.Is(err, appgroup.ErrSourceGroupPlatformMismatch):
		return 400, err.Error()
	default:
		slog.Error(logMessage, "error", err)
		return 500, publicMessage
	}
}
