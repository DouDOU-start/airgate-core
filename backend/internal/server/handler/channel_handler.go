package handler

import (
	"errors"
	"log/slog"

	appchannel "github.com/DouDOU-start/airgate-core/internal/app/channel"
)

// ChannelHandler 渠道管理 Handler。
type ChannelHandler struct {
	service *appchannel.Service
}

// NewChannelHandler 创建 ChannelHandler。
func NewChannelHandler(service *appchannel.Service) *ChannelHandler {
	return &ChannelHandler{service: service}
}

func (h *ChannelHandler) handleError(logMessage, publicMessage string, err error) (int, string) {
	switch {
	case errors.Is(err, appchannel.ErrChannelNotFound):
		return 404, err.Error()
	case errors.Is(err, appchannel.ErrInvalidReference),
		errors.Is(err, appchannel.ErrInvalidBulkAction),
		errors.Is(err, appchannel.ErrInvalidProtocol),
		errors.Is(err, appchannel.ErrNoAPIKey):
		return 400, err.Error()
	case errors.Is(err, appchannel.ErrTesterNotReady):
		return 503, err.Error()
	case errors.Is(err, appchannel.ErrTestFailed),
		errors.Is(err, appchannel.ErrModelFetchFailed),
		errors.Is(err, appchannel.ErrBalanceFetchFailed):
		return 502, err.Error()
	default:
		slog.Error(logMessage, "error", err)
		return 500, publicMessage
	}
}
