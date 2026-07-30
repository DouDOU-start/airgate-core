package wallet

import "errors"

var (
	ErrInvalidAmount       = errors.New("金额必须大于 0，且最多支持 8 位小数")
	ErrInsufficientBalance = errors.New("余额不足")
	ErrIdempotencyConflict = errors.New("幂等键对应的交易参数不一致")
	ErrTransactionNotFound = errors.New("原交易不存在")
	ErrRefundExceeded      = errors.New("退款金额超过原扣款可退金额")
)
