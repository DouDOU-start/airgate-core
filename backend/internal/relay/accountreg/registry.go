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
	"sort"
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

// ErrNoAvailableAccount 当前分组/模型下无可调度账号。
var ErrNoAvailableAccount = errors.New("无可用账号")

// Snapshot 账号运行时快照（解密后）。
//
// Pick / ListCandidates 返回的快照为只读视图：copy-on-write 更新，
// 调用方持有的指针不会被并发修改，禁止就地改写。
type Snapshot struct {
	ID             int
	Name           string
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
	// GroupIDs 绑定分组；空集合不参与任何分组调度。
	GroupIDs map[int]struct{}
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
	lazyReloadTimeout  = 5 * time.Second
	lazyReloadInterval = 3 * time.Second
)

// Registry 账号注册表。
type Registry struct {
	loader    Loader
	persister Persister
	credSink  CredentialPersister

	mu       sync.RWMutex
	accounts map[int]*Snapshot

	loadedOnce     atomic.Bool
	lazyMu         sync.Mutex
	lastLazyReload time.Time

	randFn         func(n int) int
	persistTimeout time.Duration
	stateSeq       atomic.Uint64

	persistMu      sync.Mutex
	persistPending map[int]statePersistRequest
	persistRunning map[int]bool
	persistLatest  map[int]uint64
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

// Reload 全量替换快照。
func (r *Registry) Reload(ctx context.Context) error {
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
		next[snap.ID] = &snap
	}
	r.mu.Lock()
	r.accounts = next
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
	if r == nil {
		return nil
	}
	r.ensureLoaded()
	excluded := make(map[int]struct{}, len(exclude))
	for _, id := range exclude {
		excluded[id] = struct{}{}
	}
	now := time.Now()

	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*Snapshot, 0, 8)
	for _, a := range r.accounts {
		if _, skip := excluded[a.ID]; skip {
			continue
		}
		if !a.IsSchedulable(now) {
			continue
		}
		if a.State == StateDisabled {
			continue
		}
		if _, ok := a.GroupIDs[groupID]; !ok {
			continue
		}
		if len(a.Models) > 0 {
			if _, ok := a.Models[model]; !ok {
				continue
			}
		}
		// Models 为空：视为不限制（由 Loader 应用平台默认；若仍空则跳过防误调度）。
		if len(a.Models) == 0 {
			continue
		}
		out = append(out, a)
	}
	return out
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

// MarkRateLimited 标记限流（内存 + 异步落库）。
func (r *Registry) MarkRateLimited(accountID int, until time.Time, reason string) {
	untilCopy := until
	snap, version := r.mutateState(accountID, func(a *Snapshot) bool {
		if a.State == StateRateLimited && sameTime(a.StateUntil, &until) && a.ErrorMsg == reason {
			return false
		}
		a.State = StateRateLimited
		a.StateUntil = &untilCopy
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
		if a.State == StateDisabled && a.StateUntil == nil && a.ErrorMsg == reason {
			return false
		}
		a.State = StateDisabled
		a.StateUntil = nil
		a.ErrorMsg = reason
		return true
	})
	if snap == nil || version == 0 {
		return
	}
	r.persistAsync(accountID, StateDisabled, nil, reason, version)
}

// MarkActive 恢复 active。
func (r *Registry) MarkActive(accountID int) {
	snap, version := r.mutateState(accountID, func(a *Snapshot) bool {
		if a.State == StateActive && a.StateUntil == nil && a.ErrorMsg == "" {
			return false
		}
		a.State = StateActive
		a.StateUntil = nil
		a.ErrorMsg = ""
		return true
	})
	if snap == nil || version == 0 {
		return
	}
	r.persistAsync(accountID, StateActive, nil, "", version)
}

// MarkDegraded 软降级（upstream_is_pool 抖动）。
func (r *Registry) MarkDegraded(accountID int, until time.Time, reason string) {
	untilCopy := until
	snap, version := r.mutateState(accountID, func(a *Snapshot) bool {
		if a.State == StateDegraded && sameTime(a.StateUntil, &until) && a.ErrorMsg == reason {
			return false
		}
		a.State = StateDegraded
		a.StateUntil = &untilCopy
		a.ErrorMsg = reason
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
	return &next, r.stateSeq.Add(1)
}

func sameTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
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
