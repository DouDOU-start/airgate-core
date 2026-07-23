package dto

// BookmarkResp 备忘录响应。
type BookmarkResp struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	Remark  string `json:"remark"`

	TimeMixin
}

// CreateBookmarkReq 创建备忘录请求。
type CreateBookmarkReq struct {
	Name    string `json:"name" binding:"required"`
	BaseURL string `json:"base_url"`
	Remark  string `json:"remark"`
}

// UpdateBookmarkReq 更新备忘录请求。
type UpdateBookmarkReq struct {
	Name    *string `json:"name"`
	BaseURL *string `json:"base_url"`
	Remark  *string `json:"remark"`
}
