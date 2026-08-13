// Package accountreg 提供账号路径的内存调度注册表：
// 解密后的凭证、代理 URL、priority/weight/state/groups/models 常驻内存，
// 与 channel registry 并列参与 pipeline 统一调度。
//
// 依赖约束：本包禁止 import ent / internal/app。
package accountreg

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// 状态常量（与 ent schema account.state 一致）。
const (
	StateActive      = "active"
	StateRateLimited = "rate_limited"
	StateDegraded    = "degraded"
	StateDisabled    = "disabled"
)

// RateLimitProbeDecision 是账号进入限流恢复探测前的原子判定。
type RateLimitProbeDecision uint8

const (
	// RateLimitProbeBlocked 表示账号不存在、已禁用，或当前已有探测/仍处退避期。
	RateLimitProbeBlocked RateLimitProbeDecision = iota
	// RateLimitProbeNotNeeded 表示账号当前不是限流状态，可按普通请求处理。
	RateLimitProbeNotNeeded
	// RateLimitProbeAcquired 表示调用方独占了本轮限流恢复探测资格。
	RateLimitProbeAcquired
)

// RateLimitProbeLease 标识一次具体的限流恢复探测。结算时必须携带原租约，避免旧
// 请求的迟到结果误结算账号后续新一轮探测。
type RateLimitProbeLease uint64

// ErrNoAvailableAccount 当前分组/模型下无可调度账号。
var ErrNoAvailableAccount = errors.New("无可用账号")

// Snapshot 账号运行时快照（解密后）。
//
// Pick / ListCandidates 返回的快照为只读视图：copy-on-write 更新，
// 调用方持有的指针不会被并发修改，禁止就地改写。
type Snapshot struct {
	ID   int
	Name string
	// Email 是账号表冗余邮箱快照，供调度审计展示；凭证中缺少 email 时仍可追溯。
	Email          string
	Platform       string
	Type           string
	Credentials    map[string]string
	ProxyURL       string
	Priority       int
	Weight         int
	MaxConcurrency int
	MaxRPM         int
	RateMultiplier float64
	State          string
	StateUntil     *time.Time
	ErrorMsg       string
	UpstreamIsPool bool
	// Models 可服务对外模型名；空则用平台默认（由 Loader 填好）。
	Models map[string]struct{}
	// ModelMapping 将对外模型名映射为上游模型名。
	ModelMapping map[string]string
	// GroupIDs 绑定分组；空集合不参与任何分组调度。
	GroupIDs map[int]struct{}

	// 以下字段仅存在于运行时快照，不由 Loader/Persister 读写。恢复探测必须先原子
	// claim；失败后按指数退避隐藏账号，避免插件跨请求反复放行同一限流账号。
	rateLimitProbeInFlight   bool
	rateLimitProbeLease      RateLimitProbeLease
	rateLimitProbeFailures   int
	rateLimitProbeBlockUntil time.Time
}

// ResolveModel 返回账号上游实际使用的模型名。
func (s *Snapshot) ResolveModel(model string) string {
	if s == nil {
		return model
	}
	return ResolveModelMapping(s.ModelMapping, model)
}

// ResolveModelMapping 优先精确匹配，再选择字面字符最多的通配规则。
func ResolveModelMapping(mapping map[string]string, model string) string {
	if mapped := strings.TrimSpace(mapping[model]); mapped != "" {
		return mapped
	}
	bestScore, best := -1, ""
	for pattern, mapped := range mapping {
		pattern, mapped = strings.TrimSpace(pattern), strings.TrimSpace(mapped)
		if pattern == "" || mapped == "" || !strings.Contains(pattern, "*") {
			continue
		}
		expression := "^" + strings.ReplaceAll(regexp.QuoteMeta(pattern), `\*`, ".*") + "$"
		if matched, err := regexp.MatchString(expression, model); err == nil && matched {
			if score := len(strings.ReplaceAll(pattern, "*", "")); score > bestScore {
				bestScore, best = score, mapped
			}
		}
	}
	if best != "" {
		return best
	}
	return model
}

// EffectiveCostRatio 账号成本倍率；无效时回退 1。
func (s *Snapshot) EffectiveCostRatio() float64 {
	if s != nil && s.RateMultiplier > 0 {
		return s.RateMultiplier
	}
	return 1
}

