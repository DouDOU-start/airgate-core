package user

import "errors"

var (
	// ErrUserNotFound 用户不存在。
	ErrUserNotFound = errors.New("用户不存在")
	// ErrEmailAlreadyExists 邮箱已被注册。
	ErrEmailAlreadyExists = errors.New("邮箱已被注册")
	// ErrOldPasswordMismatch 旧密码错误。
	ErrOldPasswordMismatch = errors.New("旧密码错误")
	// ErrInsufficientBalance 余额不足。
	ErrInsufficientBalance = errors.New("余额不足")
	// ErrInvalidBalanceAction 无效的余额操作类型。
	ErrInvalidBalanceAction = errors.New("无效的操作类型")
	// ErrDeleteAdminForbidden 禁止删除管理员。
	ErrDeleteAdminForbidden = errors.New("不能删除管理员用户")
	// ErrInvalidRateMultiplier 专属倍率非法（不能为负）。
	ErrInvalidRateMultiplier = errors.New("专属倍率不能为负数")
)
