package handler

import (
	"errors"
	"log/slog"

	appapikey "github.com/DouDOU-start/airgate-core/internal/app/apikey"
)

// APIKeyHandler API 密钥管理 Handler。
type APIKeyHandler struct {
	service *appapikey.Service
}

// NewAPIKeyHandler 创建 APIKeyHandler。
func NewAPIKeyHandler(service *appapikey.Service) *APIKeyHandler {
	return &APIKeyHandler{service: service}
}

// parseKeyID 解析密钥 ID，委托给公共 ParseID。
var parseKeyID = ParseID

func (h *APIKeyHandler) handleError(logMessage, publicMessage string, err error) (int, string) {
	switch {
	case errors.Is(err, appapikey.ErrKeyNotFound):
		return 404, err.Error()
	case errors.Is(err, appapikey.ErrGroupNotFound):
		return 404, err.Error()
	case errors.Is(err, appapikey.ErrGroupForbidden):
		return 403, err.Error()
	case errors.Is(err, appapikey.ErrInvalidExpiresAt),
		errors.Is(err, appapikey.ErrLegacyKeyNotReveal),
		errors.Is(err, appapikey.ErrKeyDecryptFailed):
		return 400, err.Error()
	default:
		slog.Error(logMessage, "error", err)
		return 500, publicMessage
	}
}
