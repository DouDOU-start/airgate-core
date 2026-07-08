package handler

import (
	"github.com/gin-gonic/gin"

	appmodelprice "github.com/DouDOU-start/airgate-core/internal/app/modelprice"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// ListModelPrices 分页列表模型价格。
func (h *ModelPriceHandler) ListModelPrices(c *gin.Context) {
	var page dto.PageReq
	if err := c.ShouldBindQuery(&page); err != nil {
		response.BindError(c, err)
		return
	}

	result, err := h.service.List(c.Request.Context(), appmodelprice.ListFilter{
		Page:     page.Page,
		PageSize: page.PageSize,
		Keyword:  page.Keyword,
	})
	if err != nil {
		httpCode, message := h.handleError("查询模型价格列表失败", "查询失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	list := make([]dto.ModelPriceResp, 0, len(result.List))
	for _, item := range result.List {
		list = append(list, toModelPriceRespFromDomain(item))
	}
	response.Success(c, response.PagedData(list, result.Total, result.Page, result.PageSize))
}

// CreateModelPrice 创建模型价格。
func (h *ModelPriceHandler) CreateModelPrice(c *gin.Context) {
	var req dto.CreateModelPriceReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	item, err := h.service.Create(c.Request.Context(), appmodelprice.CreateInput{
		Model:                req.Model,
		InputPrice:           req.InputPrice,
		OutputPrice:          req.OutputPrice,
		CachedInputPrice:     req.CachedInputPrice,
		CacheCreationPrice:   req.CacheCreationPrice,
		CacheCreation1hPrice: req.CacheCreation1hPrice,
		PerRequestPrice:      req.PerRequestPrice,
		PricingExtra:         req.PricingExtra,
	})
	if err != nil {
		httpCode, message := h.handleError("创建模型价格失败", "创建失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, toModelPriceRespFromDomain(item))
}

// UpdateModelPrice 更新模型价格。
func (h *ModelPriceHandler) UpdateModelPrice(c *gin.Context) {
	id, err := parseModelPriceID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的价格条目 ID")
		return
	}

	var req dto.UpdateModelPriceReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	item, err := h.service.Update(c.Request.Context(), id, appmodelprice.UpdateInput{
		Model:                req.Model,
		InputPrice:           req.InputPrice,
		OutputPrice:          req.OutputPrice,
		CachedInputPrice:     req.CachedInputPrice,
		CacheCreationPrice:   req.CacheCreationPrice,
		CacheCreation1hPrice: req.CacheCreation1hPrice,
		PerRequestPrice:      req.PerRequestPrice,
		PricingExtra:         req.PricingExtra,
	})
	if err != nil {
		httpCode, message := h.handleError("更新模型价格失败", "更新失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, toModelPriceRespFromDomain(item))
}

// DeleteModelPrice 删除模型价格。
func (h *ModelPriceHandler) DeleteModelPrice(c *gin.Context) {
	id, err := parseModelPriceID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的价格条目 ID")
		return
	}

	if err := h.service.Delete(c.Request.Context(), id); err != nil {
		httpCode, message := h.handleError("删除模型价格失败", "删除失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, nil)
}

// ImportModelPrices 批量导入模型价格（按 model 名 upsert）。
func (h *ModelPriceHandler) ImportModelPrices(c *gin.Context) {
	var req dto.ImportModelPricesReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	items := make([]appmodelprice.ImportItem, 0, len(req.Items))
	for _, item := range req.Items {
		items = append(items, appmodelprice.ImportItem{
			Model:                item.Model,
			InputPrice:           item.InputPrice,
			OutputPrice:          item.OutputPrice,
			CachedInputPrice:     item.CachedInputPrice,
			CacheCreationPrice:   item.CacheCreationPrice,
			CacheCreation1hPrice: item.CacheCreation1hPrice,
			PerRequestPrice:      item.PerRequestPrice,
			PricingExtra:         item.PricingExtra,
		})
	}

	result, err := h.service.Import(c.Request.Context(), items)
	if err != nil {
		httpCode, message := h.handleError("导入模型价格失败", "导入失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, dto.ImportModelPricesResp{
		Created: result.Created,
		Updated: result.Updated,
	})
}
