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
	// ErrInvalidAPIKeySession API Key 登录会话已失效。
	ErrInvalidAPIKeySession = errors.New("API Key 登录会话已失效")
	// ErrInvalidAPIKeyFormat API Key 格式无效。
	ErrInvalidAPIKeyFormat = errors.New("无效的 API Key 格式")
	// ErrInvalidAPIKey API Key 无效。
	ErrInvalidAPIKey = errors.New("无效的 API Key")
	// ErrAPIKeyExpired API Key 已过期。
	ErrAPIKeyExpired = errors.New("API Key 已过期")
	// ErrRegistrationDisabled 注册功能已关闭。
	ErrRegistrationDisabled = errors.New("注册功能已关闭")
	// ErrVerifyCodeRequired 需要验证码。
	ErrVerifyCodeRequired = errors.New("请输入验证码")
	// ErrVerifyCodeInvalid 验证码无效或已过期。
	ErrVerifyCodeInvalid = errors.New("验证码无效或已过期")
	// ErrMailerNotConfigured 邮件服务未配置。
	ErrMailerNotConfigured = errors.New("邮件服务未配置")
	// ErrSendMailFailed 发送邮件失败。
	ErrSendMailFailed = errors.New("发送邮件失败")
)
