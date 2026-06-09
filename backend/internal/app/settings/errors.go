package settings

import "errors"

// ErrSMTPConnection SMTP 连接/发送失败（用户可修正的配置错误）。
var ErrSMTPConnection = errors.New("SMTP 连接或发送失败")

// ErrGenerateKey 密钥生成失败。
var ErrGenerateKey = errors.New("密钥生成失败")

// ErrEncryptKey 密钥加密失败。
var ErrEncryptKey = errors.New("密钥加密失败")
