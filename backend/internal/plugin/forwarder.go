package plugin

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/routing"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"
)

// Forwarder 请求转发器：认证 → 余额预检 → 调度 → 并发闸门 → 转发 → 判决 → 计费 → 记录。
type Forwarder struct {
	db          *ent.Client
	manager     *Manager
	scheduler   *scheduler.Scheduler
	concurrency *scheduler.ConcurrencyManager
	calculator  *billing.Calculator
	recorder    *billing.Recorder
}

// NewForwarder 创建转发器。
func NewForwarder(
	db *ent.Client,
	manager *Manager,
	sched *scheduler.Scheduler,
	concurrency *scheduler.ConcurrencyManager,
	calculator *billing.Calculator,
	recorder *billing.Recorder,
) *Forwarder {
	return &Forwarder{
		db:          db,
		manager:     manager,
		scheduler:   sched,
		concurrency: concurrency,
		calculator:  calculator,
		recorder:    recorder,
	}
}

// maxFailoverAttempts 最大 failover 次数（账号级失败后切换新账号上游调用的上限）。
const maxFailoverAttempts = 3

// queueWaitTimeout 所有账号 slot 都被占满时，请求最多排队等多久再放弃。
// 1 分钟对号池小 / 并发高的场景能把毛刺吸收掉；超过这个时长意味着号池真的不够用。
const queueWaitTimeout = 60 * time.Second

// queuePollInterval slot 未释放时的轮询间隔。
const queuePollInterval = 200 * time.Millisecond

// Forward 入口。失败时自动 failover 到其它账号，最多 maxFailoverAttempts 次。
//
// Middleware：OnForwardBegin 只在首次 attempt 调用（避免 failover 污染审计计数），
// OnForwardEnd 在最终一次 attempt（成功或放弃）触发，LIFO 降序。Begin DENY 会拒绝请求。
func (f *Forwarder) Forward(c *gin.Context) {
	state, ok := f.parseRequest(c)
	if !ok {
		return
	}
	if !f.checkBalance(c, state) {
		return
	}

	// 只读元信息快车道：插件本地合成响应，跳过整条账号 / 闸门 / failover 链路。
	if isMetadataOnlyPath(state.requestPath) {
		f.forwardMetadataOnly(c, state)
		return
	}

	releaseClientQuota := f.acquireClientQuota(c, state)
	if releaseClientQuota == nil {
		return // 429 已写
	}
	defer releaseClientQuota()

	routes := routesForAPIKey(state, routing.Requirements{
		NeedsImage: requestNeedsImage(state.requestPath, state.model),
	})
	if len(routes) == 0 {
		slog.Warn("没有可用候选分组", "platform", state.requestedPlatform, "model", state.model, "user_id", state.keyInfo.UserID)
		openAIError(c, http.StatusServiceUnavailable, "server_error", "no_available_route", "请求暂时无法完成，请稍后重试")
		return
	}

	var hardExclude []int
	var mwBag map[string]string
	beginCalled := false
	ctx := c.Request.Context()

	for _, route := range routes {
		state.selectedRoute = route
		state.keyInfo = keyInfoForRoute(state.keyInfo, route)

		softExclude := []int(nil)
		attempt := 0
		queueDeadline := time.Now().Add(queueWaitTimeout)

		for attempt < maxFailoverAttempts {
			exclude := make([]int, 0, len(hardExclude)+len(softExclude))
			exclude = append(exclude, hardExclude...)
			exclude = append(exclude, softExclude...)

			if err := f.pickAccount(c, state, exclude...); err != nil {
				if len(softExclude) > 0 && time.Now().Before(queueDeadline) {
					softExclude = nil
					select {
					case <-ctx.Done():
						return
					case <-time.After(queuePollInterval):
					}
					continue
				}
				slog.Warn("候选分组账户调度失败",
					"platform", state.requestedPlatform,
					"model", state.model,
					"group_id", route.GroupID,
					"effective_rate", route.EffectiveRate,
					"hard_excluded", hardExclude,
					"soft_excluded", softExclude,
					"error", err)
				break
			}

			accountID := state.account.ID
			releaseAccountSlot, ok := f.acquireAccountSlot(c, state)
			if !ok {
				softExclude = append(softExclude, accountID)
				continue
			}

			if !beginCalled {
				allowed, bag := f.runForwardBeginChain(c, state)
				beginCalled = true
				if !allowed {
					f.scheduler.DecrementRPM(ctx, accountID)
					releaseAccountSlot()
					return
				}
				mwBag = bag
			}

			execution := f.callPlugin(c, state)
			attempt++

			if f.canFailover(c, state, execution) {
				slog.Warn("账号调用失败，尝试 failover",
					"plugin", state.plugin.Name,
					"group_id", route.GroupID,
					"effective_rate", route.EffectiveRate,
					"account_id", accountID,
					"attempt", attempt,
					"kind", execution.outcome.Kind,
					"upstream_status", execution.outcome.Upstream.StatusCode,
					"duration_ms", execution.duration.Milliseconds(),
					"reason", judgmentReason(execution),
					"error", execution.err)
				releaseAccountSlot()
				f.applyOutcome(ctx, state, execution)

				if execution.outcome.Kind.IsAccountFault() {
					hardExclude = append(hardExclude, accountID)
				} else {
					softExclude = append(softExclude, accountID)
				}
				continue
			}

			f.runForwardEndChain(c, state, execution, mwBag)
			f.writeResult(c, state, execution)
			releaseAccountSlot()
			return
		}

		slog.Warn("候选分组 failover 尝试失败",
			"plugin", state.plugin.Name,
			"platform", state.requestedPlatform,
			"model", state.model,
			"group_id", route.GroupID,
			"effective_rate", route.EffectiveRate,
			"attempts", attempt)
	}

	slog.Error("所有候选路由均失败",
		"plugin", state.plugin.Name,
		"platform", state.requestedPlatform,
		"model", state.model,
		"tried_accounts", hardExclude)
	openAIError(c, 503, "server_error", "all_routes_failed", "请求暂时无法完成，请稍后重试")
}

