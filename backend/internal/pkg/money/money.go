// Package money 提供 decimal(20,8) 平台余额与最小单位之间的精确转换。
package money

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Scale 表示 1 平台余额对应的最小单位数量。
const Scale int64 = 100_000_000

// Parse 将最多 8 位小数的十进制金额解析为最小单位。
func Parse(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "-") || strings.HasPrefix(raw, "+") {
		return 0, errors.New("金额格式错误")
	}
	parts := strings.Split(raw, ".")
	if len(parts) > 2 {
		return 0, errors.New("金额格式错误")
	}
	whole := parts[0]
	if whole == "" {
		whole = "0"
	}
	wholeValue, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || wholeValue < 0 || wholeValue > (1<<63-1)/Scale {
		return 0, errors.New("金额超出范围")
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if len(fraction) > 8 {
		return 0, errors.New("金额最多支持 8 位小数")
	}
	for _, ch := range fraction {
		if ch < '0' || ch > '9' {
			return 0, errors.New("金额格式错误")
		}
	}
	fraction += strings.Repeat("0", 8-len(fraction))
	fractionValue := int64(0)
	if fraction != "" {
		fractionValue, err = strconv.ParseInt(fraction, 10, 64)
		if err != nil {
			return 0, errors.New("金额格式错误")
		}
	}
	return wholeValue*Scale + fractionValue, nil
}

// FormatUnits 将最小单位格式化为十进制字符串。
func FormatUnits(units int64) string {
	sign := ""
	if units < 0 {
		sign = "-"
		units = -units
	}
	whole := units / Scale
	fraction := units % Scale
	if fraction == 0 {
		return fmt.Sprintf("%s%d", sign, whole)
	}
	return fmt.Sprintf("%s%d.%s", sign, whole, strings.TrimRight(fmt.Sprintf("%08d", fraction), "0"))
}

// UnitsToFloat 将最小单位转换为现有计费层使用的 float64。
func UnitsToFloat(units int64) float64 { return float64(units) / float64(Scale) }

// FloatToUnits 将库内余额按 decimal(20,8) 精度归一到最小单位。
func FloatToUnits(value float64) int64 {
	if value >= 0 {
		return int64(value*float64(Scale) + 0.5)
	}
	return int64(value*float64(Scale) - 0.5)
}

// FormatFloat 将库内余额按 8 位精度格式化。
func FormatFloat(value float64) string { return FormatUnits(FloatToUnits(value)) }
