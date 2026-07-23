package handler

import (
	"github.com/gin-gonic/gin"

	appbookmark "github.com/DouDOU-start/airgate-core/internal/app/bookmark"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// ListBookmarks 查询备忘录列表。
func (h *BookmarkHandler) ListBookmarks(c *gin.Context) {
	var page dto.PageReq
	if err := c.ShouldBindQuery(&page); err != nil {
		response.BindError(c, err)
		return
	}

	list, total, err := h.service.List(c.Request.Context(), appbookmark.ListFilter{
		Page:     page.Page,
		PageSize: page.PageSize,
		Keyword:  page.Keyword,
	})
	if err != nil {
		httpCode, message := h.handleError("查询备忘录列表失败", "查询失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	resp := make([]dto.BookmarkResp, 0, len(list))
	for _, item := range list {
		resp = append(resp, toBookmarkResp(item))
	}

	response.Success(c, response.PagedData(resp, total, page.Page, page.PageSize))
}

// CreateBookmark 创建备忘录。
func (h *BookmarkHandler) CreateBookmark(c *gin.Context) {
	var req dto.CreateBookmarkReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	item, err := h.service.Create(c.Request.Context(), appbookmark.CreateInput{
		Name:    req.Name,
		BaseURL: req.BaseURL,
		Remark:  req.Remark,
	})
	if err != nil {
		httpCode, message := h.handleError("创建备忘录失败", "创建失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, toBookmarkResp(item))
}

// UpdateBookmark 更新备忘录。
func (h *BookmarkHandler) UpdateBookmark(c *gin.Context) {
	id, err := ParseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的备忘录 ID")
		return
	}

	var req dto.UpdateBookmarkReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	item, err := h.service.Update(c.Request.Context(), id, appbookmark.UpdateInput{
		Name:    req.Name,
		BaseURL: req.BaseURL,
		Remark:  req.Remark,
	})
	if err != nil {
		httpCode, message := h.handleError("更新备忘录失败", "更新失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, toBookmarkResp(item))
}

// DeleteBookmark 删除备忘录。
func (h *BookmarkHandler) DeleteBookmark(c *gin.Context) {
	id, err := ParseID(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的备忘录 ID")
		return
	}

	if err := h.service.Delete(c.Request.Context(), id); err != nil {
		httpCode, message := h.handleError("删除备忘录失败", "删除失败", err)
		response.Error(c, httpCode, httpCode, message)
		return
	}

	response.Success(c, nil)
}
