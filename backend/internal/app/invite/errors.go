package invite

import "errors"

var (
	ErrProfileNotFound    = errors.New("邀请画像不存在")
	ErrCodeInvalid        = errors.New("邀请码无效")
	ErrSelfInvite         = errors.New("不能绑定自己的邀请码")
	ErrAlreadyBound       = errors.New("已绑定过邀请人，不可重复绑定")
	ErrRebateBalanceEmpty = errors.New("暂无可转入余额的返利")
	ErrInvalidRate        = errors.New("返利比例无效")
	ErrUserNotFound       = errors.New("用户不存在")
)
