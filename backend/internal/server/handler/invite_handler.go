package handler

import (
	"errors"
	"log/slog"

	"github.com/gin-gonic/gin"

	appinvite "github.com/DouDOU-start/airgate-core/internal/app/invite"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// InviteHandler 邀请返利域 HTTP 处理器（用户端 + 管理端）。
type InviteHandler struct {
	service *appinvite.Service
}

// NewInviteHandler 创建邀请返利处理器。
func NewInviteHandler(service *appinvite.Service) *InviteHandler {
	return &InviteHandler{service: service}
}

func toInviteeResp(r appinvite.InviteRelation) dto.InviteeResp {
	return dto.InviteeResp{
		InviterID:       r.InviterID,
		InviterEmail:    r.InviterEmail,
		InviterUsername: r.InviterUsername,
		InviteeID:       r.InviteeID,
		InviteeEmail:    r.InviteeEmail,
		InviteeUsername: r.InviteeUsername,
		CreatedAt:       r.CreatedAt,
		TotalRebate:     r.TotalRebate,
	}
}

func toRebateLogResp(l appinvite.RebateLog) dto.InviteRebateLogResp {
	return dto.InviteRebateLogResp{
		ID:              l.ID,
		UserID:          l.UserID,
		UserEmail:       l.UserEmail,
		Action:          l.Action,
		Amount:          l.Amount,
		SourceUserID:    l.SourceUserID,
		SourceUserEmail: l.SourceUserEmail,
		SourceOrderNo:   l.SourceOrderNo,
		BalanceAfter:    l.BalanceAfter,
		CreatedAt:       l.CreatedAt,
	}
}

func toOverrideEntryResp(e appinvite.OverrideEntry) dto.InviteOverrideEntryResp {
	return dto.InviteOverrideEntryResp{
		UserID:       e.UserID,
		Email:        e.Email,
		Username:     e.Username,
		InviteCode:   e.InviteCode,
		RatePercent:  e.RebateRateOverride,
		InvitedCount: e.InvitedCount,
	}
}

// ===================== 用户端 =====================

// GetMe 我的邀请信息（邀请码/生效比例/邀请人数/返利余额）。
func (h *InviteHandler) GetMe(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}
	info, err := h.service.GetMyInfo(c.Request.Context(), userID)
	if err != nil {
		slog.Error("查询邀请信息失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}
	response.Success(c, dto.InviteMeResp{
		Enabled:              info.Enabled,
		InviteCode:           info.InviteCode,
		InviterID:            info.InviterID,
		EffectiveRatePercent: info.EffectiveRatePercent,
		InvitedCount:         info.InvitedCount,
		RebateBalance:        info.RebateBalance,
		RebateTotal:          info.RebateTotal,
		Description:          info.Description,
	})
}

// ListMyInvitees 我邀请的人（分页）。
func (h *InviteHandler) ListMyInvitees(c *gin.Context) {
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
	list, total, err := h.service.ListMyInvitees(c.Request.Context(), userID, appinvite.ListFilter{
		Page: page.Page, PageSize: page.PageSize,
	})
	if err != nil {
		slog.Error("查询我的邀请记录失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}
	items := make([]dto.InviteeResp, 0, len(list))
	for _, item := range list {
		items = append(items, toInviteeResp(item))
	}
	response.Success(c, response.PagedData(items, total, page.Page, page.PageSize))
}

// ListMyRebateLogs 我的返利流水（分页）。
func (h *InviteHandler) ListMyRebateLogs(c *gin.Context) {
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
	list, total, err := h.service.ListMyRebateLogs(c.Request.Context(), userID, appinvite.ListFilter{
		Page: page.Page, PageSize: page.PageSize,
	})
	if err != nil {
		slog.Error("查询我的返利流水失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}
	items := make([]dto.InviteRebateLogResp, 0, len(list))
	for _, item := range list {
		items = append(items, toRebateLogResp(item))
	}
	response.Success(c, response.PagedData(items, total, page.Page, page.PageSize))
}

// Transfer 把返利余额转入可消费余额。
func (h *InviteHandler) Transfer(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}
	transferred, balance, err := h.service.TransferToBalance(c.Request.Context(), userID)
	if err != nil {
		h.respondInviteError(c, "返利转入余额失败", err)
		return
	}
	response.Success(c, dto.InviteTransferResp{Transferred: transferred, Balance: balance})
}

// ===================== 管理端 =====================

// AdminListOverrides 有专属比例覆盖的用户列表。
func (h *InviteHandler) AdminListOverrides(c *gin.Context) {
	var page dto.PageReq
	if err := c.ShouldBindQuery(&page); err != nil {
		response.BindError(c, err)
		return
	}
	list, total, err := h.service.AdminListOverrides(c.Request.Context(), appinvite.ListFilter{
		Page: page.Page, PageSize: page.PageSize, Keyword: page.Keyword,
	})
	if err != nil {
		slog.Error("查询邀请专属比例列表失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}
	items := make([]dto.InviteOverrideEntryResp, 0, len(list))
	for _, item := range list {
		items = append(items, toOverrideEntryResp(item))
	}
	response.Success(c, response.PagedData(items, total, page.Page, page.PageSize))
}

// AdminSetOverride 设置/清除用户专属返利比例。
func (h *InviteHandler) AdminSetOverride(c *gin.Context) {
	userID, err := ParseID(c.Param("userID"))
	if err != nil {
		response.BadRequest(c, "无效的用户 ID")
		return
	}
	var req dto.SetInviteRateOverrideReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}
	if err := h.service.AdminSetRateOverride(c.Request.Context(), userID, req.RatePercent); err != nil {
		h.respondInviteError(c, "设置专属返利比例失败", err)
		return
	}
	response.Success(c, gin.H{"user_id": userID})
}

// AdminListInvitees 全量邀请关系（分页）。
func (h *InviteHandler) AdminListInvitees(c *gin.Context) {
	var page dto.PageReq
	if err := c.ShouldBindQuery(&page); err != nil {
		response.BindError(c, err)
		return
	}
	list, total, err := h.service.AdminListInvitees(c.Request.Context(), appinvite.ListFilter{
		Page: page.Page, PageSize: page.PageSize, Keyword: page.Keyword,
	})
	if err != nil {
		slog.Error("查询全量邀请关系失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}
	items := make([]dto.InviteeResp, 0, len(list))
	for _, item := range list {
		items = append(items, toInviteeResp(item))
	}
	response.Success(c, response.PagedData(items, total, page.Page, page.PageSize))
}

// AdminListRebateLogs 全量返利流水（分页）。
func (h *InviteHandler) AdminListRebateLogs(c *gin.Context) {
	var page dto.PageReq
	if err := c.ShouldBindQuery(&page); err != nil {
		response.BindError(c, err)
		return
	}
	list, total, err := h.service.AdminListRebateLogs(c.Request.Context(), appinvite.ListFilter{
		Page: page.Page, PageSize: page.PageSize, Keyword: page.Keyword,
	})
	if err != nil {
		slog.Error("查询全量返利流水失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}
	items := make([]dto.InviteRebateLogResp, 0, len(list))
	for _, item := range list {
		items = append(items, toRebateLogResp(item))
	}
	response.Success(c, response.PagedData(items, total, page.Page, page.PageSize))
}

// respondInviteError 域错误 → HTTP 响应（业务类 4xx，系统类 500 且不外泄细节）。
func (h *InviteHandler) respondInviteError(c *gin.Context, logMessage string, err error) {
	switch {
	case errors.Is(err, appinvite.ErrCodeInvalid),
		errors.Is(err, appinvite.ErrSelfInvite),
		errors.Is(err, appinvite.ErrAlreadyBound),
		errors.Is(err, appinvite.ErrRebateBalanceEmpty),
		errors.Is(err, appinvite.ErrInvalidRate):
		response.BadRequest(c, err.Error())
	case errors.Is(err, appinvite.ErrProfileNotFound), errors.Is(err, appinvite.ErrUserNotFound):
		response.NotFound(c, err.Error())
	default:
		slog.Error(logMessage, "error", err)
		response.InternalError(c, "操作失败")
	}
}
