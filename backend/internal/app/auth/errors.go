package auth

import "errors"

var (
	// ErrInvalidCredentials 用户名或密码错误。
	ErrInvalidCredentials = errors.New("邮箱或密码错误")
	// ErrUserDisabled 用户已禁用。
	ErrUserDisabled = errors.New("账户已禁用")
	// ErrTOTPCodeRequired 已启用 TOTP 时缺少验证码。
	ErrTOTPCodeRequired = errors.New("需要 TOTP 验证码")
	// ErrInvalidTOTPCode 登录时 TOTP 验证码错误。
	ErrInvalidTOTPCode = errors.New("TOTP 验证码错误")
	// ErrEmailAlreadyExists 注册邮箱已存在。
	ErrEmailAlreadyExists = errors.New("邮箱已注册")
	// ErrUserNotFound 用户不存在。
	ErrUserNotFound = errors.New("用户不存在")
	// ErrTOTPAlreadyEnabled TOTP 已启用。
	ErrTOTPAlreadyEnabled = errors.New("TOTP 已启用")
	// ErrTOTPNotSetup 尚未设置 TOTP。
	ErrTOTPNotSetup = errors.New("请先设置 TOTP")
	// ErrTOTPNotEnabled TOTP 未启用。
	ErrTOTPNotEnabled = errors.New("TOTP 未启用")
	// ErrVerificationCodeInvalid TOTP 验证码错误。
	ErrVerificationCodeInvalid = errors.New("验证码错误")
	// ErrGenerateTOTPSecretFailed 生成 TOTP 密钥失败。
	ErrGenerateTOTPSecretFailed = errors.New("生成 TOTP 密钥失败")
	// ErrSaveTOTPSecretFailed 保存 TOTP 密钥失败。
	ErrSaveTOTPSecretFailed = errors.New("保存 TOTP 密钥失败")
)
