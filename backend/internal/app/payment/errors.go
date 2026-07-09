package payment

import "errors"

var (
	// ErrInvalidAmount 充值金额不在 [min_amount, max_amount] 区间。
	ErrInvalidAmount = errors.New("充值金额超出允许范围")
	// ErrDailyLimit 用户单日累计充值达到上限。
	ErrDailyLimit = errors.New("已达单日充值上限")
	// ErrNoProvider 没有可用的支付服务商承接该支付方式。
	ErrNoProvider = errors.New("暂无可用支付渠道")
	// ErrNotConfigured 支付模块未配置（callback_base_url 缺失）。
	ErrNotConfigured = errors.New("支付功能未配置")
	// ErrOrderNotFound 订单不存在或不属于当前用户。
	ErrOrderNotFound = errors.New("订单不存在")
	// ErrProviderNotFound 服务商实例不存在。
	ErrProviderNotFound = errors.New("支付服务商不存在")
	// ErrInvalidProviderKey 服务商实例 ID 不合法。
	ErrInvalidProviderKey = errors.New("服务商 ID 只能包含字母、数字、下划线和连字符（1-64 位）")
	// ErrAmountMismatch 回调金额与订单金额不符。
	ErrAmountMismatch = errors.New("回调金额与订单金额不符")
	// ErrOrderStateConflict 订单状态不允许该操作。
	ErrOrderStateConflict = errors.New("订单状态冲突")
)
