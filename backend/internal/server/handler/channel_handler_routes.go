package handler

import (
	"errors"
	"io"

	"github.com/gin-gonic/gin"

	appchannel "github.com/DouDOU-start/airgate-core/internal/app/channel"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// ListChannels 分页列表渠道（keyword/type/status/tag/group_id 筛选）。
func (h *ChannelHandler) ListChannels(c *gin.Context) {
	var page dto.PageReq
	if err := c.ShouldBindQuery(&page); err != nil {
		response.BindError(c, err)
		return
	}

	result, err := h.service.List(c.Request.Context(), appchannel.ListFilter{
		Page:     page.Page,
		PageSize: page.PageSize,
		Keyword:  page.Keyword,
		Type:     c.Query("type"),
		Status:   c.Query("status"),
		Tag:      c.Query("tag"),
		GroupID:  parseOptionalInt(c.Query("group_id")),
		TZ:       c.Query("tz"),
	})
	if err != nil {
		httpCode, message := h.handleError("查询渠道列表失败", "查询失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	list := make([]dto.ChannelResp, 0, len(result.List))
	for _, item := range result.List {
		list = append(list, toChannelRespFromDomain(item))
	}
	response.Success(c, response.PagedData(list, result.Total, result.Page, result.PageSize))
}

// CreateChannel 创建渠道。
func (h *ChannelHandler) CreateChannel(c *gin.Context) {
	var req dto.CreateChannelReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	item, err := h.service.Create(c.Request.Context(), appchannel.CreateInput{
		Name:    req.Name,
		BaseURL: req.BaseURL,
	})
	if err != nil {
		httpCode, message := h.handleError("创建渠道失败", "创建失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, toChannelRespFromDomain(item))
}

// UpdateChannel 更新渠道（partial，仅 name/base_url）。
func (h *ChannelHandler) UpdateChannel(c *gin.Context) {
	id, err := ParseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的渠道 ID")
		return
	}

	var req dto.UpdateChannelReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	item, err := h.service.Update(c.Request.Context(), id, appchannel.UpdateInput{
		Name:    req.Name,
		BaseURL: req.BaseURL,
	})
	if err != nil {
		httpCode, message := h.handleError("更新渠道失败", "更新失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, toChannelRespFromDomain(item))
}

// AddChannelKey 在指定渠道下新增一把 key。
func (h *ChannelHandler) AddChannelKey(c *gin.Context) {
	channelID, err := ParseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的渠道 ID")
		return
	}

	var req dto.ChannelKeyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}
	if req.Type == "" {
		response.BadRequest(c, "缺少密钥类型")
		return
	}

	item, err := h.service.AddKey(c.Request.Context(), channelID, toKeyInput(req))
	if err != nil {
		httpCode, message := h.handleError("新增密钥失败", "新增失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, toChannelKeyResp(item))
}

// UpdateChannelKey 单把密钥端点 partial 更新（模型/映射弹窗、单 key 编辑用）。
func (h *ChannelHandler) UpdateChannelKey(c *gin.Context) {
	keyID, err := ParseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的密钥端点 ID")
		return
	}

	var req dto.ChannelKeyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	item, err := h.service.UpdateKey(c.Request.Context(), keyID, toKeyInput(req))
	if err != nil {
		httpCode, message := h.handleError("更新密钥端点失败", "更新失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, toChannelKeyResp(item))
}

// DeleteChannelKey 删除一把 key。
func (h *ChannelHandler) DeleteChannelKey(c *gin.Context) {
	keyID, err := ParseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的密钥端点 ID")
		return
	}

	if err := h.service.DeleteKey(c.Request.Context(), keyID); err != nil {
		httpCode, message := h.handleError("删除密钥失败", "删除失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, nil)
}

// DeleteChannel 删除渠道。
func (h *ChannelHandler) DeleteChannel(c *gin.Context) {
	id, err := ParseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的渠道 ID")
		return
	}

	if err := h.service.Delete(c.Request.Context(), id); err != nil {
		httpCode, message := h.handleError("删除渠道失败", "删除失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, nil)
}

// TestChannel 测试渠道连通性（body 可省略；model 缺省时取渠道 test_model 或首个模型）。
func (h *ChannelHandler) TestChannel(c *gin.Context) {
	id, err := ParseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的渠道 ID")
		return
	}

	var req dto.TestChannelReq
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		response.BindError(c, err)
		return
	}

	latencyMs, err := h.service.Test(c.Request.Context(), id, req.Model, req.Endpoint)
	if err != nil {
		httpCode, message := h.handleError("测试渠道失败", "测试失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, dto.TestChannelResp{
		LatencyMs: latencyMs,
		Message:   "测试通过",
	})
}

// FetchChannelModels 从上游拉取模型列表。
func (h *ChannelHandler) FetchChannelModels(c *gin.Context) {
	id, err := ParseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的渠道 ID")
		return
	}

	models, err := h.service.FetchModels(c.Request.Context(), id)
	if err != nil {
		httpCode, message := h.handleError("拉取渠道模型失败", "拉取失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, dto.FetchChannelModelsResp{Models: models})
}

// RefreshChannelBalance 查询指定 key 的上游余额并落库（仅 openai_compatible 中转站可查）。
func (h *ChannelHandler) RefreshChannelBalance(c *gin.Context) {
	keyID, err := ParseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的密钥端点 ID")
		return
	}

	balance, updatedAt, err := h.service.RefreshBalance(c.Request.Context(), keyID)
	if err != nil {
		httpCode, message := h.handleError("刷新渠道余额失败", "刷新失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, dto.RefreshChannelBalanceResp{
		Balance:          balance,
		BalanceUpdatedAt: updatedAt,
	})
}

// FetchChannelModelsPreview 按表单连接参数预览拉取模型列表（渠道未保存时使用）。
func (h *ChannelHandler) FetchChannelModelsPreview(c *gin.Context) {
	var req dto.FetchChannelModelsPreviewReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	models, err := h.service.FetchModelsWithKey(c.Request.Context(), req.Type, req.BaseURL, req.APIKey)
	if err != nil {
		httpCode, message := h.handleError("预览拉取渠道模型失败", "拉取失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, dto.FetchChannelModelsResp{Models: models})
}

// BulkUpdateChannels 批量启停/删除/调优先级。
func (h *ChannelHandler) BulkUpdateChannels(c *gin.Context) {
	var req dto.BulkUpdateChannelsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	affected, err := h.service.BulkUpdate(c.Request.Context(), appchannel.BulkUpdateInput{
		IDs:      req.IDs,
		Action:   req.Action,
		Priority: req.Priority,
	})
	if err != nil {
		httpCode, message := h.handleError("批量更新渠道失败", "批量更新失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, dto.BulkUpdateChannelsResp{Affected: affected})
}
