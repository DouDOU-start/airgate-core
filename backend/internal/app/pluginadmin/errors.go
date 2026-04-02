package pluginadmin

import "errors"

var (
	// ErrPluginNotDev 表示当前插件不是开发模式。
	ErrPluginNotDev = errors.New("仅开发模式插件支持热加载")
	// ErrPluginUnavailable 表示插件不存在或未运行。
	ErrPluginUnavailable = errors.New("插件未运行或不存在")
)
