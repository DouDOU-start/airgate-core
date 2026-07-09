// Package logx 提供全局 slog 初始化、context 绑定 logger 与 request-id 传播。
//
// 字段命名一律 snake_case，事件名一律 snake_case，由 slog 统一输出。
// 全局 module 字段由 InitLogger 注入。
package logx

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/google/uuid"
)

// 日志相关 HTTP header 与字段名约定。
const (
	HeaderRequestID = "X-Request-ID"

	LogFieldRequestID  = "request_id"
	LogFieldUserID     = "user_id"
	LogFieldGroupID    = "group_id"
	LogFieldAccountID  = "account_id"
	LogFieldAPIKeyID   = "api_key_id"
	LogFieldModel      = "model"
	LogFieldPlatform   = "platform"
	LogFieldStatus     = "status_code"
	LogFieldDurationMs = "duration_ms"
	LogFieldError      = "error"
	LogFieldMethod     = "method"
	LogFieldPath       = "path"
	LogFieldReason     = "reason"
)

// InitLogger 初始化全局 slog。
// module 日志来源标识（如 "core"）；level: debug/info/warn/error；format: text/json。
//
// text 格式且 stdout 是 TTY 时自动启用 ANSI 颜色（按等级染色 + request_id 高亮）；
// 重定向到文件 / 管道、或设置 NO_COLOR=1 时自动退化为纯文本。
func InitLogger(module, level, format string) {
	lvl := parseLevel(level)

	var handler slog.Handler
	if format == "json" {
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	} else {
		handler = newPrettyHandler(os.Stdout, lvl, shouldColor(os.Stdout))
	}

	slog.SetDefault(slog.New(handler).With("module", module))
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// ============================================================================
// Context 绑定 logger / request-id 传播
// ============================================================================

type ctxKey int

const (
	ctxKeyLogger ctxKey = iota
	ctxKeyRequestID
)

// WithLogger 把 logger 绑到 context；通常由 HTTP 入口构造请求级 logger 后调用。
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	if logger == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxKeyLogger, logger)
}

// LoggerFromContext 从 context 取 logger；不存在则返回 slog.Default()。
//
// 该函数永远不返回 nil，调用方可直接 .Info/.Error。
func LoggerFromContext(ctx context.Context) *slog.Logger {
	if ctx == nil {
		return slog.Default()
	}
	if l, ok := ctx.Value(ctxKeyLogger).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}

// WithRequestID 把 request_id 绑到 context（不修改 logger，仅作为独立 value）。
func WithRequestID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxKeyRequestID, id)
}

// RequestIDFromContext 从 context 取 request_id；不存在返回空串。
func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if id, ok := ctx.Value(ctxKeyRequestID).(string); ok {
		return id
	}
	return ""
}

// ExtractOrGenerateRequestID 从 HTTP header 抽取 X-Request-ID；缺失则生成新 UUID。
// 用于所有"链路开始"的位置：接到客户端请求 → 抽取/生成 → 写回 header 向下透传。
func ExtractOrGenerateRequestID(headers http.Header) string {
	if headers != nil {
		if id := strings.TrimSpace(headers.Get(HeaderRequestID)); id != "" {
			return id
		}
	}
	return uuid.NewString()
}

// LoggerWithRequestID 为 ctx 派生一个带 request_id 字段的 logger 并写回 ctx。
//
// 典型用法（HTTP 中间件）：
//
//	rid := logx.ExtractOrGenerateRequestID(r.Header)
//	ctx = logx.WithRequestID(ctx, rid)
//	ctx, logger := logx.LoggerWithRequestID(ctx)
//	logger.Info("http_request_received", "method", r.Method, "path", r.URL.Path)
func LoggerWithRequestID(ctx context.Context) (context.Context, *slog.Logger) {
	rid := RequestIDFromContext(ctx)
	if rid == "" {
		rid = uuid.NewString()
		ctx = WithRequestID(ctx, rid)
	}
	logger := LoggerFromContext(ctx).With(LogFieldRequestID, rid)
	return WithLogger(ctx, logger), logger
}
