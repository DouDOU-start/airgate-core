package jsonview

import (
	"strings"
	"testing"
)

func TestParseBytes读取嵌套字段(t *testing.T) {
	body := []byte(`{"input":[{"text":"你好"}],"stream":true}`)
	result := ParseBytes(body)
	if got := result.Get("input.0.text").String(); got != "你好" {
		t.Fatalf("text=%q", got)
	}
}

func BenchmarkParseBytes大请求(b *testing.B) {
	body := []byte(`{"input":"` + strings.Repeat("x", 2<<20) + `"}`)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	for b.Loop() {
		if !ParseBytes(body).IsObject() {
			b.Fatal("解析结果不是对象")
		}
	}
}
