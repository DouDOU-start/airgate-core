package handler

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	appaccount "github.com/DouDOU-start/airgate-core/internal/app/account"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
)

// AccountHandler 上游账号管理 Handler。
//
// scheduler 用来读家族级限流冷却（Redis 侧的瞬态状态，不在 DB 里），
// 后台账号列表/详情会带上 family_cooldowns 字段。允许 nil 退化为不展示冷却信息。
type AccountHandler struct {
	service   *appaccount.Service
	scheduler *scheduler.Scheduler
}

// NewAccountHandler 创建 AccountHandler。sched 可为 nil（旧测试入口），
// 此时 family_cooldowns 字段会缺省为空。
func NewAccountHandler(service *appaccount.Service, sched *scheduler.Scheduler) *AccountHandler {
	return &AccountHandler{service: service, scheduler: sched}
}

// familyCooldownsFor 拉取指定账号在 Redis 上仍生效的家族冷却，转成 DTO。
// scheduler 为 nil 或没有冷却时返回 nil；不阻断主响应。
func (h *AccountHandler) familyCooldownsFor(ctx context.Context, accountID int) []dto.FamilyCooldownDTO {
	if h.scheduler == nil {
		return nil
	}
	entries := h.scheduler.ListFamilyCooldowns(ctx, accountID)
	if len(entries) == 0 {
		return nil
	}
	out := make([]dto.FamilyCooldownDTO, 0, len(entries))
	for _, e := range entries {
		out = append(out, dto.FamilyCooldownDTO{
			Family: e.Family,
			Until:  e.Until.UTC().Format(time.RFC3339),
			Reason: e.Reason,
		})
	}
	return out
}

func parseAccountID(raw string) (int, error) {
	return strconv.Atoi(raw)
}

func parseOptionalInt(raw string) *int {
	if raw == "" {
		return nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return nil
	}
	return &value
}

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

func (h *AccountHandler) handleError(logMessage, publicMessage string, err error) (int, string) {
	switch {
	case errors.Is(err, appaccount.ErrAccountNotFound):
		return 404, err.Error()
	case errors.Is(err, appaccount.ErrPluginNotFound):
		return 500, err.Error()
	case errors.Is(err, appaccount.ErrReauthRequired):
		// 这里的"需要重新授权"说的是**上游账号**（OAuth）的凭证失效，不是当前
		// 登录用户的 session。绝对不能返回 401——前端 HTTP 客户端有全局拦截，
		// 看到 401 会把当前管理员踹出登录页。用 422 语义最贴切：请求合法但
		// 因账号状态无法处理。
		return 422, err.Error()
	case errors.Is(err, appaccount.ErrModelRequired),
		errors.Is(err, appaccount.ErrQuotaRefreshUnsupported),
		errors.Is(err, appaccount.ErrInvalidDateRange):
		return 400, err.Error()
	default:
		slog.Error(logMessage, "error", err)
		return 500, publicMessage
	}
}
