package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	appchannel "github.com/DouDOU-start/airgate-core/internal/app/channel"
	appsettings "github.com/DouDOU-start/airgate-core/internal/app/settings"
	"github.com/DouDOU-start/airgate-core/internal/infra/bark"
	"github.com/DouDOU-start/airgate-core/internal/infra/store"
	"github.com/DouDOU-start/airgate-core/internal/notify"
	"github.com/DouDOU-start/airgate-core/internal/probe"
)

const (
	barkSettingsGroup = "bark"
	barkDedupPrefix   = "agw:bark:alert:"
	barkDedupTTL      = 10 * time.Minute
	defaultBarkServer = "https://api.day.app"
	defaultBarkGroup  = "AirGate"
	defaultBarkLevel  = "timeSensitive"
)

type barkSettingsStore interface {
	List(ctx context.Context, group string) ([]appsettings.Setting, error)
}

type barkChannelKeyFinder interface {
	FindKeyByID(ctx context.Context, keyID int) (appchannel.ChannelKey, error)
}

type barkPusher interface {
	Push(ctx context.Context, server, deviceKey string, message bark.Message) error
}

type channelBarkConfig struct {
	Enabled        bool
	Server         string
	DeviceKey      string
	Group          string
	Sound          string
	Level          string
	DetailURL      string
	NotifyDegraded bool
}

// channelBarkNotifier 将渠道健康状态变化推送到管理员配置的 Bark 设备。
// 设置每次发送前动态读取，管理员保存后无需重启服务。
//
// 扩展预留：文案构造与发送通过 notify.Message 语义表达，后续其它管理员事件
// 可复用同一 Bark 客户端与 settings 配置。
type channelBarkNotifier struct {
	settings barkSettingsStore
	channels barkChannelKeyFinder
	sender   barkPusher
	rdb      *redis.Client
}

func newChannelBarkNotifier(settings *appsettings.Service, channels *store.ChannelStore, rdb *redis.Client) *channelBarkNotifier {
	return &channelBarkNotifier{
		settings: settings,
		channels: channels,
		sender:   bark.New(),
		rdb:      rdb,
	}
}

func (n *channelBarkNotifier) Notify(ctx context.Context, event probe.HealthEvent) error {
	config, err := n.loadConfig(ctx)
	if err != nil {
		return err
	}
	if !config.Enabled || !shouldSendBarkHealthEvent(config, event) {
		return nil
	}
	if config.DeviceKey == "" {
		slog.Warn("bark_channel_alert_incomplete_config")
		return nil
	}

	key, err := n.channels.FindKeyByID(ctx, event.KeyID)
	if err != nil {
		return fmt.Errorf("读取渠道密钥信息失败：%w", err)
	}
	siteName := n.siteName(ctx)

	claimed, claimErr := n.claimEvent(ctx, event)
	if claimErr != nil {
		slog.Warn("bark_channel_alert_dedup_failed", "channel_key_id", event.KeyID, "error", claimErr)
	}
	if claimErr == nil && !claimed {
		return nil
	}

	msg := buildChannelHealthBarkMessage(siteName, config, key, event)
	if err := n.sender.Push(ctx, config.Server, config.DeviceKey, bark.Message{
		Title: msg.Title,
		Body:  msg.Body,
		URL:   msg.URL,
		Group: msg.Group,
		Sound: msg.Sound,
		Level: msg.Level,
	}); err != nil {
		n.releaseEvent(ctx, event)
		return err
	}
	return nil
}

// SendTest 实现 settings.BarkTester，使用表单当前值发送测试消息。
func (n *channelBarkNotifier) SendTest(ctx context.Context, input appsettings.TestBarkInput) error {
	deviceKey := strings.TrimSpace(input.DeviceKey)
	if deviceKey == "" {
		return errors.New("请填写 bark device key")
	}
	server := strings.TrimSpace(input.Server)
	if server == "" {
		server = defaultBarkServer
	}
	group := strings.TrimSpace(input.Group)
	if group == "" {
		group = defaultBarkGroup
	}
	level := strings.TrimSpace(input.Level)
	if level == "" {
		level = defaultBarkLevel
	}
	return n.sender.Push(ctx, server, deviceKey, bark.Message{
		Title: "AirGate Bark 通知接入成功",
		Body:  "这是一条测试消息。收到表示 Server 与 Device Key 配置正确。",
		URL:   strings.TrimSpace(input.DetailURL),
		Group: group,
		Sound: strings.TrimSpace(input.Sound),
		Level: level,
	})
}

