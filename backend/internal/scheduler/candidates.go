package scheduler

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/DouDOU-start/airgate-core/ent"
)

// RouteCandidate 是账号路由的只读、脱敏摘要。它不包含凭证、代理或上游地址，
// 仅供账号选择前的通用策略/观察型扩展识别当前是否存在基础可调度账号。
type RouteCandidate struct {
	ID       int
	Platform string
	Type     string
	Priority int
	State    string
}

// ListRouteCandidates 复用 Scheduler 的 route cache，返回模型路由与账号能力均匹配、
// 且账号基础状态当前为 Normal 的候选摘要。这里刻意不争抢 RPM/并发槽、不建立
// sticky session，也不修改账号状态；最终可用性仍以 SelectAccount 为准。
func (s *Scheduler) ListRouteCandidates(ctx context.Context, platform string, models []string, groupID int, req AccountRequirements) ([]RouteCandidate, error) {
	if s == nil {
		return nil, fmt.Errorf("scheduler 未初始化")
	}
	if len(models) == 0 {
		models = []string{""}
	}

	now := time.Now()
	seen := make(map[int]struct{})
	result := make([]RouteCandidate, 0)
	for _, model := range models {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		accounts, err := s.routeAccounts(ctx, platform, model, groupID)
		if err != nil {
			return nil, err
		}
		accounts = filterAccountsByRequirements(accounts, req)
		if filter, ok := s.accountFilters[platform]; ok {
			accounts = filter(accounts, model)
		}
		for _, account := range accounts {
			candidate, ok := routeCandidateSnapshot(account, now)
			if !ok {
				continue
			}
			if _, exists := seen[account.ID]; exists {
				continue
			}
			seen[account.ID] = struct{}{}
			result = append(result, candidate)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Priority != result[j].Priority {
			return result[i].Priority > result[j].Priority
		}
		return result[i].ID < result[j].ID
	})
	return result, nil
}

func routeCandidateSnapshot(account *ent.Account, now time.Time) (RouteCandidate, bool) {
	if account == nil || SchedulabilityOf(account, now) != Normal {
		return RouteCandidate{}, false
	}
	return RouteCandidate{
		ID:       account.ID,
		Platform: account.Platform,
		Type:     account.Type,
		Priority: account.Priority,
		State:    "active",
	}, true
}
