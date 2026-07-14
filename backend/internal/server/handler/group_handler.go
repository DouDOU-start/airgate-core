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

func (h *GroupHandler) handleError(logMessage, publicMessage string, err error) (int, string) {
	var hasChannels *appgroup.GroupHasChannelsError
	switch {
	case errors.Is(err, appgroup.ErrGroupNotFound):
		return 404, err.Error()
	case errors.Is(err, appgroup.ErrUserNotFound):
		return 404, err.Error()
	case errors.Is(err, appgroup.ErrChannelKeyNotFound):
		return 404, err.Error()
	case errors.As(err, &hasChannels):
		// 分组仍绑定渠道：拒绝删除，提示先解绑（防专属渠道静默变公共）。
		return 400, err.Error()
	default:
		slog.Error(logMessage, "error", err)
		return 500, publicMessage
	}
}
