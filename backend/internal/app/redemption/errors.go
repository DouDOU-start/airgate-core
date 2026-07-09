package redemption

import "errors"

var (
	// ErrCodeNotFound 兑换码不存在。
	ErrCodeNotFound = errors.New("兑换码不存在")
	// ErrCodeUsed 兑换码已被使用。
	ErrCodeUsed = errors.New("兑换码已被使用")
	// ErrCodeDisabled 兑换码已被停用。
	ErrCodeDisabled = errors.New("兑换码已停用")
	// ErrCodeExpired 兑换码已过期。
	ErrCodeExpired = errors.New("兑换码已过期")
	// ErrCodeStateConflict 状态流转不允许（如对已使用的码停用/删除）。
	ErrCodeStateConflict = errors.New("兑换码状态不允许该操作")
	// ErrInvalidGenerateInput 生成参数不合法。
	ErrInvalidGenerateInput = errors.New("生成参数不合法")
	// ErrRedeemRateLimited 兑换失败次数过多，暂时限流。
	ErrRedeemRateLimited = errors.New("失败次数过多，请稍后再试")
	// ErrUserNotFound 兑换归属用户不存在。
	ErrUserNotFound = errors.New("用户不存在")
)
