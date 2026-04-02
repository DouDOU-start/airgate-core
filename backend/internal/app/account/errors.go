package account

import "errors"

var (
	// ErrAccountNotFound 账号不存在。
	ErrAccountNotFound = errors.New("账号不存在")
	// ErrPluginNotFound 未找到对应平台插件。
	ErrPluginNotFound = errors.New("未找到对应平台插件")
	// ErrModelRequired 缺少测试模型。
	ErrModelRequired = errors.New("请指定测试模型")
	// ErrQuotaRefreshUnsupported 当前平台不支持额度刷新。
	ErrQuotaRefreshUnsupported = errors.New("该平台不支持刷新额度")
	// ErrInvalidDateRange 日期范围参数非法。
	ErrInvalidDateRange = errors.New("日期范围无效")
)
