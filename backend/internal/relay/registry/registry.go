// Package registry 提供渠道调度的内存注册表：
// 全量渠道快照（api_keys 已解密）常驻内存，
// 转发管线经 Pick 选渠道、NextKey 轮询密钥；状态变更（自动禁用/恢复）
// 内存即时生效并经 Persister 异步落库。
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

// 渠道状态常量，与 ent schema 的 status 枚举一致。
const (
	StatusEnabled        = "enabled"
	StatusDisabledManual = "disabled_manual"
	StatusDisabledAuto   = "disabled_auto"
)

// 入口协议常量：纯透传网关不做跨协议翻译，Pick 只在与入口协议同构的渠道类型集合内调度。
// 任务类协议（openai_video / suno）的常量值与渠道 Type、task.platform 同值——
// 任务子系统按平台分树路由，协议即平台。
const (
	ProtocolOpenAI      = "openai"
	ProtocolAnthropic   = "anthropic"
	ProtocolGemini      = "gemini"
	ProtocolOpenAIVideo = "openai_video"
	ProtocolSuno        = "suno"
)

// protocolChannelTypes 入口协议 → 可路由渠道 Type 集合。
// custom 语义为「OpenAI 兼容自定义渠道」，归 openai 协议组。
var protocolChannelTypes = map[string]map[string]struct{}{
	ProtocolOpenAI:      {"openai_compatible": {}, "custom": {}},
	ProtocolAnthropic:   {"anthropic": {}},
	ProtocolGemini:      {"gemini": {}},
	ProtocolOpenAIVideo: {"openai_video": {}},
	ProtocolSuno:        {"suno": {}},
}

// channelTypesForProtocol 返回协议可路由的渠道类型集合；
// 未知协议（含空串）返回 nil，Pick 侧表现为无可用渠道。
func channelTypesForProtocol(protocol string) map[string]struct{} {
	return protocolChannelTypes[protocol]
}

// protocolForChannelType 渠道 Type → 入口协议（protocolChannelTypes 的反向映射）；
// 未知类型返回空串（不进模型目录）。
func protocolForChannelType(chType string) string {
	for proto, types := range protocolChannelTypes {
		if _, ok := types[chType]; ok {
			return proto
		}
	}
	return ""
}

// ErrNoAvailableChannel 表示当前分组/模型下无可调度渠道。
var ErrNoAvailableChannel = errors.New("无可用渠道")

// ChannelSnapshot 渠道运行时快照（解密后）。
//
// Pick 返回的快照为只读视图：注册表内部以 copy-on-write 方式更新，
// 调用方持有的指针不会被并发修改，但也禁止就地改写。
type ChannelSnapshot struct {
	ID      int
	Name    string
	Type    string
	BaseURL string
	// APIKeys 已解密的明文密钥列表。
	APIKeys []string
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
	Status         string
	// GroupIDs 绑定分组集合；空集合表示公共渠道，对所有分组可用。
	GroupIDs  map[int]struct{}
	TestModel string
}

// Loader 全量加载渠道快照（由 channel service 实现：解密 api_keys）。
type Loader interface {
	LoadAllForRegistry(ctx context.Context) ([]ChannelSnapshot, error)
}

// Persister 渠道状态异步落库（由 channel service 实现）。
type Persister interface {
	PersistState(ctx context.Context, id int, status string, errMsg string) error
}

// 惰性兜底加载参数：注册表从未成功加载过（如启动时 DB 瞬断）时，
// Pick 触发一次带锁重载自愈，避免空注册表持续 503 直到管理员写操作。
const (
	lazyReloadTimeout  = 5 * time.Second
	lazyReloadInterval = 3 * time.Second
)

