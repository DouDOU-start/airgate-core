package timezone

import (
	"testing"
	"time"
)

func TestResolveFallsBackToLocalForEmptyOrInvalidTimezone(t *testing.T) {
	if got := Resolve(""); got != time.Local {
		t.Fatalf("空时区应回退本地时区，得到 %v", got)
	}
	if got := Resolve("不存在/时区"); got != time.Local {
		t.Fatalf("非法时区应回退本地时区，得到 %v", got)
	}
}

func TestResolveLoadsValidTimezone(t *testing.T) {
	got := Resolve("Asia/Shanghai")
	if got == time.Local {
		t.Fatal("有效时区不应直接回退本地时区")
	}
	if got.String() != "Asia/Shanghai" {
		t.Fatalf("时区 = %q，期望 Asia/Shanghai", got.String())
	}
}

func TestDayBoundariesUseInputLocation(t *testing.T) {
	loc := Resolve("Asia/Shanghai")
	value := time.Date(2026, 5, 15, 18, 30, 0, 123, loc)

	start := StartOfDay(value)
	if start.Hour() != 0 || start.Minute() != 0 || start.Second() != 0 || start.Location() != loc {
		t.Fatalf("开始时间异常: %v", start)
	}

	end := EndOfDay(value)
	if end.Hour() != 23 || end.Minute() != 59 || end.Second() != 59 || end.Nanosecond() != int(time.Second-time.Nanosecond) {
		t.Fatalf("结束时间异常: %v", end)
	}
}

func TestParseDateUsesLocation(t *testing.T) {
	loc := Resolve("Asia/Shanghai")
	got, err := ParseDate("2026-05-15", loc)
	if err != nil {
		t.Fatalf("解析日期失败: %v", err)
	}
	if got.Location() != loc || got.Hour() != 0 || got.Day() != 15 {
		t.Fatalf("解析结果异常: %v", got)
	}
}
