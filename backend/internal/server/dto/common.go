// Package dto 定义请求/响应数据传输对象，与前端 TS 类型一一对应
package dto

import "time"

// PageReq 分页请求参数。
// omitempty：不带分页参数的请求走 service 层 pagination.Normalize 的默认值，
// 而不是在绑定层直接 400。
type PageReq struct {
	Page     int    `form:"page" binding:"omitempty,min=1"`
	PageSize int    `form:"page_size" binding:"omitempty,min=1,max=100"`
	Keyword  string `form:"keyword"`
}

// TimeMixin 通用时间字段
type TimeMixin struct {
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
