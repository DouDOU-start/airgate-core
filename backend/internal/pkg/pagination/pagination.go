// Package pagination 提供分页参数归一化工具。
package pagination

const (
	// DefaultPage 默认页码。
	DefaultPage = 1
	// DefaultPageSize 默认每页条数。
	DefaultPageSize = 20
)

// Normalize 归一化分页参数：page <= 0 重置为 DefaultPage，
// pageSize <= 0 重置为 DefaultPageSize。
func Normalize(page, pageSize int) (int, int) {
	if page <= 0 {
		page = DefaultPage
	}
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	return page, pageSize
}
