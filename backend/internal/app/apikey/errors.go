package apikey

import "errors"

var (
	// ErrKeyNotFound API Key 不存在。
	ErrKeyNotFound = errors.New("密钥不存在")
	// ErrGroupNotFound 分组不存在。
	ErrGroupNotFound = errors.New("分组不存在")
	// ErrGroupForbidden 用户无权使用分组。
	ErrGroupForbidden = errors.New("无权使用该分组")
	// ErrInvalidExpiresAt 过期时间格式错误。
	ErrInvalidExpiresAt = errors.New("过期时间格式错误")
	// ErrLegacyKeyNotReveal 旧密钥无法查看原文。
	ErrLegacyKeyNotReveal = errors.New("该密钥创建于加密存储启用前，无法查看原文")
	// ErrKeyDecryptFailed 密钥解密失败。
	ErrKeyDecryptFailed = errors.New("该密钥无法解密，可能创建于不同加密密钥下，无法查看原文")
	// ErrProvisionedKeyDisabled 应用专属密钥已被禁用（视为用户暂时封禁该应用）。
	ErrProvisionedKeyDisabled = errors.New("该应用的密钥已被禁用，请在密钥管理中重新启用")
	// ErrNoDefaultGroup 无可用默认分组。
	ErrNoDefaultGroup = errors.New("没有可用的默认分组")
)
