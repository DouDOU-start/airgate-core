package settings

import "errors"

// ErrSMTPConnection SMTP 连接/发送失败（用户可修正的配置错误）。
var ErrSMTPConnection = errors.New("SMTP 连接或发送失败")

// ErrGenerateKey 密钥生成失败。
var ErrGenerateKey = errors.New("密钥生成失败")

// ErrEncryptKey 密钥加密失败。
var ErrEncryptKey = errors.New("密钥加密失败")

// ErrSecuritySettingReadOnly security 组设置禁止经通用 Update 写入
// （只允许 GenerateAdminAPIKey / DeleteAdminAPIKey 专用通道）。
var ErrSecuritySettingReadOnly = errors.New("security 组设置禁止经通用更新接口写入")
