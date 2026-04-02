package handler

import (
	"github.com/gin-gonic/gin"

	appapikey "github.com/DouDOU-start/airgate-core/internal/app/apikey"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// ListKeys 查询当前用户的 API 密钥列表。
func (h *APIKeyHandler) ListKeys(c *gin.Context) {
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

	result, err := h.service.ListByUser(c.Request.Context(), userID, appapikey.ListFilter{
		Page:     page.Page,
		PageSize: page.PageSize,
		Keyword:  page.Keyword,
	})
	if err != nil {
		httpCode, message := h.handleError("查询密钥列表失败", "查询失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	list := make([]dto.APIKeyResp, 0, len(result.List))
	for _, item := range result.List {
		list = append(list, toAPIKeyResp(item))
	}
	response.Success(c, response.PagedData(list, result.Total, result.Page, result.PageSize))
}

// CreateKey 创建 API 密钥。
func (h *APIKeyHandler) CreateKey(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}

	var req dto.CreateAPIKeyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	item, err := h.service.CreateOwned(c.Request.Context(), userID, appapikey.CreateInput{
		Name:        req.Name,
		GroupID:     req.GroupID,
		IPWhitelist: req.IPWhitelist,
		IPBlacklist: req.IPBlacklist,
		QuotaUSD:    req.QuotaUSD,
		ExpiresAt:   req.ExpiresAt,
	})
	if err != nil {
		httpCode, message := h.handleError("创建 API 密钥失败", "创建失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, toAPIKeyResp(item))
}

// UpdateKey 更新 API 密钥。
func (h *APIKeyHandler) UpdateKey(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}

	id, err := parseKeyID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的密钥 ID")
		return
	}

	var req dto.UpdateAPIKeyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	item, err := h.service.UpdateOwned(c.Request.Context(), userID, id, appapikey.UpdateInput{
		Name:           req.Name,
		GroupID:        req.GroupID,
		IPWhitelist:    req.IPWhitelist,
		HasIPWhitelist: req.IPWhitelist != nil,
		IPBlacklist:    req.IPBlacklist,
		HasIPBlacklist: req.IPBlacklist != nil,
		QuotaUSD:       req.QuotaUSD,
		ExpiresAt:      req.ExpiresAt,
		Status:         req.Status,
	})
	if err != nil {
		httpCode, message := h.handleError("更新 API 密钥失败", "更新失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, toAPIKeyResp(item))
}

// DeleteKey 删除 API 密钥。
func (h *APIKeyHandler) DeleteKey(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}

	id, err := parseKeyID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的密钥 ID")
		return
	}

	if err := h.service.DeleteOwned(c.Request.Context(), userID, id); err != nil {
		httpCode, message := h.handleError("删除 API 密钥失败", "删除失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, nil)
}

// AdminUpdateKey 管理员更新 API Key。
func (h *APIKeyHandler) AdminUpdateKey(c *gin.Context) {
	id, err := parseKeyID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的密钥 ID")
		return
	}

	var req dto.AdminUpdateAPIKeyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	item, err := h.service.UpdateAdmin(c.Request.Context(), id, appapikey.UpdateInput{
		Name:           req.Name,
		GroupID:        req.GroupID,
		IPWhitelist:    req.IPWhitelist,
		HasIPWhitelist: req.IPWhitelist != nil,
		IPBlacklist:    req.IPBlacklist,
		HasIPBlacklist: req.IPBlacklist != nil,
		QuotaUSD:       req.QuotaUSD,
		ExpiresAt:      req.ExpiresAt,
		Status:         req.Status,
	})
	if err != nil {
		httpCode, message := h.handleError("管理员更新 API 密钥失败", "更新失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, toAPIKeyResp(item))
}

// RevealKey 查看 API 密钥原文。
func (h *APIKeyHandler) RevealKey(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}

	id, err := parseKeyID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的密钥 ID")
		return
	}

	item, err := h.service.RevealOwned(c.Request.Context(), userID, id)
	if err != nil {
		httpCode, message := h.handleError("查看 API 密钥原文失败", "查询失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, toAPIKeyResp(item))
}
