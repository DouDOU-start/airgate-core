package group

import (
	"errors"
	"fmt"
)

var (
	// ErrGroupNotFound 表示目标分组不存在。
	ErrGroupNotFound = errors.New("分组不存在")
	// ErrUserNotFound 表示目标用户不存在（授予专属分组时校验）。
	ErrUserNotFound = errors.New("用户不存在")
)

// GroupHasChannelsError 表示分组仍被渠道绑定引用，不能直接删除。
// 直接删除会经 channel_groups 的 ON DELETE CASCADE 抹掉绑定行，
// 若渠道 key 只绑了这一个分组，会静默变成空分组 key（不再被任何分组调度到），
// 造成线上渠道悄悄断流。
type GroupHasChannelsError struct {
	// Count 仍绑定该分组的渠道数。
	Count int
}

func (e *GroupHasChannelsError) Error() string {
	return fmt.Sprintf("分组仍绑定 %d 个渠道，请先解绑", e.Count)
}
