package account

import "errors"

var (
	// ErrAccountNotFound 账号不存在。
	ErrAccountNotFound = errors.New("账号不存在")
	// ErrPluginNotFound 未找到对应平台插件。
	ErrPluginNotFound = errors.New("未找到对应平台插件")
	// ErrModelRequired 缺少测试模型。
	ErrModelRequired = errors.New("请指定测试模型")
	// ErrInvalidConnectivityTestMode 账号测试模式不受支持。
	ErrInvalidConnectivityTestMode = errors.New("不支持的账号测试模式")
	// ErrConnectivityTestTransformUnavailable 显式增强测试无法获得有效请求体。
	ErrConnectivityTestTransformUnavailable = errors.New("超额模式测试增强不可用")
	// ErrConnectivityTestModeAccountTypeUnsupported 当前账号类型不支持所选测试模式。
	ErrConnectivityTestModeAccountTypeUnsupported = errors.New("超额模式仅支持 OAuth 账号")
	// ErrQuotaRefreshUnsupported 当前平台不支持额度刷新。
	ErrQuotaRefreshUnsupported = errors.New("该平台不支持刷新额度")
	// ErrInvalidDateRange 日期范围参数非法。
	ErrInvalidDateRange = errors.New("日期范围无效")
	// ErrReauthRequired OAuth 凭证已失效，需要重新授权（refresh_token 失效且无法本地降级）。
	ErrReauthRequired = errors.New("账号凭证已失效，请重新授权")
	// ErrSchedulerUnavailable 调度器不可用。
	ErrSchedulerUnavailable = errors.New("调度器不可用")
)
