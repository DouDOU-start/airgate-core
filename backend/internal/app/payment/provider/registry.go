package provider

import (
	"sync"
)

// Registry 持有所有注册的 Provider 实例，提供路由能力。
//
// 线程安全：所有方法都加读写锁，支持运行时通过 admin API reload Provider 列表。
//
// 路由策略（简化版）：
//   - Pick(method) 返回第一个 Enabled + 支持该 method 的 Provider
//   - 不做多商户轮询、不做最小金额选择（用户决定不需要）
//   - 后续要扩展也只需改这一处，不影响 service / Provider 实现
type Registry struct {
	mu        sync.RWMutex
	providers []Provider          // 按 ID 字典序稳定排序，让 Pick 行为确定
	byID      map[string]Provider // ID → Provider 快速查找（用于回调路由）
}

// NewRegistry 创建一个空 Registry
func NewRegistry() *Registry {
	return &Registry{
		byID: make(map[string]Provider),
	}
}

// Replace 用一组新的 Provider 完全替换旧的列表。
// 用于 admin 修改配置后重新加载所有 Provider 实例。
func (r *Registry) Replace(providers []Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.providers = providers
	r.byID = make(map[string]Provider, len(providers))
	for _, p := range providers {
		r.byID[p.ID()] = p
	}
}

// Pick 根据用户选的 method 选一个可用的 Provider。
//
// 失败原因可能是：
//   - 没有任何 Provider 启用 → 整个支付系统未配置
//   - 该 method 没有 Provider 支持 → 用户传了一个不存在的 method
//
// 这些情况都返回 ErrNoProviderAvailable，service 层包装成对用户友好的提示。
func (r *Registry) Pick(method string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, p := range r.providers {
		if !p.Enabled() {
			continue
		}
		if !containsString(p.SupportedMethods(), method) {
			continue
		}
		return p, nil
	}
	return nil, ErrNoProviderAvailable
}

// Find 按 ID 直接查找 Provider 实例，用于回调路由。
// 返回 nil 表示不存在或已被卸载。
func (r *Registry) Find(id string) Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byID[id]
}

// AvailableMethods 返回当前所有 Enabled Provider 加起来能服务的 method 集合（去重 + 按 allMethods 顺序）。
//
// 前端调用 GET /user/methods 拿这个列表渲染支付方式按钮。
// 用户看到的就是真正能用的，避免点了之后才发现"渠道未配置"。
func (r *Registry) AvailableMethods() []MethodInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	enabled := make(map[string]bool)
	for _, p := range r.providers {
		if !p.Enabled() {
			continue
		}
		for _, m := range p.SupportedMethods() {
			enabled[m] = true
		}
	}

	out := make([]MethodInfo, 0, len(enabled))
	for _, info := range allMethods {
		if enabled[info.Key] {
			out = append(out, info)
		}
	}
	return out
}

// All 返回所有已注册的 Provider 实例（含未启用的），用于 admin 配置页展示。
func (r *Registry) All() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Provider, len(r.providers))
	copy(out, r.providers)
	return out
}

func containsString(s []string, target string) bool {
	for _, v := range s {
		if v == target {
			return true
		}
	}
	return false
}
