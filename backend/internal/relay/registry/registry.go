// Package registry 提供密钥端点调度的内存注册表：
// 全量渠道密钥端点快照（api_key 已解密）常驻内存，
// 转发管线经 Pick 直接选中一把 key；状态变更（自动禁用/恢复）
// 内存即时生效并经 Persister 异步落库。
//
// 路由单元为「渠道下的一把 key（ChannelKey）」：协议类型、模型、分组、
// 优先级/权重、限流上限、成本倍率、启停状态均在 key 级；base_url 由所属渠道共享。
//
// 依赖约束：本包禁止 import ent 或 internal/app/channel（防环）；
// 数据加载与落库均经 Loader / Persister 窄接口由外部注入。
package registry

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

// key 端点状态常量，与 ent schema 的 channel_key.status 枚举一致。
const (
	StatusEnabled        = "enabled"
	StatusDisabledManual = "disabled_manual"
	StatusDisabledAuto   = "disabled_auto"
)

// 入口协议常量：纯透传网关不做跨协议翻译，Pick 只在与入口协议同构的 key 类型集合内调度。
// 任务类协议（openai_video / suno）的常量值与 key.Type、task.platform 同值——
// 任务子系统按平台分树路由，协议即平台。
const (
	ProtocolOpenAI      = "openai"
	ProtocolAnthropic   = "anthropic"
	ProtocolGemini      = "gemini"
	ProtocolOpenAIVideo = "openai_video"
	ProtocolSuno        = "suno"
)

// protocolKeyTypes 入口协议 → 可路由 key Type 集合。
// custom 语义为「OpenAI 兼容自定义端点」，归 openai 协议组。
var protocolKeyTypes = map[string]map[string]struct{}{
	ProtocolOpenAI:      {"openai_compatible": {}, "custom": {}},
	ProtocolAnthropic:   {"anthropic": {}},
	ProtocolGemini:      {"gemini": {}},
	ProtocolOpenAIVideo: {"openai_video": {}},
	ProtocolSuno:        {"suno": {}},
}

// keyTypesForProtocol 返回协议可路由的 key 类型集合；
// 未知协议（含空串）返回 nil，Pick 侧表现为无可用 key。
func keyTypesForProtocol(protocol string) map[string]struct{} {
	return protocolKeyTypes[protocol]
}

// protocolForKeyType key Type → 入口协议（protocolKeyTypes 的反向映射）；
// 未知类型返回空串（不进模型目录）。
func protocolForKeyType(keyType string) string {
	for proto, types := range protocolKeyTypes {
		if _, ok := types[keyType]; ok {
			return proto
		}
	}
	return ""
}

// ErrNoAvailableChannel 表示当前分组/模型下无可调度的 key 端点。
var ErrNoAvailableChannel = errors.New("无可用渠道")

// ChannelKeySnapshot 渠道密钥端点运行时快照（解密后）。
//
// Pick 返回的快照为只读视图：注册表内部以 copy-on-write 方式更新，
// 调用方持有的指针不会被并发修改，但也禁止就地改写。
type ChannelKeySnapshot struct {
	// KeyID / KeyName 密钥端点标识：调度、故障隔离与管理端留痕展示使用。
	KeyID   int
	KeyName string
	// ChannelID / ChannelName 所属渠道（供应商）标识，供计费聚合与留痕。
	ChannelID   int
	ChannelName string
	// BaseURL 上游地址，来自所属渠道（供应商共享）。
	BaseURL string
	Type    string
	// APIKey 已解密的明文密钥。
	APIKey string
	// Models 可服务的对外模型名集合。
	Models map[string]struct{}
	// ModelMapping 对外模型名 → 上游模型名。
	ModelMapping   map[string]string
	ParamOverride  map[string]any
	HeaderOverride map[string]string
	Priority       int
	Weight         int
	MaxConcurrency int
	MaxRPM         int
	CostRatio      float64
	// UpstreamRate 是最近一次成功探测到的上游真实倍率；只有
	// UseUpstreamRateForCost 开启时才参与成本计算。
	UpstreamRate           float64
	UseUpstreamRateForCost bool
	Status                 string
	// GroupIDs 绑定分组集合；空集合表示未绑定任何分组，不会被任何分组调度到
	// （已不支持"空集合=公共 key，对所有分组可用"的旧语义）。
	GroupIDs  map[int]struct{}
	TestModel string
	// HealthStatus 健康状态（healthy/degraded/suspended/recovering）。
	// degraded 时 Pick 降权（有效权重减半）；suspended/recovering 由探针驱动恢复。
	HealthStatus string
}

