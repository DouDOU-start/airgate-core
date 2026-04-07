package handler

import (
	"time"

	"github.com/gin-gonic/gin"

	appuser "github.com/DouDOU-start/airgate-core/internal/app/user"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// GetMe 获取当前登录用户信息。
func (h *UserHandler) GetMe(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}

	item, err := h.service.Get(c.Request.Context(), userID)
	if err != nil {
		httpCode, message := h.handleError("查询当前用户失败", "查询失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	resp := toUserRespFromDomain(item)

	// API Key 登录场景：附带 Key 信息（名称、额度、到期时间）
	if apiKeyID, exists := c.Get(middleware.CtxKeyAPIKeyID); exists {
		if id, ok := apiKeyID.(int); ok && id > 0 {
			resp.APIKeyID = int64(id)
			if info, err := h.service.GetAPIKeyInfo(c.Request.Context(), id); err == nil {
				resp.APIKeyName = info.Name
				resp.APIKeyQuotaUSD = info.QuotaUSD
				resp.APIKeyUsedQuota = info.UsedQuota
				if info.ExpiresAt != nil {
					resp.APIKeyExpiresAt = info.ExpiresAt.Format(time.RFC3339)
				}
			}
		}
	}

	response.Success(c, resp)
}

// UpdateProfile 更新当前用户资料。
func (h *UserHandler) UpdateProfile(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}

	var req dto.UpdateProfileReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	item, err := h.service.UpdateProfile(c.Request.Context(), userID, req.Username)
	if err != nil {
		httpCode, message := h.handleError("更新用户资料失败", "更新失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, toUserRespFromDomain(item))
}

// UpdateBalanceAlert 更新余额预警设置。
func (h *UserHandler) UpdateBalanceAlert(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}

	var req struct {
		Threshold float64 `json:"threshold"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	if err := h.service.UpdateBalanceAlert(c.Request.Context(), userID, req.Threshold); err != nil {
		response.InternalError(c, "更新余额预警失败")
		return
	}
	response.Success(c, nil)
}

// ChangePassword 修改当前用户密码。
func (h *UserHandler) ChangePassword(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}

	var req dto.ChangePasswordReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	if err := h.service.ChangePassword(c.Request.Context(), userID, req.OldPassword, req.NewPassword); err != nil {
		httpCode, message := h.handleError("修改用户密码失败", "密码修改失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, nil)
}

// ListUsers 管理员查询用户列表。
func (h *UserHandler) ListUsers(c *gin.Context) {
	var page dto.PageReq
	if err := c.ShouldBindQuery(&page); err != nil {
		response.BindError(c, err)
		return
	}

	result, err := h.service.List(c.Request.Context(), appuser.ListFilter{
		Page:     page.Page,
		PageSize: page.PageSize,
		Keyword:  page.Keyword,
		Status:   c.Query("status"),
		Role:     c.Query("role"),
	})
	if err != nil {
		httpCode, message := h.handleError("查询用户列表失败", "查询失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	list := make([]dto.UserResp, 0, len(result.List))
	for _, item := range result.List {
		list = append(list, toUserRespFromDomain(item))
	}
	response.Success(c, response.PagedData(list, result.Total, result.Page, result.PageSize))
}

// CreateUser 管理员创建用户。
func (h *UserHandler) CreateUser(c *gin.Context) {
	var req dto.CreateUserReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	item, err := h.service.Create(c.Request.Context(), appuser.CreateInput{
		Email:          req.Email,
		Password:       req.Password,
		Username:       req.Username,
		Role:           req.Role,
		MaxConcurrency: req.MaxConcurrency,
		GroupRates:     req.GroupRates,
	})
	if err != nil {
		httpCode, message := h.handleError("创建用户失败", "创建失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, toUserRespFromDomain(item))
}

// UpdateUser 管理员更新用户。
func (h *UserHandler) UpdateUser(c *gin.Context) {
	id, err := parseUserID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的用户 ID")
		return
	}

	var req dto.UpdateUserReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	item, err := h.service.Update(c.Request.Context(), id, appuser.UpdateInput{
		Username:           req.Username,
		Password:           req.Password,
		Role:               req.Role,
		MaxConcurrency:     req.MaxConcurrency,
		GroupRates:         req.GroupRates,
		HasGroupRates:      req.GroupRates != nil,
		AllowedGroupIDs:    derefInt64Slice(req.AllowedGroupIDs),
		HasAllowedGroupIDs: req.AllowedGroupIDs != nil,
		Status:             req.Status,
	})
	if err != nil {
		httpCode, message := h.handleError("更新用户失败", "更新失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, toUserRespFromDomain(item))
}

// AdjustBalance 管理员调整用户余额。
func (h *UserHandler) AdjustBalance(c *gin.Context) {
	id, err := parseUserID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的用户 ID")
		return
	}

	var req dto.AdjustBalanceReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	item, err := h.service.AdjustBalance(c.Request.Context(), id, appuser.BalanceChange{
		Action: req.Action,
		Amount: req.Amount,
		Remark: req.Remark,
	})
	if err != nil {
		httpCode, message := h.handleError("调整用户余额失败", "调整余额失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, toUserRespFromDomain(item))
}

// DeleteUser 管理员删除用户。
func (h *UserHandler) DeleteUser(c *gin.Context) {
	id, err := parseUserID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的用户 ID")
		return
	}

	if err := h.service.Delete(c.Request.Context(), id); err != nil {
		httpCode, message := h.handleError("删除用户失败", "删除失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, nil)
}

// ToggleUserStatus 切换用户状态。
func (h *UserHandler) ToggleUserStatus(c *gin.Context) {
	id, err := parseUserID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的用户 ID")
		return
	}

	result, err := h.service.ToggleStatus(c.Request.Context(), id)
	if err != nil {
		httpCode, message := h.handleError("切换用户状态失败", "操作失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}
	response.Success(c, map[string]any{
		"id":     result.ID,
		"status": result.Status,
	})
}

// GetUserBalanceHistory 查询用户余额变更历史。
func (h *UserHandler) GetUserBalanceHistory(c *gin.Context) {
	id, err := parseUserID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的用户 ID")
		return
	}

	var page dto.PageReq
	if err := c.ShouldBindQuery(&page); err != nil {
		response.BindError(c, err)
		return
	}

	result, err := h.service.ListBalanceLogs(c.Request.Context(), id, page.Page, page.PageSize)
	if err != nil {
		httpCode, message := h.handleError("查询用户余额日志失败", "查询失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	list := make([]dto.BalanceLogResp, 0, len(result.List))
	for _, item := range result.List {
		list = append(list, toBalanceLogResp(item))
	}
	response.Success(c, response.PagedData(list, result.Total, result.Page, result.PageSize))
}

// AdminListUserKeys 管理员查询指定用户的 API 密钥列表。
func (h *UserHandler) AdminListUserKeys(c *gin.Context) {
	id, err := parseUserID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的用户 ID")
		return
	}

	var page dto.PageReq
	if err := c.ShouldBindQuery(&page); err != nil {
		response.BindError(c, err)
		return
	}

	result, err := h.service.ListAPIKeys(c.Request.Context(), id, page.Page, page.PageSize)
	if err != nil {
		httpCode, message := h.handleError("查询用户密钥失败", "查询失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	list := make([]dto.APIKeyResp, 0, len(result.List))
	for _, item := range result.List {
		list = append(list, toAPIKeyRespFromUserDomain(item, id))
	}
	response.Success(c, response.PagedData(list, result.Total, result.Page, result.PageSize))
}

func derefInt64Slice(input *[]int64) []int64 {
	if input == nil {
		return nil
	}
	return append([]int64(nil), (*input)...)
}
