package auth

import "errors"

var (
	// ErrInvalidCredentials 用户名或密码错误。
	ErrInvalidCredentials = errors.New("邮箱或密码错误")
	// ErrUserDisabled 用户已禁用。
	ErrUserDisabled = errors.New("账户已禁用")
	// ErrEmailAlreadyExists 注册邮箱已存在。
	ErrEmailAlreadyExists = errors.New("邮箱已注册")
	// ErrUserNotFound 用户不存在。
	ErrUserNotFound = errors.New("用户不存在")
)