// EffectiveCostRatio 返回请求落账使用的渠道成本倍率。
// 开启探测倍率计入成本且结果有效时使用探测值，否则使用手动配置；均无效时回退 1。
func (k *ChannelKeySnapshot) EffectiveCostRatio() float64 {
	if k != nil && k.UseUpstreamRateForCost && k.UpstreamRate > 0 {
		return k.UpstreamRate
	}
	if k != nil && k.CostRatio > 0 {
		return k.CostRatio
	}
	return 1
}

// Loader 全量加载密钥端点快照（由 channel service 实现：解密 api_key）。
type Loader interface {
	LoadAllForRegistry(ctx context.Context) ([]ChannelKeySnapshot, error)
}

// Persister 密钥端点状态异步落库（由 channel service 实现）。
type Persister interface {
	PersistState(ctx context.Context, keyID int, status string, errMsg string) error
}

// 惰性兜底加载参数：注册表从未成功加载过（如启动时 DB 瞬断）时，
// Pick 触发一次带锁重载自愈，避免空注册表持续 503 直到管理员写操作。
const (
	lazyReloadTimeout  = 5 * time.Second
	lazyReloadInterval = 3 * time.Second
)

// Registry 密钥端点注册表：RWMutex 保护的全量快照（按 keyID 组织）。
type Registry struct {
	loader    Loader
	persister Persister

	mu   sync.RWMutex
	keys map[int]*ChannelKeySnapshot

	// loadedOnce 是否成功加载过：false 时 Pick 触发惰性兜底重载。
	loadedOnce atomic.Bool
	// lazyMu 惰性重载互斥：TryLock 单飞，其余请求直接用当前快照。
	lazyMu sync.Mutex
	// lastLazyReload 上次惰性重载时间（lazyMu 保护）：节流避免每请求打 DB。
	lastLazyReload time.Time

	// randFn 加权随机源，可注入以便测试；入参 n>0，返回 [0,n)。
	randFn func(n int) int

	// persistTimeout 异步落库超时。
	persistTimeout time.Duration
}

// New 创建密钥端点注册表。loader 必填；persister 可为 nil（不落库，仅内存生效）。
func New(loader Loader, persister Persister) *Registry {
	return &Registry{
		loader:         loader,
		persister:      persister,
		keys:           map[int]*ChannelKeySnapshot{},
		randFn:         rand.IntN,
		persistTimeout: 5 * time.Second,
	}
}

// Reload 从 Loader 拉取全量快照并整体替换。
func (r *Registry) Reload(ctx context.Context) error {
	snaps, err := r.loader.LoadAllForRegistry(ctx)
	if err != nil {
		return err
	}

	next := make(map[int]*ChannelKeySnapshot, len(snaps))
	for i := range snaps {
		snap := snaps[i]
		next[snap.KeyID] = &snap
	}

	r.mu.Lock()
	r.keys = next
	r.mu.Unlock()
	r.loadedOnce.Store(true)
	return nil
}

// ensureLoaded 注册表从未成功加载过时惰性触发一次重载（Pick 前调用）。
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
		slog.Warn("channel_registry_lazy_reload_failed", "error", err)
	}
}

