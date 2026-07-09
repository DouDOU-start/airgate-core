package provider

import (
	"fmt"
	"sort"
	"sync"
)

// Builder 一个 Provider 实现的"构造函数"。
//
// 接收 Provider 实例 ID（用户在 admin 配置页起的名字，例如 "xunhu_main"）
// 与 enabled 标志、字段 map（admin 页表单填的值），返回完整初始化的 Provider 实例。
type Builder func(id string, enabled bool, config map[string]string) (Provider, error)

// builderRegistry 全局 Builder 注册表。
//
// 每个 Provider 实现文件在 init() 里调用 Register(kind, builder) 注册自己。
// plugin Init 时根据 store.List() 拿到的 ConfigRecord.Kind 选对应 Builder。
var (
	builderMu  sync.RWMutex
	builderMap = make(map[string]Builder)
)

// Register 注册一个 Provider Builder。重复注册会 panic（编程错误）。
func Register(kind string, b Builder) {
	builderMu.Lock()
	defer builderMu.Unlock()
	if _, exists := builderMap[kind]; exists {
		panic(fmt.Sprintf("provider kind %s already registered", kind))
	}
	builderMap[kind] = b
}

// Build 按 kind 调用对应 Builder 构造 Provider 实例。
// 找不到对应 kind 时返回错误。
func Build(kind, id string, enabled bool, config map[string]string) (Provider, error) {
	builderMu.RLock()
	b, ok := builderMap[kind]
	builderMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown provider kind: %s", kind)
	}
	return b(id, enabled, config)
}

// RegisteredKinds 返回所有已注册的 kind 列表（按字母序），用于 admin UI 展示"可添加的 Provider 类型"。
func RegisteredKinds() []string {
	builderMu.RLock()
	defer builderMu.RUnlock()
	kinds := make([]string, 0, len(builderMap))
	for k := range builderMap {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	return kinds
}

// KindMeta 描述一个 Provider 类型，用于 admin UI 渲染添加 Provider 的表单。
//
// FieldDescriptors 列出该 kind 需要哪些配置字段（key + label + type），
// admin 前端据此动态生成表单。
type KindMeta struct {
	Kind             string            `json:"kind"`
	Name             string            `json:"name"`
	Description      string            `json:"description"`
	SupportedMethods []string          `json:"supported_methods"`
	FieldDescriptors []FieldDescriptor `json:"field_descriptors"`
}

// FieldDescriptor admin 配置表单的字段定义。
//
// Type 取值：
//
//	text / password / textarea / number / bool — 普通输入控件
//	method-multi — 多选支付方式（前端渲染成 checkbox 组），可选项是该 Kind 的 SupportedMethods，
//	               存进 ConfigRecord.Config 时是逗号分隔的 method key 字符串（"alipay,wxpay"）
type FieldDescriptor struct {
	Key         string `json:"key"`         // 字段在 ConfigRecord.Config 里的 key
	Label       string `json:"label"`       // 显示名
	Type        string `json:"type"`        // text / password / textarea / number / bool / method-multi
	Required    bool   `json:"required"`    //
	Placeholder string `json:"placeholder"` // 占位提示
	Description string `json:"description"` // 帮助文本
}

// kindMetaRegistry 每个 kind 的元信息，由 Provider 文件 init() 注册
var (
	kindMetaMu  sync.RWMutex
	kindMetaMap = make(map[string]KindMeta)
)

// RegisterKindMeta 注册 kind 元信息
func RegisterKindMeta(meta KindMeta) {
	kindMetaMu.Lock()
	defer kindMetaMu.Unlock()
	kindMetaMap[meta.Kind] = meta
}

// AllKindMetas 返回所有 kind 元信息（按 kind 字母序）
func AllKindMetas() []KindMeta {
	kindMetaMu.RLock()
	defer kindMetaMu.RUnlock()
	out := make([]KindMeta, 0, len(kindMetaMap))
	for _, m := range kindMetaMap {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}

// GetKindMeta 按 kind 查找元信息，找不到返回零值
func GetKindMeta(kind string) (KindMeta, bool) {
	kindMetaMu.RLock()
	defer kindMetaMu.RUnlock()
	m, ok := kindMetaMap[kind]
	return m, ok
}
