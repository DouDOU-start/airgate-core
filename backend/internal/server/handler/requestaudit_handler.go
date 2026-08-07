package handler

import (
	"log/slog"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/internal/requestaudit"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// RequestAuditHandler 提供仅管理员可访问的完整请求审计查询。
type RequestAuditHandler struct {
	service *requestaudit.Service
}

// NewRequestAuditHandler 创建完整请求审计处理器。
func NewRequestAuditHandler(service *requestaudit.Service) *RequestAuditHandler {
	return &RequestAuditHandler{service: service}
}

type requestAuditQuery struct {
	Page       int    `form:"page" binding:"omitempty,min=1"`
	PageSize   int    `form:"page_size" binding:"omitempty,min=1,max=100"`
	Keyword    string `form:"keyword"`
	Model      string `form:"model"`
	StatusCode int    `form:"status_code" binding:"omitempty,min=100,max=599"`
	AccountID  int    `form:"account_id" binding:"omitempty,min=1"`
	ChannelID  int    `form:"channel_id" binding:"omitempty,min=1"`
	Start      string `form:"start"`
	End        string `form:"end"`
}

// List 分页查询审计记录，不在列表接口解密大字段。
func (h *RequestAuditHandler) List(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	var query requestAuditQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		response.BindError(c, err)
		return
	}
	start, ok := parseAuditTime(c, query.Start, "start")
	if !ok {
		return
	}
	end, ok := parseAuditTime(c, query.End, "end")
	if !ok {
		return
	}
	items, total, err := h.service.List(c.Request.Context(), requestaudit.ListFilter{
		Page: query.Page, PageSize: query.PageSize, Keyword: query.Keyword, Model: query.Model,
		StatusCode: query.StatusCode, AccountID: query.AccountID, ChannelID: query.ChannelID,
		Start: start, End: end,
	})
	if err != nil {
		slog.Error("查询完整请求审计失败", "error", err)
		response.InternalError(c, "查询请求审计失败")
		return
	}
	page := query.Page
	if page < 1 {
		page = 1
	}
	pageSize := query.PageSize
	if pageSize < 1 {
		pageSize = 20
	}
	response.Success(c, response.PagedData(items, int64(total), page, pageSize))
}

// Get 返回单条请求及全部实际上游尝试的解密详情。
func (h *RequestAuditHandler) Get(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		response.BadRequest(c, "审计记录 ID 非法")
		return
	}
	detail, err := h.service.Get(c.Request.Context(), id)
	if ent.IsNotFound(err) {
		response.NotFound(c, "审计记录不存在")
		return
	}
	if err != nil {
		slog.Error("读取完整请求审计详情失败", "audit_id", id, "error", err)
		response.InternalError(c, "读取请求审计详情失败")
		return
	}
	response.Success(c, detail)
}

func parseAuditTime(c *gin.Context, raw, field string) (*time.Time, bool) {
	if raw == "" {
		return nil, true
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		response.BadRequest(c, field+" 必须是 RFC3339 时间")
		return nil, false
	}
	return &value, true
}
