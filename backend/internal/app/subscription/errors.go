package subscription

import "errors"

var (
	// ErrSubscriptionNotFound 订阅不存在。
	ErrSubscriptionNotFound = errors.New("订阅不存在")
	// ErrInvalidExpiresAt 分配订阅时过期时间格式错误。
	ErrInvalidExpiresAt = errors.New("过期时间格式错误，请使用 RFC3339 格式")
	// ErrInvalidAdjustExpiresAt 调整订阅时过期时间格式错误。
	ErrInvalidAdjustExpiresAt = errors.New("过期时间格式错误")
)
