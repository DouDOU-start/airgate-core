package handler

import (
	"github.com/gin-gonic/gin"

	apptier "github.com/DouDOU-start/airgate-core/internal/app/tier"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// ListTiers 查询等级列表。
func (h *TierHandler) ListTiers(c *gin.Context) {
	var page dto.PageReq
	if err := c.ShouldBindQuery(&page); err != nil {
		response.BindError(c, err)
		return
	}

	result, err := h.service.List(c.Request.Context(), apptier.ListFilter{
		Page:     page.Page,
		PageSize: page.PageSize,
		Keyword:  page.Keyword,
	})
	if err != nil {
		httpCode, message := h.handleError("查询等级列表失败", "查询失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	list := make([]dto.TierResp, 0, len(result.List))
	for _, item := range result.List {
		list = append(list, toTierRespFromDomain(item))
	}
	response.Success(c, response.PagedData(list, result.Total, result.Page, result.PageSize))
}

// GetTier 获取等级详情。
func (h *TierHandler) GetTier(c *gin.Context) {
	id, err := ParseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的等级 ID")
		return
	}

	item, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		httpCode, message := h.handleError("查询等级失败", "查询失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, toTierRespFromDomain(item))
}

// CreateTier 创建等级。
func (h *TierHandler) CreateTier(c *gin.Context) {
	var req dto.CreateTierReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	item, err := h.service.Create(c.Request.Context(), apptier.CreateInput{
		Name:       req.Name,
		Rates:      req.Rates,
		Note:       req.Note,
		SortWeight: req.SortWeight,
	})
	if err != nil {
		httpCode, message := h.handleError("创建等级失败", "创建失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, toTierRespFromDomain(item))
}

// UpdateTier 更新等级。
func (h *TierHandler) UpdateTier(c *gin.Context) {
	id, err := ParseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的等级 ID")
		return
	}

	var req dto.UpdateTierReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	item, err := h.service.Update(c.Request.Context(), id, apptier.UpdateInput{
		Name:       req.Name,
		Rates:      req.Rates,
		HasRates:   req.Rates != nil,
		Note:       req.Note,
		SortWeight: req.SortWeight,
	})
	if err != nil {
		httpCode, message := h.handleError("更新等级失败", "更新失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, toTierRespFromDomain(item))
}

// DeleteTier 删除等级。
func (h *TierHandler) DeleteTier(c *gin.Context) {
	id, err := ParseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的等级 ID")
		return
	}

	if err := h.service.Delete(c.Request.Context(), id); err != nil {
		httpCode, message := h.handleError("删除等级失败", "删除失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, nil)
}
