// Package timezone 提供按调用方时区计算时间边界的工具。
//
// 背景：所有面向用户的"今天 / 昨天 / 最近 N 天"以及前端传入的 YYYY-MM-DD
// 日期范围都必须按用户当前的时区解释，否则在跨时区使用时会出现明显错位。
// 典型陷阱是 Go 的 time.Truncate(24*time.Hour)：它是相对于 UTC 零点截断的，
// 在 Asia/Shanghai (UTC+8) 下会得到当天 08:00 而不是 00:00。
//
// 用法约定：
//   - HTTP 层从查询参数 tz=IANA 名解析；为空或无效时回退服务器本地时区。
//   - service / store 层调用 Resolve 拿到 *time.Location，再用 StartOfDay 等
//     计算边界；不要直接用 time.Truncate。
package timezone

import "time"

// Resolve 解析 IANA 时区名；空或无效时回退到服务器本地时区。
// 调用方传入错误的 tz 不应导致请求失败，所以这里静默回退。
func Resolve(tz string) *time.Location {
	if tz == "" {
		return time.Local
	}
	if loc, err := time.LoadLocation(tz); err == nil {
		return loc
	}
	return time.Local
}

// StartOfDay 返回 t 当天（按 t 所在时区）的 00:00:00。
//
// 不要使用 t.Truncate(24*time.Hour)：它相对于 UTC 零点截断，
// 在非 UTC 时区下结果会偏移到当天的非零点时刻。
func StartOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// EndOfDay 返回 t 当天的 23:59:59.999999999（包含整天的右端点）。
func EndOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 23, 59, 59, int(time.Second-time.Nanosecond), t.Location())
}

// ParseDate 在指定时区解析 YYYY-MM-DD 形式的日期字符串。
// 等价于 time.ParseInLocation("2006-01-02", s, loc)。
func ParseDate(s string, loc *time.Location) (time.Time, error) {
	return time.ParseInLocation("2006-01-02", s, loc)
}
