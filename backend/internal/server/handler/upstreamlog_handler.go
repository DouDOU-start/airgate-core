package handler

import (
	"log/slog"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	appupstreamlog "github.com/DouDOU-start/airgate-core/internal/app/upstreamlog"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// UpstreamLogHandler 失败请求留痕（用户转发失败 + 渠道测试失败）查询 Handler。
type UpstreamLogHandler struct {
	service *appupstreamlog.Service
}

// NewUpstreamLogHandler 创建 UpstreamLogHandler。
func NewUpstreamLogHandler(service *appupstreamlog.Service) *UpstreamLogHandler {
	return &UpstreamLogHandler{service: service}
}

// AdminList 管理员分页查询上游请求日志。
func (h *UpstreamLogHandler) AdminList(c *gin.Context) {
	var query dto.UpstreamLogQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		response.BindError(c, err)
		return
	}

	result, err := h.service.List(c.Request.Context(), appupstreamlog.ListFilter{
		Page:      query.Page,
		PageSize:  query.PageSize,
		Source:    query.Source,
		Phase:     query.Phase,
		UserID:    query.UserID,
		APIKeyID:  query.APIKeyID,
		ChannelID: query.ChannelID,
		RequestID: query.RequestID,
		Model:     query.Model,
		StartDate: query.StartDate,
		EndDate:   query.EndDate,
		TZ:        c.Query("tz"),
	})
	if err != nil {
		slog.Error("查询上游请求日志失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}

	list := make([]dto.UpstreamLogResp, 0, len(result.List))
	for _, item := range result.List {
		list = append(list, toUpstreamLogResp(item))
	}
	response.Success(c, response.PagedData(list, result.Total, result.Page, result.PageSize))
}

// UserList 用户查询自己的失败请求（脱敏：不含渠道/重试链/IP/UA）。
// API Key 登录（customer scope）时强制只看该 Key 的记录。
func (h *UpstreamLogHandler) UserList(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}
	var query dto.UpstreamLogQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		response.BindError(c, err)
		return
	}

	uid := int64(userID)
	apiKeyFilter := query.APIKeyID
	if scopedKey := scopedAPIKeyID(c); scopedKey > 0 {
		apiKeyFilter = &scopedKey
	}

	result, err := h.service.List(c.Request.Context(), appupstreamlog.ListFilter{
		Page:     query.Page,
		PageSize: query.PageSize,
		// 用户视角只看转发请求；channel_test 属管理员操作（本就无用户归属，双保险）。
		Source:    "relay",
		Phase:     query.Phase,
		UserID:    &uid,
		APIKeyID:  apiKeyFilter,
		RequestID: query.RequestID,
		Model:     query.Model,
		StartDate: query.StartDate,
		EndDate:   query.EndDate,
		TZ:        c.Query("tz"),
	})
	if err != nil {
		slog.Error("查询用户失败请求失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}

	list := make([]dto.UserUpstreamLogResp, 0, len(result.List))
	for _, item := range result.List {
		list = append(list, dto.UserUpstreamLogResp{
			ID:          item.ID,
			RequestID:   item.RequestID,
			Phase:       item.Phase,
			StatusCode:  item.StatusCode,
			ErrorType:   item.ErrorType,
			ErrorCode:   item.ErrorCode,
			Message:     item.Message,
			Attempts:    item.Attempts,
			Billed:      item.Billed,
			Model:       item.Model,
			Endpoint:    item.Endpoint,
			Stream:      item.Stream,
			APIKeyID:    item.APIKeyID,
			DurationMs:  item.DurationMs,
			RepeatCount: item.RepeatCount,
			CreatedAt:   item.CreatedAt,
		})
	}
	response.Success(c, response.PagedData(list, result.Total, result.Page, result.PageSize))
}

// 渠道失败计数窗口：默认/上限（分钟）。上限与 errlog 桶 TTL 对齐。
const (
	defaultFailureWindowMinutes = 30
	maxFailureWindowMinutes     = 60
	maxFailureStatsChannels     = 500
)

// ChannelFailureStats 渠道近 N 分钟失败计数（Redis 分钟桶，渠道页监控列）。
func (h *UpstreamLogHandler) ChannelFailureStats(c *gin.Context) {
	var query dto.ChannelFailureStatsQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		response.BindError(c, err)
		return
	}

	ids := make([]int, 0, 32)
	for _, part := range strings.Split(query.IDs, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.Atoi(part)
		if err != nil || id <= 0 {
			response.BadRequest(c, "ids 参数非法")
			return
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 || len(ids) > maxFailureStatsChannels {
		response.BadRequest(c, "ids 数量非法")
		return
	}
	minutes := query.Minutes
	if minutes <= 0 {
		minutes = defaultFailureWindowMinutes
	}
	if minutes > maxFailureWindowMinutes {
		minutes = maxFailureWindowMinutes
	}

	counts, err := h.service.ChannelFailureStats(c.Request.Context(), ids, minutes)
	if err != nil {
		slog.Error("查询渠道失败计数失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}

	resp := dto.ChannelFailureStatsResp{Minutes: minutes, Channels: make([]dto.ChannelFailureCounts, 0, len(ids))}
	for _, id := range ids {
		byVerdict := counts[id]
		var total int64
		for _, n := range byVerdict {
			total += n
		}
		if byVerdict == nil {
			byVerdict = map[string]int64{}
		}
		resp.Channels = append(resp.Channels, dto.ChannelFailureCounts{ChannelID: id, Total: total, ByVerdict: byVerdict})
	}
	response.Success(c, resp)
}

func toUpstreamLogResp(item appupstreamlog.Record) dto.UpstreamLogResp {
	return dto.UpstreamLogResp{
		ID:           item.ID,
		RequestID:    item.RequestID,
		Source:       item.Source,
		Phase:        item.Phase,
		StatusCode:   item.StatusCode,
		ErrorType:    item.ErrorType,
		ErrorCode:    item.ErrorCode,
		Message:      item.Message,
		Attempts:     item.Attempts,
		AttemptChain: item.AttemptChain,
		Billed:       item.Billed,
		Model:        item.Model,
		Endpoint:     item.Endpoint,
		Stream:       item.Stream,
		UserID:       item.UserID,
		UserEmail:    item.UserEmail,
		APIKeyID:     item.APIKeyID,
		GroupID:      item.GroupID,
		ChannelID:    item.ChannelID,
		ChannelName:  item.ChannelName,
		IPAddress:    item.IPAddress,
		UserAgent:    item.UserAgent,
		DurationMs:   item.DurationMs,
		RepeatCount:  item.RepeatCount,
		CreatedAt:    item.CreatedAt,
	}
}
