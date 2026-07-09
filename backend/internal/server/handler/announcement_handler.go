package handler

import (
	"errors"
	"log/slog"

	appannouncement "github.com/DouDOU-start/airgate-core/internal/app/announcement"
)

// AnnouncementHandler 公告 Handler：管理端 CRUD + 用户端查看/标已读。
type AnnouncementHandler struct {
	service *appannouncement.Service
}

// NewAnnouncementHandler 创建 AnnouncementHandler。
func NewAnnouncementHandler(service *appannouncement.Service) *AnnouncementHandler {
	return &AnnouncementHandler{service: service}
}

func (h *AnnouncementHandler) handleError(logMessage, publicMessage string, err error) (int, string) {
	switch {
	case errors.Is(err, appannouncement.ErrAnnouncementNotFound):
		return 404, err.Error()
	case errors.Is(err, appannouncement.ErrInvalidStatus),
		errors.Is(err, appannouncement.ErrInvalidNotifyMode),
		errors.Is(err, appannouncement.ErrInvalidTimeRange),
		errors.Is(err, appannouncement.ErrInvalidTime):
		return 400, err.Error()
	default:
		slog.Error(logMessage, "error", err)
		return 500, publicMessage
	}
}
