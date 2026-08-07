// Package notify 定义管理员外推通知的窄抽象，便于渠道健康告警等场景接入
// 多种推送通道（当前为 Bark，后续可扩展 Telegram/Webhook 等）。
//
// 本包只描述消息与通道接口，不依赖 ent / gin / 具体 HTTP 客户端。
package notify

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Message 一条面向管理员的外推通知。
type Message struct {
	Title string
	Body  string
	URL   string
	// Group / Sound / Level 为通道可选能力；不支持的通道可忽略。
	Group string
	Sound string
	Level string
}

// Channel 推送通道。实现方自行处理配置缺失（应返回 nil 表示跳过，或返回错误）。
type Channel interface {
	Name() string
	Send(ctx context.Context, msg Message) error
}

// Multi 将消息扇出到多个通道；任一失败不短路其它通道，最终聚合错误。
type Multi struct {
	Channels []Channel
}

// Name 实现 Channel。
func (m *Multi) Name() string { return "multi" }

// Send 扇出推送。
func (m *Multi) Send(ctx context.Context, msg Message) error {
	if m == nil || len(m.Channels) == 0 {
		return nil
	}
	var errs []error
	for _, ch := range m.Channels {
		if ch == nil {
			continue
		}
		if err := ch.Send(ctx, msg); err != nil {
			errs = append(errs, fmt.Errorf("%s：%w", ch.Name(), err))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// SplitTokens 按空白/逗号/分号拆分多值配置（device key 列表等）。
func SplitTokens(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case ',', '，', ';', '；', '\n', '\r', '\t', ' ':
			return true
		default:
			return false
		}
	})
	seen := make(map[string]struct{}, len(parts))
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}