// IsSchedulable 当前是否可参与调度（含 state_until 到期自动恢复语义，只读判定）。
func (s *Snapshot) IsSchedulable(now time.Time) bool {
	if s == nil {
		return false
	}
	switch s.State {
	case StateActive:
		return true
	case StateDegraded:
		// degraded 仍可调度，但优先级降档由调用方处理；到期视为 active。
		if s.StateUntil != nil && !s.StateUntil.After(now) {
			return true
		}
		return true
	case StateRateLimited:
		// 限流期内不可调度；到期后可调度（内存侧 MarkActive 由调用方触发亦可）。
		if s.StateUntil == nil || !s.StateUntil.After(now) {
			return true
		}
		return false
	default: // disabled 等
		return false
	}
}

// EffectivePriority 调度用优先级：degraded 且未到期时压到最低档。
func (s *Snapshot) EffectivePriority(now time.Time) int {
	if s == nil {
		return 0
	}
	if s.State == StateDegraded && s.StateUntil != nil && s.StateUntil.After(now) {
		return 0
	}
	return s.Priority
}

// ModelMappingFromExtra 解析 extra.model_mapping，过滤空映射。
func ModelMappingFromExtra(extra map[string]any) map[string]string {
	if extra == nil {
		return nil
	}
	raw, ok := extra["model_mapping"]
	if !ok || raw == nil {
		return nil
	}
	out := map[string]string{}
	switch values := raw.(type) {
	case map[string]string:
		for source, target := range values {
			source, target = strings.TrimSpace(source), strings.TrimSpace(target)
			if source != "" && target != "" {
				out[source] = target
			}
		}
	case map[string]any:
		for source, value := range values {
			target, ok := value.(string)
			if !ok {
				continue
			}
			source, target = strings.TrimSpace(source), strings.TrimSpace(target)
			if source != "" && target != "" {
				out[source] = target
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Loader 全量加载账号快照（由 app/server 适配：解密凭证、解析代理 URL、填充模型）。
type Loader interface {
	LoadAllForAccountRegistry(ctx context.Context) ([]Snapshot, error)
}

// Persister 账号状态异步落库（由 app/server 适配）。
type Persister interface {
	PersistAccountState(ctx context.Context, accountID int, state string, stateUntil *time.Time, errMsg string) error
}

// CredentialPersister 刷新后的凭证落库（可选）。
type CredentialPersister interface {
	PersistAccountCredentials(ctx context.Context, accountID int, credentials map[string]string) error
}

const (
	lazyReloadTimeout            = 5 * time.Second
	lazyReloadInterval           = 3 * time.Second
	rateLimitProbeInitialBackoff = 30 * time.Second
	rateLimitProbeMaximumBackoff = 15 * time.Minute
)

// Registry 账号注册表。
type Registry struct {
	loader    Loader
	persister Persister
	credSink  CredentialPersister

	mu       sync.RWMutex
	accounts map[int]*Snapshot
	index    map[accountCandidateKey][]int

	loadedOnce     atomic.Bool
	lazyMu         sync.Mutex
	reloadMu       sync.Mutex
	lastLazyReload time.Time

	randFn         func(n int) int
	persistTimeout time.Duration
	stateSeq       atomic.Uint64
	routeVersion   atomic.Uint64
	probeSeq       atomic.Uint64

	persistMu      sync.Mutex
	persistPending map[int]statePersistRequest
	persistRunning map[int]bool
	persistLatest  map[int]uint64

	// modelCooldown (账号, 模型) 级 429 运行时冷却，见 model_cooldown.go。
	modelCooldown modelCooldownState
}

type accountCandidateKey struct {
	groupID int
	model   string
}

type statePersistRequest struct {
	accountID int
	state     string
	until     *time.Time
	errMsg    string
	version   uint64
}

// New 创建账号注册表。loader 必填；persister / credSink 可为 nil。
func New(loader Loader, persister Persister) *Registry {
	return &Registry{
		loader:         loader,
		persister:      persister,
		accounts:       map[int]*Snapshot{},
		index:          map[accountCandidateKey][]int{},
		randFn:         rand.IntN,
		persistTimeout: 5 * time.Second,
		persistPending: make(map[int]statePersistRequest),
		persistRunning: make(map[int]bool),
		persistLatest:  make(map[int]uint64),
	}
}

// SetCredentialPersister 注入凭证落库实现（refresh 后可选）。
func (r *Registry) SetCredentialPersister(p CredentialPersister) {
	if r == nil {
		return
	}
	r.credSink = p
}

// Reload 全量替换快照。仍处于同一轮限流的账号保留仅运行时探测门闩，避免账号或
// 代理配置变更触发重载后，已经失败的超额探测立即获得新资格。
func (r *Registry) Reload(ctx context.Context) error {
	r.reloadMu.Lock()
	defer r.reloadMu.Unlock()

	// Loader 可能执行较慢，不能持有主锁。记录 copy-on-write 指针作为基线，装载
	// 完成后若指针变化，说明期间有状态、凭证或探测门闩更新，必须保留新值。
	r.mu.RLock()
	baseline := make(map[int]*Snapshot, len(r.accounts))
	for id, snap := range r.accounts {
		baseline[id] = snap
	}
	r.mu.RUnlock()

	snaps, err := r.loader.LoadAllForAccountRegistry(ctx)
	if err != nil {
		return err
	}
	next := make(map[int]*Snapshot, len(snaps))
	for i := range snaps {
		snap := snaps[i]
		// 防御性拷贝 map，避免 loader 侧后续修改污染。
		snap.Credentials = cloneStringMap(snap.Credentials)
		snap.Models = cloneStructMap(snap.Models)
		snap.GroupIDs = cloneIntSet(snap.GroupIDs)
		// Loader 不负责运行时门闩；即使包内测试替身填入，也不能直接注入。
		resetRateLimitProbe(&snap)
		next[snap.ID] = &snap
	}
	// 索引只依赖 GroupIDs/Models 等成员字段，锁内合并仅改运行时状态，
	// 在写锁外构建后整体换入，避免 O(账号×分组×模型) 构建期间阻塞全部请求。
	index := buildAccountCandidateIndex(next)
	r.mu.Lock()
	for id, snap := range next {
		old := r.accounts[id]
		if old == nil {
			continue
		}
		before := baseline[id]
		if before != old {
			loadedStateUntil := cloneTime(snap.StateUntil)
			if before == nil || !sameStringMap(before.Credentials, old.Credentials) {
				snap.Credentials = old.Credentials
			}
			if before == nil || !sameAccountRuntimeState(before, old) {
				preserveConcurrentState(snap, old)
				mergeRateLimitProbeDeadline(snap, loadedStateUntil)
				continue
			}
			if !sameRateLimitProbeRuntime(before, old) && old.State == StateRateLimited && snap.State == StateRateLimited {
				copyRateLimitProbeRuntime(snap, old)
				mergeRateLimitProbeDeadline(snap, loadedStateUntil)
			}
			continue
		}
		if old.State == StateRateLimited && snap.State == StateRateLimited {
			copyRateLimitProbeRuntime(snap, old)
			mergeRateLimitProbeDeadline(snap, snap.StateUntil)
		}
	}
	r.accounts = next
	r.index = index
	r.routeVersion.Add(1)
	r.mu.Unlock()
	r.loadedOnce.Store(true)
	return nil
}

func (r *Registry) ensureLoaded() {
	if r.loadedOnce.Load() {
		return
	}
	if !r.lazyMu.TryLock() {
		return
	}
	defer r.lazyMu.Unlock()
	if r.loadedOnce.Load() {
		return
	}
	now := time.Now()
	if now.Sub(r.lastLazyReload) < lazyReloadInterval {
		return
	}
	r.lastLazyReload = now
	ctx, cancel := context.WithTimeout(context.Background(), lazyReloadTimeout)
	defer cancel()
	if err := r.Reload(ctx); err != nil {
		slog.Warn("account_registry_lazy_reload_failed", "error", err)
	}
}

// ListCandidates 返回可调度账号候选（已过滤 state / group / model / exclude）。
// 返回切片内指针只读。degraded 账号包含在内（EffectivePriority 已压档）。
func (r *Registry) ListCandidates(groupID int, model string, exclude []int) []*Snapshot {
	return r.listCandidates(groupID, model, exclude, nil, false)
}

// ListCandidatesAllowRateLimited 返回普通可调度候选，并额外允许调用方显式列出的
// rate_limited Codex OAuth 账号。该入口仅供已经通过 Relay Hook 决策校验的单次
// 路由使用；其他平台、API Key、disabled、分组不匹配和模型不匹配仍会被过滤。
func (r *Registry) ListCandidatesAllowRateLimited(groupID int, model string, exclude, allowRateLimited []int) []*Snapshot {
	allowed := make(map[int]struct{}, len(allowRateLimited))
	for _, id := range allowRateLimited {
		if id > 0 {
			allowed[id] = struct{}{}
		}
	}
	return r.listCandidates(groupID, model, exclude, allowed, false)
}

// ListRelayHookCandidates 返回对 Relay Hook 可见的账号目录候选。
// 与普通调度不同，它保留 rate_limited 账号供受信插件作显式单次决策；
// disabled、分组不匹配和模型不匹配账号仍不可见。
func (r *Registry) ListRelayHookCandidates(groupID int, model string) []*Snapshot {
	return r.listCandidates(groupID, model, nil, nil, true)
}

func (r *Registry) listCandidates(
	groupID int,
	model string,
	exclude []int,
	allowRateLimited map[int]struct{},
	includeAllRateLimited bool,
) []*Snapshot {
	if r == nil {
		return nil
	}
	r.ensureLoaded()
	now := time.Now()

	r.mu.RLock()
	defer r.mu.RUnlock()

	ids := r.index[accountCandidateKey{groupID: groupID, model: model}]
	out := make([]*Snapshot, 0, len(ids))
	for _, id := range ids {
		a := r.accounts[id]
		if a == nil || containsInt(exclude, a.ID) {
			continue
		}
		if a.State == StateDisabled {
			continue
		}
		// 探测进行中或退避未结束时，所有候选入口一律隐藏。这样即使插件携带
		// 先前生成的显式 allow，Core 也不会再次放行该账号。
		if a.State == StateRateLimited && a.rateLimitProbeBlocked(now) {
			continue
		}
		if !a.IsSchedulable(now) {
			_, explicitlyAllowed := allowRateLimited[a.ID]
			if a.State != StateRateLimited ||
				(!includeAllRateLimited && (!explicitlyAllowed || !isCodexOAuth(a))) {
				continue
			}
		}
		// (账号, 模型) 级冷却：该模型限流期内隐藏候选，其余模型不受影响。
		if r.isModelRateLimited(a.ID, model, now) {
			continue
		}
		out = append(out, a)
	}
	return out
}

func buildAccountCandidateIndex(accounts map[int]*Snapshot) map[accountCandidateKey][]int {
	index := make(map[accountCandidateKey][]int)
	for id, account := range accounts {
		if account == nil || len(account.Models) == 0 || len(account.GroupIDs) == 0 {
			continue
		}
		for groupID := range account.GroupIDs {
			for model := range account.Models {
				key := accountCandidateKey{groupID: groupID, model: model}
				index[key] = append(index[key], id)
			}
		}
	}
	for key := range index {
		sort.Ints(index[key])
	}
	return index
}

func containsInt(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// RouteCandidateIDs 返回指定路由分片的不可变成员索引。
// Reload 会整体替换索引映射，因此返回的切片在解锁后仍然有效。
func (r *Registry) RouteCandidateIDs(groupID int, model string) (uint64, []int) {
	if r == nil {
		return 0, nil
	}
	r.ensureLoaded()
	r.mu.RLock()
	ids := r.index[accountCandidateKey{groupID: groupID, model: model}]
	version := r.routeVersion.Load()
	r.mu.RUnlock()
	return version, ids
}

// RouteCandidate 根据当前写时复制运行时状态解析并重新校验单个索引账号。
func (r *Registry) RouteCandidate(id, groupID int, model string, now time.Time) (*Snapshot, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	account := r.accounts[id]
	if account == nil || account.State == StateDisabled || account.rateLimitProbeBlocked(now) || !account.IsSchedulable(now) {
		r.mu.RUnlock()
		return nil, false
	}
	_, groupOK := account.GroupIDs[groupID]
	_, modelOK := account.Models[model]
	r.mu.RUnlock()
	if groupOK && modelOK && r.isModelRateLimited(id, model, now) {
		return nil, false
	}
	return account, groupOK && modelOK
}

func (r *Registry) RouteVersion() uint64 {
	if r == nil {
		return 0
	}
	return r.routeVersion.Load()
}

func isCodexOAuth(account *Snapshot) bool {
	return account != nil &&
		strings.EqualFold(strings.TrimSpace(account.Platform), "codex") &&
		strings.EqualFold(strings.TrimSpace(account.Type), "oauth")
}

// ModelsForGroup 返回指定分组下账号可服务的模型名并集（字典序）。
//
// 口径对齐调度可见性：绑定该分组、非 disabled、Models 非空。
// rate_limited / degraded 仍列入目录——临时状态不掩盖「分组下有哪些模型」；
// 真正能否立刻调度由 ListCandidates / IsSchedulable 另行判定。
func (r *Registry) ModelsForGroup(groupID int) []string {
	if r == nil {
		return nil
	}
	r.ensureLoaded()

	r.mu.RLock()
	set := map[string]struct{}{}
	for _, a := range r.accounts {
		if a.State == StateDisabled {
			continue
		}
		if _, ok := a.GroupIDs[groupID]; !ok {
			continue
		}
		if len(a.Models) == 0 {
			continue
		}
		for m := range a.Models {
			set[m] = struct{}{}
		}
	}
	r.mu.RUnlock()

	out := make([]string, 0, len(set))
	for m := range set {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// Pick 单独从账号池选一个（测试/兼容用）；统一调度请用 pipeline 混合选路。
func (r *Registry) Pick(groupID int, model string, exclude []int) (*Snapshot, error) {
	cands := r.ListCandidates(groupID, model, exclude)
	if len(cands) == 0 {
		return nil, ErrNoAvailableAccount
	}
	now := time.Now()
	// 最高 effective priority 档。
	best := -1
	var tier []*Snapshot
	for _, a := range cands {
		p := a.EffectivePriority(now)
		if p > best {
			best = p
			tier = tier[:0]
		}
		if p == best {
			tier = append(tier, a)
		}
	}
	sort.Slice(tier, func(i, j int) bool { return tier[i].ID < tier[j].ID })
	total := 0
	for _, a := range tier {
		total += a.Weight + 10
	}
	n := r.randFn(total)
	for _, a := range tier {
		n -= a.Weight + 10
		if n < 0 {
			return a, nil
		}
	}
	return tier[len(tier)-1], nil
}

// Snapshot 按 ID 取只读快照。
func (r *Registry) Snapshot(id int) (*Snapshot, bool) {
	r.ensureLoaded()
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.accounts[id]
	return a, ok
}

// BeginRateLimitProbe 原子判断账号状态并 claim 一次限流恢复探测。
//
// active / degraded 无需探测；rate_limited 在没有在途探测且退避已结束时仅允许一个
// 调用方取得资格；disabled、未知状态和不存在的账号均拒绝。
func (r *Registry) BeginRateLimitProbe(accountID int) (RateLimitProbeDecision, RateLimitProbeLease) {
	if r == nil {
		return RateLimitProbeBlocked, 0
	}
	r.ensureLoaded()
	now := time.Now()

	// 快路径：绝大多数请求的账号处于 active/degraded（无需探测、不改状态），
	// 用读锁判定即返回，避免每个账号请求都在全局写锁上串行。
	// 状态在读写锁之间可能变化，写锁内会重新判定，不影响正确性。
	r.mu.RLock()
	fast, ok := r.accounts[accountID]
	if !ok || fast == nil {
		r.mu.RUnlock()
		return RateLimitProbeBlocked, 0
	}
	if fast.State == StateActive || fast.State == StateDegraded {
		r.mu.RUnlock()
		return RateLimitProbeNotNeeded, 0
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	old, ok := r.accounts[accountID]
	if !ok || old == nil {
		return RateLimitProbeBlocked, 0
	}
	switch old.State {
	case StateActive, StateDegraded:
		return RateLimitProbeNotNeeded, 0
	case StateRateLimited:
		if old.rateLimitProbeBlocked(now) {
			return RateLimitProbeBlocked, 0
		}
		lease := RateLimitProbeLease(r.probeSeq.Add(1))
		next := *old
		next.rateLimitProbeInFlight = true
		next.rateLimitProbeLease = lease
		r.accounts[accountID] = &next
		return RateLimitProbeAcquired, lease
	default:
		return RateLimitProbeBlocked, 0
	}
}

// CancelRateLimitProbe 归还尚未触达上游的探测资格，例如本地并发/RPM 槽获取失败。
func (r *Registry) CancelRateLimitProbe(accountID int, lease RateLimitProbeLease) {
	if r == nil {
		return
	}
	r.mutateRuntime(accountID, func(a *Snapshot) bool {
		if !a.hasRateLimitProbeLease(lease) {
			return false
		}
		a.rateLimitProbeInFlight = false
		a.rateLimitProbeLease = 0
		return true
	})
}

// MarkRateLimitProbeFailed 记录已 claim 探测仍被限流。退避从 30 秒开始按失败次数
// 翻倍，最高 15 分钟；最终截止时间不会早于上游 Retry-After 或当前 StateUntil。
func (r *Registry) MarkRateLimitProbeFailed(accountID int, lease RateLimitProbeLease, retryAfter time.Duration, reason string) time.Time {
	if r == nil {
		return time.Time{}
	}
	now := time.Now()
	snap, version := r.mutateState(accountID, func(a *Snapshot) bool {
		if !a.hasRateLimitProbeLease(lease) {
			return false
		}
		a.rateLimitProbeFailures++
		until := now.Add(rateLimitProbeBackoff(a.rateLimitProbeFailures))
		if retryAfter > 0 {
			until = laterTime(until, now.Add(retryAfter))
		}
		if a.StateUntil != nil {
			until = laterTime(until, *a.StateUntil)
		}
		a.rateLimitProbeInFlight = false
		a.rateLimitProbeLease = 0
		a.rateLimitProbeBlockUntil = until
		a.State = StateRateLimited
		a.StateUntil = cloneTime(&until)
		a.ErrorMsg = reason
		return true
	})
	if snap == nil || version == 0 {
		return time.Time{}
	}
	r.persistAsync(accountID, StateRateLimited, snap.StateUntil, reason, version)
	return snap.rateLimitProbeBlockUntil
}

// MarkRateLimitProbeSucceeded 仅允许当前在途的限流探测恢复账号。没有 claim 的旧请求
// 不能借此覆盖较新的限流状态。
func (r *Registry) MarkRateLimitProbeSucceeded(accountID int, lease RateLimitProbeLease) {
	r.markActiveWhen(accountID, func(a *Snapshot) bool {
		return a.hasRateLimitProbeLease(lease)
	})
}

// MarkActiveIfNotRateLimited 处理普通请求成功：当前已经变成 rate_limited/disabled 时
// 保持新状态，防止较早发出的 active 请求晚到成功后错误清除限流。
func (r *Registry) MarkActiveIfNotRateLimited(accountID int) {
	if r == nil {
		return
	}
	r.mu.RLock()
	account := r.accounts[accountID]
	if account == nil || account.State == StateRateLimited || account.State == StateDisabled ||
		(account.State == StateActive && account.StateUntil == nil && account.ErrorMsg == "" && account.rateLimitProbeClean()) {
		r.mu.RUnlock()
		return
	}
	r.mu.RUnlock()
	r.markActiveWhen(accountID, func(a *Snapshot) bool {
		return a.State == StateActive || a.State == StateDegraded
	})
}

// MarkRateLimited 标记限流（内存 + 异步落库）。
func (r *Registry) MarkRateLimited(accountID int, until time.Time, reason string) {
	snap, version := r.mutateState(accountID, func(a *Snapshot) bool {
		wasRateLimited := a.State == StateRateLimited
		// 同一轮限流只延长、不缩短已有冷却；探测失败门闩也不得被普通 429 清除。
		if wasRateLimited {
			if a.StateUntil != nil {
				until = laterTime(until, *a.StateUntil)
			}
			if a.rateLimitProbeFailures > 0 {
				until = laterTime(until, a.rateLimitProbeBlockUntil)
				a.rateLimitProbeBlockUntil = until
			}
		} else {
			// 从非限流状态首次进入新一轮限流，立即提供一次恢复探测资格。
			resetRateLimitProbe(a)
		}
		if wasRateLimited && sameTime(a.StateUntil, &until) && a.ErrorMsg == reason {
			return false
		}
		a.State = StateRateLimited
		a.StateUntil = cloneTime(&until)
		a.ErrorMsg = reason
		return true
	})
	if snap == nil || version == 0 {
		return
	}
	r.persistAsync(accountID, StateRateLimited, &until, reason, version)
}

// MarkDisabled 标记禁用（凭证失效等）。
func (r *Registry) MarkDisabled(accountID int, reason string) {
	snap, version := r.mutateState(accountID, func(a *Snapshot) bool {
		if a.State == StateDisabled && a.StateUntil == nil && a.ErrorMsg == reason && a.rateLimitProbeClean() {
			return false
		}
		a.State = StateDisabled
		a.StateUntil = nil
		a.ErrorMsg = reason
		resetRateLimitProbe(a)
		return true
	})
	if snap == nil || version == 0 {
		return
	}
	r.persistAsync(accountID, StateDisabled, nil, reason, version)
}

// MarkActive 恢复 active。
func (r *Registry) MarkActive(accountID int) {
	r.markActiveWhen(accountID, func(*Snapshot) bool { return true })
}

// MarkDegraded 软降级（upstream_is_pool 抖动）。
func (r *Registry) MarkDegraded(accountID int, until time.Time, reason string) {
	untilCopy := until
	snap, version := r.mutateState(accountID, func(a *Snapshot) bool {
		if a.State == StateDegraded && sameTime(a.StateUntil, &until) && a.ErrorMsg == reason && a.rateLimitProbeClean() {
			return false
		}
		a.State = StateDegraded
		a.StateUntil = &untilCopy
		a.ErrorMsg = reason
		resetRateLimitProbe(a)
		return true
	})
	if snap == nil || version == 0 {
		return
	}
	r.persistAsync(accountID, StateDegraded, &until, reason, version)
}

// UpdateCredentials 更新内存中的凭证（refresh 后）；可选异步落库。
func (r *Registry) UpdateCredentials(accountID int, credentials map[string]string) {
	if len(credentials) == 0 {
		return
	}
	creds := cloneStringMap(credentials)
	r.mutate(accountID, func(a *Snapshot) {
		a.Credentials = creds
	})
	if r.credSink == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), r.persistTimeout)
		defer cancel()
		if err := r.credSink.PersistAccountCredentials(ctx, accountID, creds); err != nil {
			slog.Error("account_credentials_persist_failed",
				"account_id", accountID, "error", err)
		}
	}()
}

func (r *Registry) mutate(accountID int, apply func(*Snapshot)) *Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	old, ok := r.accounts[accountID]
	if !ok {
		return nil
	}
	next := *old
	// map 字段浅共享（只读）；apply 若替换 map 则写新指针。
	apply(&next)
	r.accounts[accountID] = &next
	return &next
}

func (r *Registry) mutateRuntime(accountID int, apply func(*Snapshot) bool) *Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	old, ok := r.accounts[accountID]
	if !ok {
		return nil
	}
	next := *old
	if !apply(&next) {
		return old
	}
	r.accounts[accountID] = &next
	return &next
}

func (r *Registry) mutateState(accountID int, apply func(*Snapshot) bool) (*Snapshot, uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	old, ok := r.accounts[accountID]
	if !ok {
		return nil, 0
	}
	next := *old
	if !apply(&next) {
		return old, 0
	}
	r.accounts[accountID] = &next
	// 路由目录只按 EffectivePriority 分桶；rate_limited/disabled 等状态由
	// RouteCandidate 每次选取时实时复核，无需作废目录。只有优先级档位变化
	// （degraded 压档/恢复）才需要重建，避免高频状态抖动引发全量目录失效风暴。
	now := time.Now()
	if old.EffectivePriority(now) != next.EffectivePriority(now) {
		r.routeVersion.Add(1)
	}
	return &next, r.stateSeq.Add(1)
}

func sameTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func (r *Registry) markActiveWhen(accountID int, allowed func(*Snapshot) bool) {
	if r == nil {
		return
	}
	snap, version := r.mutateState(accountID, func(a *Snapshot) bool {
		if !allowed(a) {
			return false
		}
		if a.State == StateActive && a.StateUntil == nil && a.ErrorMsg == "" && a.rateLimitProbeClean() {
			return false
		}
		a.State = StateActive
		a.StateUntil = nil
		a.ErrorMsg = ""
		resetRateLimitProbe(a)
		return true
	})
	if snap == nil || version == 0 {
		return
	}
	r.persistAsync(accountID, StateActive, nil, "", version)
}

func (s *Snapshot) rateLimitProbeBlocked(now time.Time) bool {
	return s != nil && (s.rateLimitProbeInFlight || s.rateLimitProbeBlockUntil.After(now))
}

func (s *Snapshot) rateLimitProbeClean() bool {
	return s != nil && !s.rateLimitProbeInFlight && s.rateLimitProbeLease == 0 && s.rateLimitProbeFailures == 0 && s.rateLimitProbeBlockUntil.IsZero()
}

func (s *Snapshot) hasRateLimitProbeLease(lease RateLimitProbeLease) bool {
	return s != nil && lease != 0 && s.State == StateRateLimited && s.rateLimitProbeInFlight && s.rateLimitProbeLease == lease
}

func resetRateLimitProbe(s *Snapshot) {
	if s == nil {
		return
	}
	s.rateLimitProbeInFlight = false
	s.rateLimitProbeLease = 0
	s.rateLimitProbeFailures = 0
	s.rateLimitProbeBlockUntil = time.Time{}
}

func copyRateLimitProbeRuntime(dst, src *Snapshot) {
	if dst == nil || src == nil {
		return
	}
	dst.rateLimitProbeInFlight = src.rateLimitProbeInFlight
	dst.rateLimitProbeLease = src.rateLimitProbeLease
	dst.rateLimitProbeFailures = src.rateLimitProbeFailures
	dst.rateLimitProbeBlockUntil = src.rateLimitProbeBlockUntil
}

func preserveConcurrentState(dst, src *Snapshot) {
	if dst == nil || src == nil {
		return
	}
	dst.State = src.State
	dst.StateUntil = cloneTime(src.StateUntil)
	dst.ErrorMsg = src.ErrorMsg
	copyRateLimitProbeRuntime(dst, src)
}

func sameAccountRuntimeState(left, right *Snapshot) bool {
	return left != nil && right != nil &&
		left.State == right.State && sameTime(left.StateUntil, right.StateUntil) && left.ErrorMsg == right.ErrorMsg
}

func sameRateLimitProbeRuntime(left, right *Snapshot) bool {
	return left != nil && right != nil &&
		left.rateLimitProbeInFlight == right.rateLimitProbeInFlight &&
		left.rateLimitProbeLease == right.rateLimitProbeLease &&
		left.rateLimitProbeFailures == right.rateLimitProbeFailures &&
		left.rateLimitProbeBlockUntil.Equal(right.rateLimitProbeBlockUntil)
}

func sameStringMap(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		other, ok := right[key]
		if !ok || other != value {
			return false
		}
	}
	return true
}

func mergeRateLimitProbeDeadline(s *Snapshot, loadedStateUntil *time.Time) {
	if s == nil || s.State != StateRateLimited || s.rateLimitProbeFailures == 0 {
		return
	}
	until := s.rateLimitProbeBlockUntil
	if s.StateUntil != nil {
		until = laterTime(until, *s.StateUntil)
	}
	if loadedStateUntil != nil {
		until = laterTime(until, *loadedStateUntil)
	}
	s.rateLimitProbeBlockUntil = until
	s.StateUntil = cloneTime(&until)
}

func rateLimitProbeBackoff(failures int) time.Duration {
	backoff := rateLimitProbeInitialBackoff
	for attempt := 1; attempt < failures && backoff < rateLimitProbeMaximumBackoff; attempt++ {
		backoff *= 2
		if backoff >= rateLimitProbeMaximumBackoff {
			return rateLimitProbeMaximumBackoff
		}
	}
	return backoff
}

func laterTime(left, right time.Time) time.Time {
	if right.After(left) {
		return right
	}
	return left
}

func (r *Registry) persistAsync(accountID int, state string, until *time.Time, errMsg string, version uint64) {
	if r.persister == nil {
		return
	}
	request := statePersistRequest{
		accountID: accountID,
		state:     state,
		until:     cloneTime(until),
		errMsg:    errMsg,
		version:   version,
	}
	r.persistMu.Lock()
	if version <= r.persistLatest[accountID] {
		r.persistMu.Unlock()
		return
	}
	r.persistLatest[accountID] = version
	r.persistPending[accountID] = request
	if r.persistRunning[accountID] {
		r.persistMu.Unlock()
		return
	}
	r.persistRunning[accountID] = true
	r.persistMu.Unlock()
	go r.runStatePersistWorker(accountID)
}

func (r *Registry) runStatePersistWorker(accountID int) {
	for {
		r.persistMu.Lock()
		request, ok := r.persistPending[accountID]
		if !ok {
			delete(r.persistRunning, accountID)
			r.persistMu.Unlock()
			return
		}
		delete(r.persistPending, accountID)
		r.persistMu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), r.persistTimeout)
		err := r.persister.PersistAccountState(ctx, request.accountID, request.state, request.until, request.errMsg)
		cancel()
		if err != nil {
			slog.Error("account_state_persist_failed",
				"account_id", request.accountID, "state", request.state, "error", err)
		}
	}
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneStructMap(in map[string]struct{}) map[string]struct{} {
	if in == nil {
		return nil
	}
	out := make(map[string]struct{}, len(in))
	for k := range in {
		out[k] = struct{}{}
	}
	return out
}

func cloneIntSet(in map[int]struct{}) map[int]struct{} {
	if in == nil {
		return nil
	}
	out := make(map[int]struct{}, len(in))
	for k := range in {
		out[k] = struct{}{}
	}
	return out
}
