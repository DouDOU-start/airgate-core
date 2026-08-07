package handler

import (
	"errors"
	"log/slog"
	"strconv"
	"strings"

	appaccount "github.com/DouDOU-start/airgate-core/internal/app/account"
)

// AccountHandler 上游账号管理 Handler。
type AccountHandler struct {
	service *appaccount.Service
}

// NewAccountHandler 创建 AccountHandler。
func NewAccountHandler(service *appaccount.Service) *AccountHandler {
	return &AccountHandler{service: service}
}

func (h *AccountHandler) handleError(logMessage, publicMessage string, err error) (int, string) {
	switch {
	case errors.Is(err, appaccount.ErrAccountNotFound):
		return 404, err.Error()
	case errors.Is(err, appaccount.ErrInvalidState),
		errors.Is(err, appaccount.ErrInvalidCredentials),
		errors.Is(err, appaccount.ErrEmptyCredentials),
		errors.Is(err, appaccount.ErrUnsupportedPlatform),
		errors.Is(err, appaccount.ErrUsageNotSupported),
		errors.Is(err, appaccount.ErrTooManySortCandidates):
		return 400, err.Error()
	default:
		slog.Error(logMessage, "error", err)
		return 500, publicMessage
	}
}

// handleImportError 导入/换票失败：始终把 err 原文回给前端（含上游 HTTP body），
// 避免 handleError 默认吞成泛化「导入失败」。
func (h *AccountHandler) handleImportError(logMessage string, err error) (int, string) {
	if err == nil {
		return 500, "导入失败"
	}
	msg := err.Error()
	var upstreamStatus interface{ StatusCode() int }
	if errors.As(err, &upstreamStatus) {
		statusCode := upstreamStatus.StatusCode()
		if statusCode >= 400 && statusCode < 500 {
			// 不直接透传 401，避免前端把上游凭证无效误判成管理员登录失效。
			slog.Warn(logMessage, "upstream_status", statusCode, "error", err)
			return 400, msg
		}
		if statusCode >= 500 {
			slog.Error(logMessage, "upstream_status", statusCode, "error", err)
			return 502, msg
		}
	}
	switch {
	case errors.Is(err, appaccount.ErrAccountNotFound):
		return 404, msg
	case errors.Is(err, appaccount.ErrUnsupportedPlatform),
		errors.Is(err, appaccount.ErrEmptyCredentials),
		errors.Is(err, appaccount.ErrInvalidCredentials):
		return 400, msg
	case strings.Contains(msg, "上游 HTTP"),
		strings.Contains(msg, "session 刷新失败"),
		strings.Contains(msg, "刷新 token"),
		strings.Contains(msg, "请求 token"),
		strings.Contains(msg, "请求 session"),
		strings.Contains(msg, "缺少 project_id"),
		strings.Contains(msg, "missing project_id"),
		strings.Contains(msg, "invalid_grant"),
		strings.Contains(msg, "解析"):
		// 上游鉴权/业务拒绝或可诊断错误：4xx 语义
		slog.Warn(logMessage, "error", err)
		return 400, msg
	default:
		slog.Error(logMessage, "error", err)
		// 网络等：502 + 原文
		return 502, msg
	}
}

// parseOptionalBool 解析可选布尔查询参数。
func parseOptionalBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

// parseIDList 解析逗号分隔的整数列表（如 "1,2,3"），忽略空项与非法项。
func parseIDList(raw string) []int {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	ids := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if v, err := strconv.Atoi(p); err == nil {
			ids = append(ids, v)
		}
	}
	return ids
}