// ListCandidates 返回可调度 key 候选（已过滤 status/type/group/model/exclude）。
// 供与账号路径统一混合选路；返回切片内指针只读。
func (r *Registry) ListCandidates(groupID int, model, protocol string, exclude []int) []*ChannelKeySnapshot {
	r.ensureLoaded()
	excluded := make(map[int]struct{}, len(exclude))
	for _, id := range exclude {
		excluded[id] = struct{}{}
	}
	allowedTypes := keyTypesForProtocol(protocol)

	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*ChannelKeySnapshot, 0, 8)
	for _, k := range r.keys {
		if _, skip := excluded[k.KeyID]; skip {
			continue
		}
		if _, ok := allowedTypes[k.Type]; !ok {
			continue
		}
		if k.Status != StatusEnabled {
			continue
		}
		if _, ok := k.Models[model]; !ok {
			continue
		}
		if _, ok := k.GroupIDs[groupID]; !ok {
			continue
		}
		out = append(out, k)
	}
	return out
}

// Pick 为指定分组、模型与入口协议选择一把 key 端点：
//
//	候选 = status==enabled 且模型命中
//	       且 key.Type 属于入口协议的同构类型集合（纯透传：不做跨协议翻译）
//	       且分组命中（key.GroupIDs 为空则不命中任何分组）且不在 exclude（按 keyID）中
//	→ 取最高 priority 档 → 档内按 weight+10 加权随机。
//
// 无候选返回 ErrNoAvailableChannel。
func (r *Registry) Pick(groupID int, model, protocol string, exclude []int) (*ChannelKeySnapshot, error) {
	r.ensureLoaded()

	excluded := make(map[int]struct{}, len(exclude))
	for _, id := range exclude {
		excluded[id] = struct{}{}
	}
	allowedTypes := keyTypesForProtocol(protocol)

	r.mu.RLock()
	defer r.mu.RUnlock()

	// 单趟扫描：维护当前最高 priority 档。
	var tier []*ChannelKeySnapshot
	best := -1
	for _, k := range r.keys {
		if _, skip := excluded[k.KeyID]; skip {
			continue
		}
		if _, ok := allowedTypes[k.Type]; !ok {
			continue
		}
		if k.Status != StatusEnabled {
			continue
		}
		if _, ok := k.Models[model]; !ok {
			continue
		}
		if _, ok := k.GroupIDs[groupID]; !ok {
			continue
		}
		if k.Priority > best {
			best = k.Priority
			tier = tier[:0]
		}
		if k.Priority == best {
			tier = append(tier, k)
		}
	}
	if len(tier) == 0 {
		return nil, ErrNoAvailableChannel
	}

	// 档内按 KeyID 排序：map 遍历无序，排序保证同一随机值的选择结果可复现（也便于测试）。
	sort.Slice(tier, func(i, j int) bool { return tier[i].KeyID < tier[j].KeyID })

	total := 0
	for _, k := range tier {
		w := k.Weight + 10
		if k.HealthStatus == "degraded" {
			w /= 2
		}
		total += w
	}
	n := r.randFn(total)
	for _, k := range tier {
		w := k.Weight + 10
		if k.HealthStatus == "degraded" {
			w /= 2
		}
		n -= w
		if n < 0 {
			return k, nil
		}
	}
	return tier[len(tier)-1], nil
}

// ModelEntry 模型目录条目：对外模型名 + 可经哪些入口协议调用（升序）。
type ModelEntry struct {
	Name      string
	Protocols []string
}

