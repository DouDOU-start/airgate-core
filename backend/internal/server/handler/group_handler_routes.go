package handler

import (
	"github.com/gin-gonic/gin"

	appgroup "github.com/DouDOU-start/airgate-core/internal/app/group"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// ListGroups 查询分组列表。
func (h *GroupHandler) ListGroups(c *gin.Context) {
	var page dto.PageReq
	if err := c.ShouldBindQuery(&page); err != nil {
		response.BindError(c, err)
		return
	}

	ctx := c.Request.Context()
	result, err := h.service.List(ctx, appgroup.ListFilter{
		Page:        page.Page,
		PageSize:    page.PageSize,
		Keyword:     page.Keyword,
		Platform:    c.Query("platform"),
		ServiceTier: c.Query("service_tier"),
	})
	if err != nil {
		httpCode, message := h.handleError("查询分组列表失败", "查询失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	// 批量查询分组统计
	groupIDs := make([]int, 0, len(result.List))
	for _, item := range result.List {
		groupIDs = append(groupIDs, item.ID)
	}
	statsMap, _ := h.service.StatsForGroups(ctx, groupIDs, c.Query("tz"))

	list := make([]dto.GroupResp, 0, len(result.List))
	for _, item := range result.List {
		resp := toGroupRespFromDomain(item)
		if stats, ok := statsMap[item.ID]; ok {
			resp.AccountActive = stats.AccountActive
			resp.AccountError = stats.AccountError
			resp.AccountDisabled = stats.AccountDisabled
			resp.AccountTotal = stats.AccountTotal
			resp.CapacityUsed = stats.CapacityUsed
			resp.CapacityTotal = stats.CapacityTotal
			resp.TodayCost = stats.TodayCost
			resp.TotalCost = stats.TotalCost
		}
		list = append(list, resp)
	}

	response.Success(c, response.PagedData(list, result.Total, result.Page, result.PageSize))
}

// ListAvailableGroups 查询当前用户可用分组列表。
func (h *GroupHandler) ListAvailableGroups(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}

	var page dto.PageReq
	if err := c.ShouldBindQuery(&page); err != nil {
		response.BindError(c, err)
		return
	}

	result, err := h.service.ListAvailable(c.Request.Context(), appgroup.AvailableFilter{
		UserID:   userID,
		Page:     page.Page,
		PageSize: page.PageSize,
		Keyword:  page.Keyword,
		Platform: c.Query("platform"),
	})
	if err != nil {
		httpCode, message := h.handleError("查询用户可用分组失败", "查询失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	list := make([]dto.GroupResp, 0, len(result.List))
	for _, item := range result.List {
		list = append(list, toGroupRespFromDomain(item))
	}

	response.Success(c, response.PagedData(list, result.Total, result.Page, result.PageSize))
}

// GetGroup 获取分组详情。
func (h *GroupHandler) GetGroup(c *gin.Context) {
	id, err := parseGroupID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的分组 ID")
		return
	}

	item, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		httpCode, message := h.handleError("查询分组失败", "查询失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, toGroupRespFromDomain(item))
}

// CreateGroup 创建分组。
func (h *GroupHandler) CreateGroup(c *gin.Context) {
	var req dto.CreateGroupReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	// StatusVisible 未提交时默认 true（保持旧行为兼容：已有接入方不传值就继续对外展示）。
	statusVisible := true
	if req.StatusVisible != nil {
		statusVisible = *req.StatusVisible
	}

	item, err := h.service.Create(c.Request.Context(), appgroup.CreateInput{
		Name:                     req.Name,
		Platform:                 req.Platform,
		RateMultiplier:           req.RateMultiplier,
		IsExclusive:              req.IsExclusive,
		StatusVisible:            statusVisible,
		SubscriptionType:         req.SubscriptionType,
		Quotas:                   req.Quotas,
		ModelRouting:             req.ModelRouting,
		ServiceTier:              req.ServiceTier,
		ForceInstructions:        req.ForceInstructions,
		Note:                     req.Note,
		SortWeight:               req.SortWeight,
		CopyAccountsFromGroupIDs: req.CopyAccountsFromGroupIDs,
	})
	if err != nil {
		httpCode, message := h.handleError("创建分组失败", "创建失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, toGroupRespFromDomain(item))
}

// UpdateGroup 更新分组。
func (h *GroupHandler) UpdateGroup(c *gin.Context) {
	id, err := parseGroupID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的分组 ID")
		return
	}

	var req dto.UpdateGroupReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	item, err := h.service.Update(c.Request.Context(), id, appgroup.UpdateInput{
		Name:              req.Name,
		RateMultiplier:    req.RateMultiplier,
		IsExclusive:       req.IsExclusive,
		StatusVisible:     req.StatusVisible,
		SubscriptionType:  req.SubscriptionType,
		Quotas:            req.Quotas,
		ModelRouting:      req.ModelRouting,
		ServiceTier:       req.ServiceTier,
		ForceInstructions: req.ForceInstructions,
		Note:              req.Note,
		SortWeight:        req.SortWeight,
	})
	if err != nil {
		httpCode, message := h.handleError("更新分组失败", "更新失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, toGroupRespFromDomain(item))
}

// DeleteGroup 删除分组。
func (h *GroupHandler) DeleteGroup(c *gin.Context) {
	id, err := parseGroupID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的分组 ID")
		return
	}

	if err := h.service.Delete(c.Request.Context(), id); err != nil {
		httpCode, message := h.handleError("删除分组失败", "删除失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, nil)
}
