package scheduler

// Schedulability 账户调度状态（三态）
type Schedulability int

const (
	// Normal 可正常调度
	Normal Schedulability = iota
	// StickyOnly 仅允许粘性会话访问（如 RPM 接近上限、窗口费用接近阈值）
	StickyOnly
	// NotSchedulable 不可调度（如 RPM 已满、窗口费用超限）
	NotSchedulable
)

// ExtraFloat64 从 account.Extra 中安全提取 float64 值
func ExtraFloat64(extra map[string]interface{}, key string) float64 {
	v, ok := extra[key]
	if !ok {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return val
	case int:
		return float64(val)
	case int64:
		return float64(val)
	default:
		return 0
	}
}

// ExtraInt 从 account.Extra 中安全提取 int 值
func ExtraInt(extra map[string]interface{}, key string) int {
	v, ok := extra[key]
	if !ok {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return int(val)
	case int:
		return val
	case int64:
		return int(val)
	default:
		return 0
	}
}
