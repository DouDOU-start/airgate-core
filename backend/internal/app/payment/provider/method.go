package provider

import "strings"

// PayMethod 是用户面向的支付方式标识符。它与 Provider 解耦：
// "用户选支付宝" 与 "走哪家服务商" 是两个独立决定。
//
// 添加新方法时只需在这里加常量 + 在 allMethods 注册元信息，
// 然后在某个 Provider 的 SupportedMethods() 里声明它能承载这个 method。
const (
	MethodAlipay = "alipay" // 支付宝
	MethodWxpay  = "wxpay"  // 微信支付
)

// MethodInfo 支付方式的展示元信息。
// 前端拿到 /user/methods 后用这些字段渲染按钮。
type MethodInfo struct {
	Key         string `json:"key"`         // 唯一标识符
	Label       string `json:"label"`       // 用户可见的中文名
	Icon        string `json:"icon"`        // 前端图标 key
	Description string `json:"description"` // 简介（可选）
}

// allMethods 所有已知支付方式的元信息表。
// Router.AvailableMethods() 会过滤这个列表，只返回当前真正可用的。
var allMethods = []MethodInfo{
	{Key: MethodAlipay, Label: "支付宝", Icon: "alipay", Description: "扫码 / 网页支付"},
	{Key: MethodWxpay, Label: "微信支付", Icon: "wechat", Description: "扫码 / Native 支付"},
}

// MethodInfoFor 查找单个 method 的展示元信息。未知 method 返回零值。
func MethodInfoFor(key string) MethodInfo {
	for _, m := range allMethods {
		if m.Key == key {
			return m
		}
	}
	return MethodInfo{Key: key, Label: key}
}

// AllMethodInfos 返回所有已知 method 的元信息（顺序稳定，前端用作展示顺序基础）。
func AllMethodInfos() []MethodInfo {
	out := make([]MethodInfo, len(allMethods))
	copy(out, allMethods)
	return out
}

// parseEnabledMethods 解析 admin 在配置里勾选的"启用的支付方式"字段。
//
// 存储格式：逗号分隔的 method key 字符串（例如 "alipay,wxpay"）。
// 解析时：
//  1. trim 空白 + 跳过空 token
//  2. 用 supported 集合过滤无效值（防止 admin 手改 DB 写入未支持的 method）
//  3. 保持顺序，去重
//
// 当输入为空或解析后为空时，返回 nil — 调用方根据"nil 表示未配置"决定行为
// （xunhu/caihong 在 nil 时退化为协议全集，避免 Provider 完全不可用）。
func parseEnabledMethods(raw string, supported []string) []string {
	if raw == "" {
		return nil
	}
	supportedSet := make(map[string]bool, len(supported))
	for _, s := range supported {
		supportedSet[s] = true
	}
	seen := make(map[string]bool)
	var out []string
	for _, token := range strings.Split(raw, ",") {
		token = strings.TrimSpace(token)
		if token == "" || !supportedSet[token] || seen[token] {
			continue
		}
		seen[token] = true
		out = append(out, token)
	}
	return out
}
