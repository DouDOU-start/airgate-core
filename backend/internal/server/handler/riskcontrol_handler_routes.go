package handler

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	appriskcontrol "github.com/DouDOU-start/airgate-core/internal/app/riskcontrol"
	"github.com/DouDOU-start/airgate-core/internal/moderation"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// GetConfig GET /admin/risk-control/config 配置回显。
func (h *RiskControlHandler) GetConfig(c *gin.Context) {
	view, err := h.service.GetConfig(c.Request.Context())
	if err != nil {
		httpCode, message := h.handleError("查询风控配置失败", "查询失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, toRiskControlConfigResp(view))
}

// UpdateConfig PUT /admin/risk-control/config 增量更新配置。
func (h *RiskControlHandler) UpdateConfig(c *gin.Context) {
	var req dto.UpdateRiskControlConfigReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}
	view, err := h.service.UpdateConfig(c.Request.Context(), toRiskControlUpdateInput(req))
	if err != nil {
		httpCode, message := h.handleError("更新风控配置失败", "更新失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, toRiskControlConfigResp(view))
}

// GetStatus GET /admin/risk-control/status 运行时状态。
func (h *RiskControlHandler) GetStatus(c *gin.Context) {
	response.Success(c, h.service.GetStatus(c.Request.Context()))
}

// TestAPIKeys POST /admin/risk-control/api-keys/test 探活/试审。
func (h *RiskControlHandler) TestAPIKeys(c *gin.Context) {
	var req dto.TestRiskControlKeysReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}
	result, err := h.service.TestAPIKeys(c.Request.Context(), moderation.TestKeysInput{
		APIKeys:   req.APIKeys,
		BaseURL:   req.BaseURL,
		Model:     req.Model,
		TimeoutMS: req.TimeoutMS,
		Prompt:    req.Prompt,
		Images:    req.Images,
	})
	if err != nil {
		httpCode, message := h.handleError("审核 key 探活失败", "测试失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, result)
}

// ListLogs GET /admin/risk-control/logs 审核日志分页。
func (h *RiskControlHandler) ListLogs(c *gin.Context) {
	var query dto.RiskControlLogsQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		response.BindError(c, err)
		return
	}
	filter := appriskcontrol.ListFilter{
		Page:     query.Page,
		PageSize: query.PageSize,
		Result:   query.Result,
		GroupID:  parseOptionalInt(query.GroupID),
		Endpoint: query.Endpoint,
		Search:   query.Search,
		From:     parseOptionalTime(query.From),
		To:       parseOptionalTime(query.To),
	}
	items, total, err := h.service.ListLogs(c.Request.Context(), filter)
	if err != nil {
		httpCode, message := h.handleError("查询审核日志失败", "查询失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	list := make([]dto.RiskControlLogResp, 0, len(items))
	list = append(list, items...)
	response.Success(c, response.PagedData(list, total, filter.Page, filter.PageSize))
}

// UnbanUser POST /admin/risk-control/users/:id/unban 解封用户。
func (h *RiskControlHandler) UnbanUser(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		response.Error(c, 400, 400, "用户 ID 无效")
		return
	}
	result, err := h.service.UnbanUser(c.Request.Context(), id)
	if err != nil {
		httpCode, message := h.handleError("解封用户失败", "解封失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, dto.RiskControlUnbanResp{UserID: result.UserID, Status: result.Status})
}

// DeleteHash DELETE /admin/risk-control/hashes 删除单条命中哈希。
func (h *RiskControlHandler) DeleteHash(c *gin.Context) {
	var req dto.DeleteRiskControlHashReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}
	deleted, err := h.service.DeleteFlaggedHash(c.Request.Context(), req.InputHash)
	if err != nil {
		httpCode, message := h.handleError("删除命中哈希失败", "删除失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, dto.DeleteRiskControlHashResp{InputHash: req.InputHash, Deleted: deleted})
}

// ClearLogs DELETE /admin/risk-control/logs 清空审核日志（可按 result 类型筛选）。
func (h *RiskControlHandler) ClearLogs(c *gin.Context) {
	var req dto.ClearRiskControlLogsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}
	deleted, err := h.service.ClearLogs(c.Request.Context(), req.Result)
	if err != nil {
		httpCode, message := h.handleError("清空审核日志失败", "清空失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, dto.ClearRiskControlLogsResp{Deleted: deleted, Result: req.Result})
}

// ClearHashes DELETE /admin/risk-control/hashes/all 清空命中哈希缓存。
func (h *RiskControlHandler) ClearHashes(c *gin.Context) {
	deleted, err := h.service.ClearFlaggedHashes(c.Request.Context())
	if err != nil {
		httpCode, message := h.handleError("清空命中哈希失败", "清空失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, dto.ClearRiskControlHashesResp{Deleted: deleted})
}

// parseOptionalTime RFC3339 时间参数解析；空串或非法返回 nil。
func parseOptionalTime(value string) *time.Time {
	if value == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil
	}
	return &t
}
