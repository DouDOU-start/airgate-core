package handler

import "strconv"

// ParseID 将路径参数字符串解析为 int ID。
// 用于替代各 handler 重复定义的 parseAccountID / parseUserID 等函数。
func ParseID(raw string) (int, error) {
	return strconv.Atoi(raw)
}

// parseOptionalInt 解析可选整数查询参数：空串或非法值返回 nil。
func parseOptionalInt(raw string) *int {
	if raw == "" {
		return nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return nil
	}
	return &value
}
