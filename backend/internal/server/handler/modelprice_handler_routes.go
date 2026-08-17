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
		Page:          req.Page,
		PageSize:      req.PageSize,
		Keyword:       req.Keyword,
		TagID:         tagIDFromReq(req.TagID),
		Enabled:       req.Enabled,
		MarketVisible: req.MarketVisible,
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

// PublicListModelMarket 模型广场公开接口：未登录可见的模型价格 + 非专属分组倍率区间。
func (h *ModelPriceHandler) PublicListModelMarket(c *gin.Context) {
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
	result, err := h.service.ListPublic(c.Request.Context(), filter)
	if err != nil {
		httpCode, message := h.handleError("查询模型广场失败", "查询失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	list := make([]dto.ModelMarketItemResp, 0, len(result.List))
	for _, item := range result.List {
		list = append(list, toModelMarketItemRespFromDomain(item))
	}
	resp := dto.ModelMarketResp{List: list, Total: result.Total, Page: result.Page, PageSize: result.PageSize}
	if result.HasMultiplier {
		resp.Multiplier = &dto.ModelMarketMultiplierRange{Min: result.MultiplierMin, Max: result.MultiplierMax}
	}
	response.Success(c, resp)
}

// SyncModelPrices 从公共价格源刷新本地模型目录。
func (h *ModelPriceHandler) SyncModelPrices(c *gin.Context) {
	result, err := h.service.Sync(c.Request.Context())
	if err != nil {
		httpCode, message := h.handleError("同步模型价格失败", "同步模型价格失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, result)
}

// ListModelPriceSyncCandidates 返回可从远端目录同步的模型候选。
func (h *ModelPriceHandler) ListModelPriceSyncCandidates(c *gin.Context) {
	items, err := h.service.SyncCandidates(c.Request.Context())
	if err != nil {
		httpCode, message := h.handleError("获取模型同步候选失败", "获取模型同步候选失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, items)
}

// SyncSelectedModelPrices 仅同步管理员选择的远端模型。
func (h *ModelPriceHandler) SyncSelectedModelPrices(c *gin.Context) {
	var req dto.SyncModelPricesReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}
	result, err := h.service.SyncSelected(c.Request.Context(), req.Models)
	if err != nil {
		httpCode, message := h.handleError("同步所选模型失败", "同步所选模型失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, result)
}

// BulkUpdateModelPrices 批量启停模型、切换广场可见性或删除模型价格条目。
func (h *ModelPriceHandler) BulkUpdateModelPrices(c *gin.Context) {
	var req dto.BulkUpdateModelPricesReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}
	result := h.service.BulkUpdate(c.Request.Context(), appmodelprice.BulkUpdateInput{
		IDs:    req.IDs,
		Action: appmodelprice.BulkAction(req.Action),
	})
	items := make([]dto.BulkModelPriceResult, 0, len(result.Results))
	for _, item := range result.Results {
		items = append(items, dto.BulkModelPriceResult{ID: item.ID, Success: item.Success, Error: item.Error})
	}
	response.Success(c, dto.BulkUpdateModelPricesResp{
		Success: result.Success, Failed: result.Failed,
		SuccessIDs: result.SuccessIDs, FailedIDs: result.FailedIDs, Results: items,
	})
}

// CreateModelPrice 创建模型价格。
func (h *ModelPriceHandler) CreateModelPrice(c *gin.Context) {
	var req dto.CreateModelPriceReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	// MarketVisible 未提交时默认 true（保持旧行为兼容：已有接入方不传值就继续对外展示）。
	marketVisible := true
	if req.MarketVisible != nil {
		marketVisible = *req.MarketVisible
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
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
		Enabled:              &enabled,
		MarketVisible:        marketVisible,
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
		Enabled:              req.Enabled,
		MarketVisible:        req.MarketVisible,
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
