package logx

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// prettyHandler 给 text 格式日志加 ANSI 颜色：level 按等级染色、time/key 暗化、message 加粗。
//
// 行为：
//   - 输出非 TTY 或环境变量 NO_COLOR=1 → 自动退化为纯文本（保持 slog text 格式风格）
//   - format=json 永远不走这里，由 JSONHandler 直接输出
//
// 设计取舍：自己实现而不引入 tint，是为了零额外依赖。
// 仅支持 With/WithAttrs，不支持 group（airgate 内部不用）。
type prettyHandler struct {
	mu       *sync.Mutex
	out      io.Writer
	minLevel slog.Level
	color    bool
	attrs    []slog.Attr
}

// ANSI 颜色码。SGR (Select Graphic Rendition) sequence。
const (
	ansiReset      = "\x1b[0m"
	ansiBold       = "\x1b[1m"
	ansiDim        = "\x1b[2m"
	ansiRed        = "\x1b[31m"
	ansiGreen      = "\x1b[32m"
	ansiYellow     = "\x1b[33m"
	ansiCyan       = "\x1b[36m"
	ansiBoldRed    = "\x1b[1;31m"
	ansiBoldGreen  = "\x1b[1;32m"
	ansiBoldYellow = "\x1b[1;33m"
	ansiBoldCyan   = "\x1b[1;36m"
)

func newPrettyHandler(out io.Writer, level slog.Level, color bool) *prettyHandler {
	return &prettyHandler{
		mu:       &sync.Mutex{},
		out:      out,
		minLevel: level,
		color:    color,
	}
}

func (h *prettyHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.minLevel
}

func (h *prettyHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	var b strings.Builder
	b.Grow(256)

	// 时间：HH:MM:SS.mmm 比 RFC3339 短得多，前端调试可读
	tsRaw := r.Time.Format("15:04:05.000")
	h.writeColored(&b, ansiDim, tsRaw)
	b.WriteByte(' ')

	// 等级：定宽 5 字符 + 等级色
	h.writeColored(&b, levelColor(r.Level), levelLabel(r.Level))
	b.WriteByte(' ')

	// 消息：加粗
	h.writeColored(&b, ansiBold, r.Message)

	// 把 With(...) 里的 attrs 写进来
	for _, a := range h.attrs {
		writeAttr(&b, a, h.color)
	}
	// Record 自带 attrs
	r.Attrs(func(a slog.Attr) bool {
		writeAttr(&b, a, h.color)
		return true
	})

	b.WriteByte('\n')
	_, err := io.WriteString(h.out, b.String())
	return err
}

func (h *prettyHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	new := *h
	new.attrs = append(slices.Clone(h.attrs), attrs...)
	return &new
}

func (h *prettyHandler) WithGroup(_ string) slog.Handler {
	// airgate 不使用 group，直接返回自身
	return h
}

func (h *prettyHandler) writeColored(b *strings.Builder, color, s string) {
	if !h.color || color == "" {
		b.WriteString(s)
		return
	}
	b.WriteString(color)
	b.WriteString(s)
	b.WriteString(ansiReset)
}

// levelLabel 返回定宽 5 字符的等级标签。
func levelLabel(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "ERROR"
	case l >= slog.LevelWarn:
		return "WARN "
	case l >= slog.LevelInfo:
		return "INFO "
	default:
		return "DEBUG"
	}
}

// levelColor 返回等级对应的 ANSI 色。
func levelColor(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return ansiBoldRed
	case l >= slog.LevelWarn:
		return ansiBoldYellow
	case l >= slog.LevelInfo:
		return ansiBoldGreen
	default:
		return ansiBoldCyan
	}
}

// writeAttr 把单个 attr 以 " key=value" 格式追加，key 暗化。
func writeAttr(b *strings.Builder, a slog.Attr, color bool) {
	if a.Equal(slog.Attr{}) {
		return
	}
	b.WriteByte(' ')
	if color {
		b.WriteString(ansiDim)
		b.WriteString(a.Key)
		b.WriteString(ansiReset)
	} else {
		b.WriteString(a.Key)
	}
	b.WriteByte('=')
	writeValue(b, a.Value, color, a.Key)
}

// writeValue 按值类型格式化；string 含空白时用 strconv.Quote。
func writeValue(b *strings.Builder, v slog.Value, color bool, key string) {
	v = v.Resolve()
	switch v.Kind() {
	case slog.KindString:
		s := v.String()
		// 高亮 request_id 让肉眼快速锁定
		if color && key == LogFieldRequestID && s != "" {
			b.WriteString(ansiCyan)
			b.WriteString(quoteIfNeeded(s))
			b.WriteString(ansiReset)
			return
		}
		b.WriteString(quoteIfNeeded(s))
	case slog.KindInt64:
		b.WriteString(strconv.FormatInt(v.Int64(), 10))
	case slog.KindUint64:
		b.WriteString(strconv.FormatUint(v.Uint64(), 10))
	case slog.KindFloat64:
		b.WriteString(strconv.FormatFloat(v.Float64(), 'g', -1, 64))
	case slog.KindBool:
		b.WriteString(strconv.FormatBool(v.Bool()))
	case slog.KindDuration:
		b.WriteString(v.Duration().String())
	case slog.KindTime:
		b.WriteString(v.Time().Format(time.RFC3339))
	case slog.KindAny:
		any := v.Any()
		if err, ok := any.(error); ok {
			// 错误用红色，便于扫读
			s := err.Error()
			if color {
				b.WriteString(ansiRed)
				b.WriteString(quoteIfNeeded(s))
				b.WriteString(ansiReset)
				return
			}
			b.WriteString(quoteIfNeeded(s))
			return
		}
		b.WriteString(quoteIfNeeded(fmt.Sprint(any)))
	default:
		b.WriteString(quoteIfNeeded(v.String()))
	}
}

// quoteIfNeeded 含空白/引号/不可见字符时用 strconv.Quote，与 slog TextHandler 保持一致。
func quoteIfNeeded(s string) string {
	if s == "" {
		return `""`
	}
	for _, r := range s {
		if r <= ' ' || r == '"' || r == '=' {
			return strconv.Quote(s)
		}
	}
	return s
}

// shouldColor 判断当前 stdout 是否应当上色：
//   - NO_COLOR 环境变量被设置（标准约定，https://no-color.org）→ false
//   - TERM=dumb → false
//   - stdout 不是 TTY → false（被管道/重定向到文件）
//   - 否则 true
func shouldColor(f *os.File) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if term := os.Getenv("TERM"); term == "dumb" {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