func (n *channelBarkNotifier) loadConfig(ctx context.Context) (channelBarkConfig, error) {
	items, err := n.settings.List(ctx, barkSettingsGroup)
	if err != nil {
		return channelBarkConfig{}, fmt.Errorf("读取 Bark 设置失败：%w", err)
	}
	config := channelBarkConfig{
		Server: defaultBarkServer,
		Group:  defaultBarkGroup,
		Level:  defaultBarkLevel,
	}
	for _, item := range items {
		switch item.Key {
		case "bark_enabled":
			config.Enabled = strings.EqualFold(strings.TrimSpace(item.Value), "true")
		case "bark_server":
			if v := strings.TrimSpace(item.Value); v != "" {
				config.Server = strings.TrimRight(v, "/")
			}
		case "bark_device_key":
			config.DeviceKey = strings.TrimSpace(item.Value)
		case "bark_group":
			if v := strings.TrimSpace(item.Value); v != "" {
				config.Group = v
			}
		case "bark_sound":
			config.Sound = strings.TrimSpace(item.Value)
		case "bark_level":
			if v := strings.TrimSpace(item.Value); v != "" {
				config.Level = v
			}
		case "bark_alert_detail_url":
			config.DetailURL = strings.TrimSpace(item.Value)
		case "bark_notify_degraded":
			config.NotifyDegraded = strings.EqualFold(strings.TrimSpace(item.Value), "true")
		}
	}
	return config, nil
}

func (n *channelBarkNotifier) siteName(ctx context.Context) string {
	items, err := n.settings.List(ctx, "site")
	if err == nil {
		for _, item := range items {
			if item.Key == "site_name" && strings.TrimSpace(item.Value) != "" {
				return strings.TrimSpace(item.Value)
			}
		}
	}
	return "AirGate"
}

func (n *channelBarkNotifier) claimEvent(ctx context.Context, event probe.HealthEvent) (bool, error) {
	if n.rdb == nil {
		return true, nil
	}
	return n.rdb.SetNX(ctx, barkDedupKey(event), "1", barkDedupTTL).Result()
}

func (n *channelBarkNotifier) releaseEvent(ctx context.Context, event probe.HealthEvent) {
	if n.rdb == nil {
		return
	}
	_ = n.rdb.Del(ctx, barkDedupKey(event)).Err()
}

func barkDedupKey(event probe.HealthEvent) string {
	return fmt.Sprintf("%s%d:%s", barkDedupPrefix, event.KeyID, event.NewHealth)
}

func shouldSendBarkHealthEvent(config channelBarkConfig, event probe.HealthEvent) bool {
	switch event.NewHealth {
	case probe.HealthDegraded:
		return config.NotifyDegraded
	case probe.HealthSuspended:
		return true
	case probe.HealthHealthy:
		return event.OldHealth != probe.HealthHealthy
	default:
		return false
	}
}

func buildChannelHealthBarkMessage(siteName string, config channelBarkConfig, key appchannel.ChannelKey, event probe.HealthEvent) notify.Message {
	state := barkHealthLabel(event.NewHealth)
	keyName := strings.TrimSpace(key.Name)
	if keyName == "" {
		keyName = fmt.Sprintf("密钥 #%d", key.ID)
	}
	channelName := strings.TrimSpace(key.ChannelName)
	if channelName == "" {
		channelName = fmt.Sprintf("渠道 #%d", key.ChannelID)
	}

	title := fmt.Sprintf("%s · 渠道%s", siteName, state)
	transition := fmt.Sprintf("连续失败 %d 次", event.Failures)
	remark := "请登录管理后台查看渠道详情。"
	if event.NewHealth == probe.HealthHealthy {
		transition = "恢复验证已通过"
		remark = "渠道已恢复，将重新参与调度。"
	}

	body := strings.Join([]string{
		fmt.Sprintf("渠道：%s", channelName),
		fmt.Sprintf("密钥：%s", keyName),
		fmt.Sprintf("状态：%s", state),
		fmt.Sprintf("原因：%s", strings.TrimSpace(event.Reason)),
		fmt.Sprintf("说明：%s", transition),
		fmt.Sprintf("时间：%s", formatBarkTime(event.OccurredAt)),
		remark,
	}, "\n")

	return notify.Message{
		Title: title,
		Body:  body,
		URL:   config.DetailURL,
		Group: config.Group,
		Sound: config.Sound,
		Level: config.Level,
	}
}

func barkHealthLabel(health probe.HealthStatus) string {
	switch health {
	case probe.HealthHealthy:
		return "已恢复"
	case probe.HealthDegraded:
		return "已降级"
	case probe.HealthSuspended:
		return "已暂停"
	case probe.HealthRecovering:
		return "恢复观察中"
	default:
		return string(health)
	}
}

func formatBarkTime(value time.Time) string {
	if location, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		value = value.In(location)
	} else {
		value = value.Local()
	}
	return value.Format("2006-01-02 15:04:05")
}
