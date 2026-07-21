package riskcontrol

import "errors"

var (
	// ErrInvalidConfig 配置不合法（模式/URL/状态码/模型过滤等校验失败）。
	ErrInvalidConfig = errors.New("风控配置不合法")
	// ErrUserNotFound 目标用户不存在。
	ErrUserNotFound = errors.New("用户不存在")
	// ErrInvalidHash 输入哈希格式不合法（须为 64 位 hex）。
	ErrInvalidHash = errors.New("输入哈希格式不合法")
)
