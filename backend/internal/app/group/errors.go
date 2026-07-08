package group

import (
	"errors"
	"fmt"
)

var (
	// ErrGroupNotFound 表示目标分组不存在。
	ErrGroupNotFound = errors.New("分组不存在")
)

// GroupHasChannelsError 表示分组仍被渠道绑定引用，不能直接删除。
// 直接删除会经 channel_groups 的 ON DELETE CASCADE 抹掉绑定行，
// 使专属渠道静默变成公共渠道（GroupIDs 为空 = 对所有分组可用），越权扩散。
type GroupHasChannelsError struct {
	// Count 仍绑定该分组的渠道数。
	Count int
}

func (e *GroupHasChannelsError) Error() string {
	return fmt.Sprintf("分组仍绑定 %d 个渠道，请先解绑", e.Count)
}
