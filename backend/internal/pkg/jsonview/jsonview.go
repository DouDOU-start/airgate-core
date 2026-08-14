// Package jsonview 提供只读 JSON 字节切片的零拷贝查询视图。
package jsonview

import (
	"unsafe"

	"github.com/tidwall/gjson"
)

// ParseBytes 把只读字节切片交给 gjson 解析且不复制完整载荷。
// 调用方必须保证 result 使用期间 data 不被修改或释放；Go 切片由 result 的
// 同步调用栈持有时满足该约束。需要跨 goroutine 或长期保存时应自行复制。
func ParseBytes(data []byte) gjson.Result {
	if len(data) == 0 {
		return gjson.Result{}
	}
	return gjson.Parse(unsafe.String(unsafe.SliceData(data), len(data)))
}
