package handler

import (
	"errors"
	"log/slog"

	appbookmark "github.com/DouDOU-start/airgate-core/internal/app/bookmark"
)

// BookmarkHandler 备忘录 Handler。
type BookmarkHandler struct {
	service *appbookmark.Service
}

// NewBookmarkHandler 创建 BookmarkHandler。
func NewBookmarkHandler(service *appbookmark.Service) *BookmarkHandler {
	return &BookmarkHandler{service: service}
}

func (h *BookmarkHandler) handleError(logMessage, publicMessage string, err error) (int, string) {
	switch {
	case errors.Is(err, appbookmark.ErrBookmarkNotFound):
		return 404, err.Error()
	default:
		slog.Error(logMessage, "error", err)
		return 500, publicMessage
	}
}
