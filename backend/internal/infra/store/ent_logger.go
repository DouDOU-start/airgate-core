// Package store 同时承担 ent.Client 的 logger 桥接职责。
//
// ent 默认 cfg.log = log.Println，所有 debug / error 信息会走 stdlib log，
// 完全丢失 slog 的结构化字段（level、time、attrs）。本文件提供一个 ent.Option，
// 把 ent 内部日志转发到 slog，方便统一收集。
package store

import (
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/DouDOU-start/airgate-core/ent"
)

// EntSlogLogger 返回一个 ent.Option，用 slog.Debug 输出 ent 自身日志。
//
// 用法：
//
//	client := ent.NewClient(ent.Driver(drv), store.EntSlogLogger())
//
// ent 仅在 Debug() 模式下才会调用 cfg.log；非 Debug 模式下日志开销为 0。
// 即便如此，统一用 slog 转发也能避免 stdlib log 输出污染（比如启动时的
// schema migration warning）。
func EntSlogLogger() ent.Option {
	return ent.Log(func(args ...any) {
		slog.Debug("ent_log", "msg", fmt.Sprint(args...))
	})
}

// RedactDSN 把 PostgreSQL DSN（key=value 形式）中的 password 字段擦掉，
// 用于结构化日志输出。也兼容 URL 形式（postgres://user:pass@host:port/db）。
//
// 示例：
//
//	"host=db port=5432 user=app password=secret dbname=airgate sslmode=disable"
//	→ "host=db port=5432 user=app password=*** dbname=airgate sslmode=disable"
func RedactDSN(dsn string) string {
	if dsn == "" {
		return ""
	}
	if strings.Contains(dsn, "://") {
		if u, err := url.Parse(dsn); err == nil {
			if u.User != nil {
				if _, hasPwd := u.User.Password(); hasPwd {
					u.User = url.UserPassword(u.User.Username(), "***")
				}
			}
			return u.String()
		}
	}
	parts := strings.Fields(dsn)
	for i, p := range parts {
		if strings.HasPrefix(strings.ToLower(p), "password=") {
			parts[i] = "password=***"
		}
	}
	return strings.Join(parts, " ")
}

// EmailHash 输出脱敏后的邮箱字符串：保留 local part 前 3 个字符与完整域名。
//
//	joineroz749@gmail.com → joi***@gmail.com
//	a@b.com               → a***@b.com   （不足 3 字符也补 ***）
//	没有 @ 的输入直接返回 ***
func EmailHash(email string) string {
	at := strings.LastIndex(email, "@")
	if at <= 0 {
		return "***"
	}
	local := email[:at]
	domain := email[at:]
	prefix := local
	if len(local) > 3 {
		prefix = local[:3]
	}
	return prefix + "***" + domain
}