// Registry 渠道注册表：RWMutex 保护的全量快照 + 渠道内 key 轮询计数器。
type Registry struct {
	loader    Loader
	persister Persister

	mu       sync.RWMutex
	channels map[int]*ChannelSnapshot
	counters map[int]*atomic.Uint64

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

// New 创建渠道注册表。loader 必填；persister 可为 nil（不落库，仅内存生效）。
func New(loader Loader, persister Persister) *Registry {
	return &Registry{
		loader:         loader,
		persister:      persister,
		channels:       map[int]*ChannelSnapshot{},
		counters:       map[int]*atomic.Uint64{},
		randFn:         rand.IntN,
		persistTimeout: 5 * time.Second,
	}
}

// Reload 从 Loader 拉取全量快照并整体替换；保留仍存在渠道的 key 轮询计数器。
func (r *Registry) Reload(ctx context.Context) error {
	snaps, err := r.loader.LoadAllForRegistry(ctx)
	if err != nil {
		return err
	}

	next := make(map[int]*ChannelSnapshot, len(snaps))
	for i := range snaps {
		snap := snaps[i]
		next[snap.ID] = &snap
	}

	r.mu.Lock()
	r.channels = next
	for id := range next {
		if _, ok := r.counters[id]; !ok {
			r.counters[id] = new(atomic.Uint64)
		}
	}
	for id := range r.counters {
		if _, ok := next[id]; !ok {
			delete(r.counters, id)
		}
	}
	r.mu.Unlock()
	r.loadedOnce.Store(true)
	return nil
}

// ensureLoaded 注册表从未成功加载过时惰性触发一次重载（Pick 前调用）。
// TryLock 单飞：另一请求正在重载时直接返回用当前（空）快照；
// 节流间隔内不重复打 DB。加载成功后此路径永不再触发。
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

// Pick 为指定分组、模型与入口协议选择一个渠道：
//
//	候选 = status==enabled 且模型命中
//	       且渠道 Type 属于入口协议的同构类型集合（纯透传：不做跨协议翻译，
//	       同一模型可同时存在于多协议渠道，Pick 只在协议匹配的集合内调度）
//	       且分组命中（渠道 GroupIDs 为空 = 公共渠道）且不在 exclude 中
//	→ 取最高 priority 档 → 档内按 weight+10 加权随机。
//
// 无候选返回 ErrNoAvailableChannel。
func (r *Registry) Pick(groupID int, model, protocol string, exclude []int) (*ChannelSnapshot, error) {
	r.ensureLoaded()

	excluded := make(map[int]struct{}, len(exclude))
	for _, id := range exclude {
		excluded[id] = struct{}{}
	}
	allowedTypes := channelTypesForProtocol(protocol)

	r.mu.RLock()
	defer r.mu.RUnlock()

	// 单趟扫描：维护当前最高 priority 档。
	var tier []*ChannelSnapshot
	best := -1
	for _, ch := range r.channels {
		if _, skip := excluded[ch.ID]; skip {
			continue
		}
		if _, ok := allowedTypes[ch.Type]; !ok {
			continue
		}
		if ch.Status != StatusEnabled {
			continue
		}
		if _, ok := ch.Models[model]; !ok {
			continue
		}
		if len(ch.GroupIDs) > 0 {
			if _, ok := ch.GroupIDs[groupID]; !ok {
				continue
			}
		}
		if ch.Priority > best {
			best = ch.Priority
			tier = tier[:0]
		}
		if ch.Priority == best {
			tier = append(tier, ch)
		}
	}
	if len(tier) == 0 {
		return nil, ErrNoAvailableChannel
	}

	// 档内按 ID 排序：map 遍历无序，排序保证同一随机值的选择结果可复现（也便于测试）。
	sort.Slice(tier, func(i, j int) bool { return tier[i].ID < tier[j].ID })

	total := 0
	for _, ch := range tier {
		total += ch.Weight + 10
	}
	n := r.randFn(total)
	for _, ch := range tier {
		n -= ch.Weight + 10
		if n < 0 {
			return ch, nil
		}
	}
	return tier[len(tier)-1], nil
}

// ModelEntry 模型目录条目：对外模型名 + 可经哪些入口协议调用（升序）。
// 同一模型可能同时由多协议渠道供给（如 claude 系模型既有 anthropic 原生渠道
// 又有 openai 兼容聚合渠道），第一方应用据 Protocols 选端点。
type ModelEntry struct {
	Name      string
	Protocols []string
}

// ModelEntriesForGroup 返回指定分组可用渠道（status==enabled）的
// 模型目录（含协议集合），按模型名字典序。
func (r *Registry) ModelEntriesForGroup(groupID int) []ModelEntry {
	r.mu.RLock()
	set := map[string]map[string]struct{}{}
	for _, ch := range r.channels {
		if ch.Status != StatusEnabled {
			continue
		}
		if len(ch.GroupIDs) > 0 {
			if _, ok := ch.GroupIDs[groupID]; !ok {
				continue
			}
		}
		proto := protocolForChannelType(ch.Type)
		if proto == "" {
			continue
		}
		for m := range ch.Models {
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

// Snapshot 按 ID 返回渠道只读快照；不存在返回 (nil, false)。
// 任务子系统用：轮询已落库任务、代理成片内容时须回到提交时的原渠道。
func (r *Registry) Snapshot(id int) (*ChannelSnapshot, bool) {
	r.ensureLoaded()
	r.mu.RLock()
	defer r.mu.RUnlock()
	ch, ok := r.channels[id]
	return ch, ok
}

// NextKey 渠道内 API Key 原子轮询；渠道不存在或无密钥返回空串。
func (r *Registry) NextKey(channelID int) string {
	r.mu.RLock()
	ch, ok := r.channels[channelID]
	counter := r.counters[channelID]
	r.mu.RUnlock()

	if !ok || len(ch.APIKeys) == 0 || counter == nil {
		return ""
	}
	n := counter.Add(1) - 1
	return ch.APIKeys[int(n%uint64(len(ch.APIKeys)))]
}

// MarkAutoDisabled 自动禁用渠道（上游 401/403）：内存即时生效 + 异步落库。
func (r *Registry) MarkAutoDisabled(id int, reason string) {
	snap := r.mutate(id, func(c *ChannelSnapshot) {
		c.Status = StatusDisabledAuto
	})
	if snap == nil {
		return
	}
	r.persistAsync(id, StatusDisabledAuto, reason)
}

// MarkRecovered 将 disabled_auto 渠道恢复为 enabled；其余状态不动（手动禁用不自动恢复）。
func (r *Registry) MarkRecovered(id int) {
	recovered := false
	snap := r.mutate(id, func(c *ChannelSnapshot) {
		if c.Status != StatusDisabledAuto {
			return
		}
		c.Status = StatusEnabled
		recovered = true
	})
	if snap == nil || !recovered {
		return
	}
	r.persistAsync(id, StatusEnabled, "")
}

// mutate 以 copy-on-write 方式更新指定渠道快照，返回更新后的快照；渠道不存在返回 nil。
func (r *Registry) mutate(id int, apply func(*ChannelSnapshot)) *ChannelSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()

	old, ok := r.channels[id]
	if !ok {
		return nil
	}
	// 浅拷贝：map/slice 字段只读共享，仅标量状态字段被改写。
	next := *old
	apply(&next)
	r.channels[id] = &next
	return &next
}

// persistAsync 异步落库渠道状态；失败仅记日志，不影响内存状态。
func (r *Registry) persistAsync(id int, status string, errMsg string) {
	if r.persister == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), r.persistTimeout)
		defer cancel()
		if err := r.persister.PersistState(ctx, id, status, errMsg); err != nil {
			slog.Error("channel_state_persist_failed",
				"channel_id", id,
				"status", status,
				"error", err)
		}
	}()
}
