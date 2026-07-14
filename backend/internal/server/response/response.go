// Package response 提供统一的 HTTP JSON 响应格式
package response

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// R 统一响应结构。Data 不带 omitempty：nil 也要序列化成显式的 "data": null——
// 前端 useQuery 的 queryFn 不允许返回 undefined，键缺失会被当成查询失败抛错
// （曾经查询失败是静默的没暴露，加了全局兜底 toast 后这个坑会立刻炸出来）。
type R struct {
	Code    int         `json:"code"`
	Data    interface{} `json:"data"`
	Message string      `json:"message"`
}

// Success 返回成功响应
func Success(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, R{
		Code:    0,
		Data:    data,
		Message: "ok",
	})
}

// Error 返回错误响应
func Error(c *gin.Context, httpCode int, code int, msg string) {
	c.JSON(httpCode, R{
		Code:    code,
		Message: msg,
	})
}

// BadRequest 返回 400 错误
func BadRequest(c *gin.Context, msg string) {
	Error(c, http.StatusBadRequest, 400, msg)
}

// BindError 处理参数绑定错误，返回友好提示，内部错误仅记日志
func BindError(c *gin.Context, err error) {
	slog.Debug("请求参数绑定失败", "path", c.FullPath(), "error", err)
	BadRequest(c, "请求参数格式不正确，请检查输入")
}

// Unauthorized 返回 401 错误
func Unauthorized(c *gin.Context, msg string) {
	Error(c, http.StatusUnauthorized, 401, msg)
}

// Forbidden 返回 403 错误
func Forbidden(c *gin.Context, msg string) {
	Error(c, http.StatusForbidden, 403, msg)
}

// NotFound 返回 404 错误
func NotFound(c *gin.Context, msg string) {
	Error(c, http.StatusNotFound, 404, msg)
}

// InternalError 返回 500 错误
func InternalError(c *gin.Context, msg string) {
	Error(c, http.StatusInternalServerError, 500, msg)
}

// PagedData 构建分页响应数据
func PagedData(list interface{}, total int64, page, pageSize int) map[string]interface{} {
	return map[string]interface{}{
		"list":      list,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	}
}
