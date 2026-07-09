package handler

import (
	"errors"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	appredemption "github.com/DouDOU-start/airgate-core/internal/app/redemption"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// RedemptionHandler 兑换码域 HTTP 处理器（管理端生成/管理 + 用户端兑换）。
type RedemptionHandler struct {
	service *appredemption.Service
}

// NewRedemptionHandler 创建兑换码处理器。
func NewRedemptionHandler(service *appredemption.Service) *RedemptionHandler {
	return &RedemptionHandler{service: service}
}

func toRedemptionCodeResp(c appredemption.Code, now time.Time) dto.RedemptionCodeResp {
	status := c.Status
	if c.Expired(now) {
		status = appredemption.StatusExpired
	}
	resp := dto.RedemptionCodeResp{
		ID:          c.ID,
		Code:        c.Code,
		Value:       c.Value,
		Status:      status,
		Remark:      c.Remark,
		UsedByID:    c.UsedByID,
		UsedByEmail: c.UsedByEmail,
		UsedAt:      c.UsedAt,
		ExpiresAt:   c.ExpiresAt,
	}
	resp.CreatedAt = c.CreatedAt
	resp.UpdatedAt = c.UpdatedAt
	return resp
}

// ===================== 管理端 =====================

// AdminGenerateCodes 批量生成兑换码，返回明文码列表供复制导出。
func (h *RedemptionHandler) AdminGenerateCodes(c *gin.Context) {
	var req dto.GenerateRedemptionCodesReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}
	created, err := h.service.Generate(c.Request.Context(), appredemption.GenerateInput{
		Count:     req.Count,
		Value:     req.Value,
		Remark:    req.Remark,
		ExpiresAt: req.ExpiresAt,
	})
	if err != nil {
		if errors.Is(err, appredemption.ErrInvalidGenerateInput) {
			response.BadRequest(c, err.Error())
			return
		}
		slog.Error("生成兑换码失败", "error", err)
		response.InternalError(c, "生成失败")
		return
	}
	now := time.Now()
	list := make([]dto.RedemptionCodeResp, 0, len(created))
	for _, item := range created {
		list = append(list, toRedemptionCodeResp(item, now))
	}
	response.Success(c, list)
}

// AdminListCodes 分页列表（status 支持 unused/used/disabled/expired 虚拟状态）。
func (h *RedemptionHandler) AdminListCodes(c *gin.Context) {
	var page dto.PageReq
	if err := c.ShouldBindQuery(&page); err != nil {
		response.BindError(c, err)
		return
	}
	list, total, err := h.service.List(c.Request.Context(), appredemption.ListFilter{
		Page:     page.Page,
		PageSize: page.PageSize,
		Keyword:  page.Keyword,
		Status:   c.Query("status"),
	})
	if err != nil {
		slog.Error("查询兑换码列表失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}
	now := time.Now()
	items := make([]dto.RedemptionCodeResp, 0, len(list))
	for _, item := range list {
		items = append(items, toRedemptionCodeResp(item, now))
	}
	response.Success(c, response.PagedData(items, total, page.Page, page.PageSize))
}

// AdminStats 兑换码统计卡片。
func (h *RedemptionHandler) AdminStats(c *gin.Context) {
	stats, err := h.service.Stats(c.Request.Context())
	if err != nil {
		slog.Error("查询兑换码统计失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}
	response.Success(c, dto.RedemptionStatsResp{
		Total:       stats.Total,
		Unused:      stats.Unused,
		Used:        stats.Used,
		Disabled:    stats.Disabled,
		Expired:     stats.Expired,
		UsedValue:   stats.UsedValue,
		UnusedValue: stats.UnusedValue,
	})
}

// AdminUpdateStatus 停用/恢复兑换码。
func (h *RedemptionHandler) AdminUpdateStatus(c *gin.Context) {
	id, err := ParseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的兑换码 ID")
		return
	}
	var req dto.UpdateRedemptionCodeStatusReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}
	if err := h.service.SetDisabled(c.Request.Context(), id, *req.Disabled); err != nil {
		h.respondRedemptionError(c, "更新兑换码状态失败", err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// AdminDeleteCode 删除兑换码（已使用的码不可删除）。
func (h *RedemptionHandler) AdminDeleteCode(c *gin.Context) {
	id, err := ParseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的兑换码 ID")
		return
	}
	if err := h.service.Delete(c.Request.Context(), id); err != nil {
		h.respondRedemptionError(c, "删除兑换码失败", err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// ===================== 用户端 =====================

// Redeem 用户兑换：入账成功返回面值与最新余额。
func (h *RedemptionHandler) Redeem(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}
	var req dto.RedeemReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}
	result, err := h.service.Redeem(c.Request.Context(), userID, req.Code)
	if err != nil {
		h.respondRedemptionError(c, "兑换失败", err)
		return
	}
	response.Success(c, dto.RedeemResp{Value: result.Value, Balance: result.Balance})
}

// respondRedemptionError 域错误 → HTTP 响应（业务类 4xx，系统类 500 且不外泄细节）。
func (h *RedemptionHandler) respondRedemptionError(c *gin.Context, logMessage string, err error) {
	switch {
	case errors.Is(err, appredemption.ErrCodeNotFound):
		response.NotFound(c, err.Error())
	case errors.Is(err, appredemption.ErrCodeUsed),
		errors.Is(err, appredemption.ErrCodeDisabled),
		errors.Is(err, appredemption.ErrCodeExpired),
		errors.Is(err, appredemption.ErrCodeStateConflict),
		errors.Is(err, appredemption.ErrRedeemRateLimited),
		errors.Is(err, appredemption.ErrUserNotFound):
		response.BadRequest(c, err.Error())
	default:
		slog.Error(logMessage, "error", err)
		response.InternalError(c, "操作失败")
	}
}
