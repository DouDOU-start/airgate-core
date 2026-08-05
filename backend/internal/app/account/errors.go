package account

import "errors"

var (
	// ErrAccountNotFound 账号不存在。
	ErrAccountNotFound = errors.New("账号不存在")
	// ErrInvalidCredentials 凭证加密/解密/JSON 编解码失败。
	ErrInvalidCredentials = errors.New("凭证无效")
	// ErrEmptyCredentials 创建/更新时凭证为空。
	ErrEmptyCredentials = errors.New("凭证不能为空")
	// ErrInvalidState 手动写入的 state 不是 active/disabled。
	ErrInvalidState = errors.New("状态无效")
	// ErrUnsupportedPlatform 不支持的账号平台。
	ErrUnsupportedPlatform = errors.New("不支持的账号平台")
	// ErrTooManySortCandidates 并发/RPM 排序候选集过大，需先缩小筛选范围。
	ErrTooManySortCandidates = errors.New("匹配账号数过多，无法按该字段排序，请先缩小筛选范围")
)
