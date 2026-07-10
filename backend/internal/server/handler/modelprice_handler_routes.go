package handler

import (
	"github.com/gin-gonic/gin"

	appmodelprice "github.com/DouDOU-start/airgate-core/internal/app/modelprice"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// ListModelPrices 分页列表模型价格。
func (h *ModelPriceHandler) ListModelPrices(c *gin.Context) {
	var req dto.ListModelPricesReq
	if err := c.ShouldBindQuery(&req); err != nil {
		response.BindError(c, err)
		return
	}

	filter := appmodelprice.ListFilter{
		Page:     req.Page,
		PageSize: req.PageSize,
		Keyword:  req.Keyword,
		TagID:    tagIDFromReq(req.TagID),
	}
	result, err := h.service.List(c.Request.Context(), filter)
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
		TagID:                tagIDFromReq(req.TagID),
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
	id, err := ParseID(c.Param("id"))
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
		TagID:                tagIDFromReq(req.TagID),
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
	id, err := ParseID(c.Param("id"))
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

// —— 模型标签端点（家族归类，归属模型管理）——

// ListModelTags 列出全部模型标签（含模型计数）。
func (h *ModelPriceHandler) ListModelTags(c *gin.Context) {
	tags, err := h.service.ListTags(c.Request.Context())
	if err != nil {
		httpCode, message := h.handleError("查询模型标签失败", "查询失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	list := make([]dto.ModelTagResp, 0, len(tags))
	for _, tag := range tags {
		list = append(list, toModelTagRespFromDomain(tag))
	}
	response.Success(c, list)
}

// CreateModelTag 新建模型标签。
func (h *ModelPriceHandler) CreateModelTag(c *gin.Context) {
	var req dto.CreateModelTagReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}
	tag, err := h.service.CreateTag(c.Request.Context(), req.Name)
	if err != nil {
		httpCode, message := h.handleError("创建模型标签失败", "创建失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, toModelTagRespFromDomain(tag))
}

// UpdateModelTag 重命名模型标签。
func (h *ModelPriceHandler) UpdateModelTag(c *gin.Context) {
	id, err := ParseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的标签 ID")
		return
	}
	var req dto.UpdateModelTagReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}
	tag, err := h.service.RenameTag(c.Request.Context(), id, req.Name)
	if err != nil {
		httpCode, message := h.handleError("重命名模型标签失败", "更新失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, toModelTagRespFromDomain(tag))
}

// DeleteModelTag 删除模型标签（引用该标签的模型置为无标签）。
func (h *ModelPriceHandler) DeleteModelTag(c *gin.Context) {
	id, err := ParseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的标签 ID")
		return
	}
	if err := h.service.DeleteTag(c.Request.Context(), id); err != nil {
		httpCode, message := h.handleError("删除模型标签失败", "删除失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, nil)
}