// ModelEntriesForGroup 返回指定分组可用 key 端点（status==enabled）的
// 模型目录（含协议集合），按模型名字典序。
func (r *Registry) ModelEntriesForGroup(groupID int) []ModelEntry {
	r.mu.RLock()
	set := map[string]map[string]struct{}{}
	for _, k := range r.keys {
		if k.Status != StatusEnabled {
			continue
		}
		if _, ok := k.GroupIDs[groupID]; !ok {
			continue
		}
		proto := protocolForKeyType(k.Type)
		if proto == "" {
			continue
		}
		for m := range k.Models {
			if set[m] == nil {
				set[m] = map[string]struct{}{}
			}
			set[m][proto] = struct{}{}
		}
	}
	r.mu.RUnlock()

	entries := make([]ModelEntry, 0, len(set))
	for m, protos := range set {
		ps := make([]string, 0, len(protos))
		for p := range protos {
			ps = append(ps, p)
		}
		sort.Strings(ps)
		entries = append(entries, ModelEntry{Name: m, Protocols: ps})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries
}

// Snapshot 按 keyID 返回 key 端点只读快照；不存在返回 (nil, false)。
// 任务子系统用：轮询已落库任务、代理成片内容时须回到提交时的原 key。
func (r *Registry) Snapshot(keyID int) (*ChannelKeySnapshot, bool) {
	r.ensureLoaded()
	r.mu.RLock()
	defer r.mu.RUnlock()
	k, ok := r.keys[keyID]
	return k, ok
}

// AnyKeyForChannel 返回指定渠道下任一 enabled key 端点快照（存量任务无 key_id 时回退用）。
// 无可用 key 返回 (nil, false)。
func (r *Registry) AnyKeyForChannel(channelID int) (*ChannelKeySnapshot, bool) {
	r.ensureLoaded()
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, k := range r.keys {
		if k.ChannelID == channelID && k.Status == StatusEnabled {
			return k, true
		}
	}
	return nil, false
}

// MarkAutoDisabled 自动禁用 key 端点（上游 401/403）：内存即时生效 + 异步落库。
func (r *Registry) MarkAutoDisabled(keyID int, reason string) {
	snap := r.mutate(keyID, func(k *ChannelKeySnapshot) {
		k.Status = StatusDisabledAuto
	})
	if snap == nil {
		return
	}
	r.persistAsync(keyID, StatusDisabledAuto, reason)
}

// MarkRecovered 将 disabled_auto 的 key 恢复为 enabled；其余状态不动（手动禁用不自动恢复）。
func (r *Registry) MarkRecovered(keyID int) {
	recovered := false
	snap := r.mutate(keyID, func(k *ChannelKeySnapshot) {
		if k.Status != StatusDisabledAuto {
			return
		}
		k.Status = StatusEnabled
		recovered = true
	})
	if snap == nil || !recovered {
		return
	}
	r.persistAsync(keyID, StatusEnabled, "")
}

// UpdateHealth 更新 key 的健康状态（内存即时生效，不落库——由探针引擎负责落库）。
func (r *Registry) UpdateHealth(keyID int, health string) {
	r.mutate(keyID, func(k *ChannelKeySnapshot) {
		k.HealthStatus = health
	})
}

// UpdateUpstreamRate 在探测结果落库后同步更新运行时快照；是否计入成本由密钥开关决定。
func (r *Registry) UpdateUpstreamRate(keyID int, rate float64) {
	if rate <= 0 {
		return
	}
	r.mutate(keyID, func(k *ChannelKeySnapshot) {
		k.UpstreamRate = rate
	})
}

// mutate 以 copy-on-write 方式更新指定 key 快照，返回更新后的快照；key 不存在返回 nil。
func (r *Registry) mutate(keyID int, apply func(*ChannelKeySnapshot)) *ChannelKeySnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()

	old, ok := r.keys[keyID]
	if !ok {
		return nil
	}
	// 浅拷贝：map/slice 字段只读共享，仅标量状态字段被改写。
	next := *old
	apply(&next)
	r.keys[keyID] = &next
	return &next
}

// persistAsync 异步落库 key 状态；失败仅记日志，不影响内存状态。
func (r *Registry) persistAsync(keyID int, status string, errMsg string) {
	if r.persister == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), r.persistTimeout)
		defer cancel()
		if err := r.persister.PersistState(ctx, keyID, status, errMsg); err != nil {
			slog.Error("channel_key_state_persist_failed",
				"channel_key_id", keyID,
				"status", status,
				"error", err)
		}
	}()
}
