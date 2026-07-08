package dto

// AnnouncementResp 公告响应（管理端）。
type AnnouncementResp struct {
	ID         int64   `json:"id"`
	Title      string  `json:"title"`
	Content    string  `json:"content"`
	Status     string  `json:"status"`
	NotifyMode string  `json:"notify_mode"`
	StartsAt   *string `json:"starts_at"`
	EndsAt     *string `json:"ends_at"`

	TimeMixin
}

// UserAnnouncementResp 公告响应（用户端）：不暴露 status，附带当前用户已读时间。
type UserAnnouncementResp struct {
	ID         int64   `json:"id"`
	Title      string  `json:"title"`
	Content    string  `json:"content"`
	NotifyMode string  `json:"notify_mode"`
	ReadAt     *string `json:"read_at"`

	TimeMixin
}

// CreateAnnouncementReq 创建公告请求。
// StartsAt/EndsAt 为 RFC3339 字符串，缺省/空串 = 立即生效 / 永久展示。
type CreateAnnouncementReq struct {
	Title      string  `json:"title" binding:"required"`
	Content    string  `json:"content" binding:"required"`
	Status     string  `json:"status" binding:"omitempty,oneof=draft active archived"`
	NotifyMode string  `json:"notify_mode" binding:"omitempty,oneof=silent popup"`
	StartsAt   *string `json:"starts_at"`
	EndsAt     *string `json:"ends_at"`
}

// UpdateAnnouncementReq 更新公告请求。
// 时间字段用指针区分：nil = 不修改，空串 = 清空。
type UpdateAnnouncementReq struct {
	Title      *string `json:"title"`
	Content    *string `json:"content"`
	Status     *string `json:"status" binding:"omitempty,oneof=draft active archived"`
	NotifyMode *string `json:"notify_mode" binding:"omitempty,oneof=silent popup"`
	StartsAt   *string `json:"starts_at"`
	EndsAt     *string `json:"ends_at"`
}