func routesForAPIKey(state *forwardState, requirements routing.Requirements) []routing.Candidate {
	if state == nil || state.keyInfo == nil {
		return nil
	}
	if !apiKeyGroupMatchesRequirements(state.keyInfo, requirements) {
		return nil
	}
	return []routing.Candidate{keyInfoRoute(state.keyInfo)}
}

func apiKeyGroupMatchesRequirements(keyInfo *auth.APIKeyInfo, requirements routing.Requirements) bool {
	if keyInfo == nil {
		return false
	}
	if requirements.NeedsImage && strings.EqualFold(keyInfo.GroupPlatform, "openai") {
		return pluginSettingEnabledForKey(keyInfo.GroupPluginSettings, "openai", "image_enabled")
	}
	return true
}

func pluginSettingEnabledForKey(settings map[string]map[string]string, plugin, key string) bool {
	for pluginName, kv := range settings {
		if !strings.EqualFold(pluginName, plugin) {
			continue
		}
		for k, v := range kv {
			if strings.EqualFold(k, key) {
				return strings.EqualFold(strings.TrimSpace(v), "true")
			}
		}
	}
	return false
}

func keyInfoRoute(keyInfo *auth.APIKeyInfo) routing.Candidate {
	return routing.Candidate{
		GroupID:                keyInfo.GroupID,
		Platform:               keyInfo.GroupPlatform,
		EffectiveRate:          billing.ResolveBillingRateForGroup(keyInfo.UserGroupRates, keyInfo.GroupID, keyInfo.GroupRateMultiplier),
		GroupRateMultiplier:    keyInfo.GroupRateMultiplier,
		GroupServiceTier:       keyInfo.GroupServiceTier,
		GroupForceInstructions: keyInfo.GroupForceInstructions,
		GroupPluginSettings:    clonePluginSettingsForKey(keyInfo.GroupPluginSettings),
	}
}

func clonePluginSettingsForKey(in map[string]map[string]string) map[string]map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]map[string]string, len(in))
	for plugin, settings := range in {
		if len(settings) == 0 {
			continue
		}
		out[plugin] = make(map[string]string, len(settings))
		for k, v := range settings {
			out[plugin][k] = v
		}
	}
	return out
}

func keyInfoForRoute(base *auth.APIKeyInfo, route routing.Candidate) *auth.APIKeyInfo {
	info := *base
	info.GroupID = route.GroupID
	info.GroupPlatform = route.Platform
	info.GroupRateMultiplier = route.GroupRateMultiplier
	info.GroupServiceTier = route.GroupServiceTier
	info.GroupForceInstructions = route.GroupForceInstructions
	info.GroupPluginSettings = route.GroupPluginSettings
	return &info
}

// canFailover 是否允许换账号重试。
// 流式已写入 → 不可；err 非 nil（插件自身崩）→ 可；其余由 Kind.ShouldFailover() 决定。
func (f *Forwarder) canFailover(c *gin.Context, state *forwardState, execution forwardExecution) bool {
	if state.stream && c.Writer.Written() {
		return false
	}
	if execution.err != nil {
		return true
	}
	return execution.outcome.Kind.ShouldFailover()
}

// callPlugin 把请求发给插件。
func (f *Forwarder) callPlugin(c *gin.Context, state *forwardState) forwardExecution {
	outcome, err := state.plugin.Gateway.Forward(c.Request.Context(), buildPluginRequest(c, state))
	return forwardExecution{
		outcome:  outcome,
		err:      err,
		duration: time.Since(state.startedAt),
	}
}
