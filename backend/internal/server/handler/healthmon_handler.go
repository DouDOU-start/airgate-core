package handler

import (
	"errors"
	"log/slog"
	"strings"

	"github.com/gin-gonic/gin"

	apphealthmon "github.com/DouDOU-start/airgate-core/internal/app/healthmon"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// HealthmonHandler 健康监测 Handler（管理员只读）。
type HealthmonHandler struct {
	service *apphealthmon.Service
}

// NewHealthmonHandler 创建 HealthmonHandler。
func NewHealthmonHandler(service *apphealthmon.Service) *HealthmonHandler {
	return &HealthmonHandler{service: service}
}

// Overview GET /admin/health-monitor/overview
func (h *HealthmonHandler) Overview(c *gin.Context) {
	var query dto.HealthmonOverviewQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		response.BindError(c, err)
		return
	}
	out, err := h.service.Overview(c.Request.Context(), query.Window)
	if err != nil {
		slog.Error("healthmon_overview_failed", "error", err)
		response.InternalError(c, "查询失败")
		return
	}
	response.Success(c, toHealthmonOverviewResp(out))
}

// Entities GET /admin/health-monitor/entities
func (h *HealthmonHandler) Entities(c *gin.Context) {
	var query dto.HealthmonEntitiesQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		response.BindError(c, err)
		return
	}
	scope := strings.TrimSpace(query.Scope)
	if scope == "" {
		scope = "channel_key"
	}
	window, _ := apphealthmon.ParseWindow(query.Window)
	filter := apphealthmon.ListFilter{
		Window:        window,
		OnlyUnhealthy: query.OnlyUnhealthy,
		IDs:           parseIDList(query.IDs),
	}
	var (
		rows []apphealthmon.EntityRow
		err  error
	)
	switch scope {
	case "channel_key":
		rows, err = h.service.ListChannelKeys(c.Request.Context(), filter)
	case "group":
		rows, err = h.service.ListGroups(c.Request.Context(), filter)
	default:
		response.BadRequest(c, "scope 仅支持 channel_key 或 group")
		return
	}
	if err != nil {
		slog.Error("healthmon_entities_failed", "scope", scope, "error", err)
		response.InternalError(c, "查询失败")
		return
	}
	list := make([]dto.HealthmonEntityResp, 0, len(rows))
	for _, row := range rows {
		list = append(list, toHealthmonEntityResp(row))
	}
	response.Success(c, list)
}

// UserOverview GET /channel-status/overview。
func (h *HealthmonHandler) UserOverview(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok || userID <= 0 {
		response.Unauthorized(c, "用户未认证")
		return
	}
	var query dto.HealthmonOverviewQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		response.BindError(c, err)
		return
	}
	out, err := h.service.UserOverview(c.Request.Context(), userID, query.Window)
	if err != nil {
		if errors.Is(err, apphealthmon.ErrChannelStatusDisabled) {
			response.Forbidden(c, err.Error())
			return
		}
		slog.Error("channel_status_overview_failed", "user_id", userID, "error", err)
		response.InternalError(c, "查询失败")
		return
	}
	response.Success(c, toChannelStatusOverviewResp(out))
}

// UserGroups GET /channel-status/groups。
func (h *HealthmonHandler) UserGroups(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok || userID <= 0 {
		response.Unauthorized(c, "用户未认证")
		return
	}
	var query dto.HealthmonOverviewQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		response.BindError(c, err)
		return
	}
	rows, err := h.service.ListUserGroups(c.Request.Context(), userID, query.Window)
	if err != nil {
		if errors.Is(err, apphealthmon.ErrChannelStatusDisabled) {
			response.Forbidden(c, err.Error())
			return
		}
		slog.Error("channel_status_groups_failed", "user_id", userID, "error", err)
		response.InternalError(c, "查询失败")
		return
	}
	list := make([]dto.ChannelStatusGroupResp, 0, len(rows))
	for _, row := range rows {
		list = append(list, toChannelStatusGroupResp(row))
	}
	response.Success(c, list)
}
