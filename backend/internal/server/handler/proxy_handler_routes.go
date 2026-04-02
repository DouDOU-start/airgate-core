package handler

import (
	"github.com/gin-gonic/gin"

	appproxy "github.com/DouDOU-start/airgate-core/internal/app/proxy"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// ListProxies 分页列表代理。
func (h *ProxyHandler) ListProxies(c *gin.Context) {
	var page dto.PageReq
	if err := c.ShouldBindQuery(&page); err != nil {
		response.BindError(c, err)
		return
	}

	result, err := h.service.List(c.Request.Context(), appproxy.ListFilter{
		Page:     page.Page,
		PageSize: page.PageSize,
		Keyword:  page.Keyword,
		Status:   c.Query("status"),
	})
	if err != nil {
		httpCode, message := h.handleError("查询代理列表失败", "查询失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	list := make([]dto.ProxyResp, 0, len(result.List))
	for _, item := range result.List {
		list = append(list, toProxyRespFromDomain(item))
	}
	response.Success(c, response.PagedData(list, result.Total, result.Page, result.PageSize))
}

// CreateProxy 创建代理。
func (h *ProxyHandler) CreateProxy(c *gin.Context) {
	var req dto.CreateProxyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	item, err := h.service.Create(c.Request.Context(), appproxy.CreateInput{
		Name:     req.Name,
		Protocol: req.Protocol,
		Address:  req.Address,
		Port:     req.Port,
		Username: req.Username,
		Password: req.Password,
	})
	if err != nil {
		httpCode, message := h.handleError("创建代理失败", "创建失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, toProxyRespFromDomain(item))
}

// UpdateProxy 更新代理。
func (h *ProxyHandler) UpdateProxy(c *gin.Context) {
	id, err := parseProxyID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的代理 ID")
		return
	}

	var req dto.UpdateProxyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	item, err := h.service.Update(c.Request.Context(), id, appproxy.UpdateInput{
		Name:     req.Name,
		Protocol: req.Protocol,
		Address:  req.Address,
		Port:     req.Port,
		Username: req.Username,
		Password: req.Password,
		Status:   req.Status,
	})
	if err != nil {
		httpCode, message := h.handleError("更新代理失败", "更新失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, toProxyRespFromDomain(item))
}

// DeleteProxy 删除代理。
func (h *ProxyHandler) DeleteProxy(c *gin.Context) {
	id, err := parseProxyID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的代理 ID")
		return
	}

	if err := h.service.Delete(c.Request.Context(), id); err != nil {
		httpCode, message := h.handleError("删除代理失败", "删除失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, nil)
}

// TestProxy 测试代理连通性。
func (h *ProxyHandler) TestProxy(c *gin.Context) {
	id, err := parseProxyID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的代理 ID")
		return
	}

	result, err := h.service.Test(c.Request.Context(), id)
	if err != nil {
		httpCode, message := h.handleError("测试代理失败", "测试失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, toTestProxyRespFromDomain(result))
}
