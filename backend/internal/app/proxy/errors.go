package proxy

import "errors"

var (
	// ErrProxyNotFound 表示目标代理不存在。
	ErrProxyNotFound = errors.New("代理不存在")
)
