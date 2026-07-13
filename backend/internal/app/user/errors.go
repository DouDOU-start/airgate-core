package user

import "errors"

var (
	// ErrUserNotFound 用户不存在。
	ErrUserNotFound = errors.New("用户不存在")
	// ErrEmailAlreadyExists 邮箱已被注册。
	ErrEmailAlreadyExists = errors.New("邮箱已被注册")
	// ErrOldPasswordMismatch 旧密码错误。
	ErrOldPasswordMismatch = errors.New("旧密码错误")
	// ErrInsufficientBalance 余额不足。
	ErrInsufficientBalance = errors.New("余额不足")
	// ErrInvalidBalanceAction 无效的余额操作类型。
	ErrInvalidBalanceAction = errors.New("无效的操作类型")
	// ErrDeleteAdminForbidden 禁止删除管理员。
	ErrDeleteAdminForbidden = errors.New("不能删除管理员用户")
	// ErrInvalidRateMultiplier 专属倍率非法（不能为负）。
	ErrInvalidRateMultiplier = errors.New("专属倍率不能为负数")
	// ErrDuplicateBalanceChange 幂等键已存在，同一笔余额变更已入账。
	ErrDuplicateBalanceChange = errors.New("余额变更已入账（幂等键重复）")
	// ErrTierNotFound 指定的用户等级不存在。
	ErrTierNotFound = errors.New("等级不存在")
	// ErrTooManySortCandidates 并发/RPM 排序候选集过大，需先用筛选条件缩小范围。
	ErrTooManySortCandidates = errors.New("匹配用户数过多，无法按该字段排序，请先缩小筛选范围")
)
