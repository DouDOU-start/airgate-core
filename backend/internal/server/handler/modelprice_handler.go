package handler

import (
	"errors"
	"log/slog"

	appmodelprice "github.com/DouDOU-start/airgate-core/internal/app/modelprice"
)

// ModelPriceHandler 模型价目表管理 Handler。
type ModelPriceHandler struct {
	service *appmodelprice.Service
}

// NewModelPriceHandler 创建 ModelPriceHandler。
func NewModelPriceHandler(service *appmodelprice.Service) *ModelPriceHandler {
	return &ModelPriceHandler{service: service}
}

// parseModelPriceID 解析价格条目 ID，委托给公共 ParseID。
var parseModelPriceID = ParseID

func (h *ModelPriceHandler) handleError(logMessage, publicMessage string, err error) (int, string) {
	switch {
	case errors.Is(err, appmodelprice.ErrModelPriceNotFound):
		return 404, err.Error()
	case errors.Is(err, appmodelprice.ErrModelPriceExists):
		return 400, err.Error()
	default:
		slog.Error(logMessage, "error", err)
		return 500, publicMessage
	}
}
