package schema

import "time"

// timeNow 返回当前时间（用于 Ent Schema 默认值）
func timeNow() time.Time {
	return time.Now()
}
