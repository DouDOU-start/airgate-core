// Package dto 定义请求/响应数据传输对象，与前端 TS 类型一一对应
package dto

import "time"

// PageReq 分页请求参数
type PageReq struct {
	Page     int    `form:"page" binding:"min=1"`
	PageSize int    `form:"page_size" binding:"min=1,max=100"`
	Keyword  string `form:"keyword"`
}

// PageResp 分页响应
type PageResp[T any] struct {
	List     []T   `json:"list"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
}

// IDParam 路径参数 ID
type IDParam struct {
	ID int64 `uri:"id" binding:"required,min=1"`
}

// TimeMixin 通用时间字段
type TimeMixin struct {
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
