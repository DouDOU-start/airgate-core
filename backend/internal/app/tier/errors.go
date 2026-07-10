package tier

import (
	"errors"
	"fmt"
)

var (
	// ErrTierNotFound 等级不存在。
	ErrTierNotFound = errors.New("等级不存在")
	// ErrTierNameExists 等级名称已存在。
	ErrTierNameExists = errors.New("等级名称已存在")
	// ErrInvalidRate 等级倍率非法（必须大于 0；不设置该分组请直接删除条目）。
	ErrInvalidRate = errors.New("等级倍率必须大于 0")
)

// TierHasUsersError 等级仍有用户归属时拒绝删除。
// 直接删除会让这批用户的倍率静默回落到分组档位，等同一次隐性调价。
type TierHasUsersError struct {
	Count int
}

func (e *TierHasUsersError) Error() string {
	return fmt.Sprintf("仍有 %d 个用户属于该等级，请先移除用户归属", e.Count)
}
