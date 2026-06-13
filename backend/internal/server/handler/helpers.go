package handler

import "strconv"

// ParseID 将路径参数字符串解析为 int ID。
// 用于替代各 handler 重复定义的 parseAccountID / parseUserID 等函数。
func ParseID(raw string) (int, error) {
	return strconv.Atoi(raw)
}
