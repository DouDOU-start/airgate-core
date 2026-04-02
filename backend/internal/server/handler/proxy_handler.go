package handler

import (
	"errors"
	"log/slog"
	"strconv"

	appproxy "github.com/DouDOU-start/airgate-core/internal/app/proxy"
)

// ProxyHandler 代理管理 Handler。
type ProxyHandler struct {
	service *appproxy.Service
}

// NewProxyHandler 创建 ProxyHandler。
func NewProxyHandler(service *appproxy.Service) *ProxyHandler {
	return &ProxyHandler{service: service}
}

func parseProxyID(raw string) (int, error) {
	return strconv.Atoi(raw)
}

func (h *ProxyHandler) handleError(logMessage, publicMessage string, err error) (int, string) {
	switch {
	case errors.Is(err, appproxy.ErrProxyNotFound):
		return 404, err.Error()
	default:
		slog.Error(logMessage, "error", err)
		return 500, publicMessage
	}
}
