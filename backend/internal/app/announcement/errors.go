package announcement

import "errors"

var (
	// ErrAnnouncementNotFound 表示目标公告不存在（或对当前用户不可见）。
	ErrAnnouncementNotFound = errors.New("公告不存在")
	// ErrInvalidStatus 表示公告状态取值非法。
	ErrInvalidStatus = errors.New("无效的公告状态")
	// ErrInvalidNotifyMode 表示通知模式取值非法。
	ErrInvalidNotifyMode = errors.New("无效的通知模式")
	// ErrInvalidTimeRange 表示起止时间窗口非法。
	ErrInvalidTimeRange = errors.New("公告开始时间须早于结束时间")
	// ErrInvalidTime 表示时间格式错误。
	ErrInvalidTime = errors.New("时间格式错误，须为 RFC3339")
)
