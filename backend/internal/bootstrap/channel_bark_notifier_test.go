package bootstrap

import (
	"context"
	"strings"
	"testing"
	"time"

	appchannel "github.com/DouDOU-start/airgate-core/internal/app/channel"
	appsettings "github.com/DouDOU-start/airgate-core/internal/app/settings"
	"github.com/DouDOU-start/airgate-core/internal/infra/bark"
	"github.com/DouDOU-start/airgate-core/internal/probe"
)

type barkSettingsStub struct {
	groups map[string][]appsettings.Setting
}

func (s barkSettingsStub) List(_ context.Context, group string) ([]appsettings.Setting, error) {
	return s.groups[group], nil
}

type barkChannelStub struct {
	key appchannel.ChannelKey
}

func (s barkChannelStub) FindKeyByID(_ context.Context, _ int) (appchannel.ChannelKey, error) {
	return s.key, nil
}

type barkSenderStub struct {
	server    string
	deviceKey string
	msg       bark.Message
}

func (s *barkSenderStub) Push(_ context.Context, server, deviceKey string, message bark.Message) error {
	s.server = server
	s.deviceKey = deviceKey
	s.msg = message
	return nil
}

func TestShouldSendBarkHealthEvent(t *testing.T) {
	config := channelBarkConfig{}
	if shouldSendBarkHealthEvent(config, probe.HealthEvent{OldHealth: probe.HealthHealthy, NewHealth: probe.HealthDegraded}) {
		t.Fatal("默认不应通知降级")
	}
	config.NotifyDegraded = true
	if !shouldSendBarkHealthEvent(config, probe.HealthEvent{OldHealth: probe.HealthHealthy, NewHealth: probe.HealthDegraded}) {
		t.Fatal("开启后应通知降级")
	}
	if !shouldSendBarkHealthEvent(config, probe.HealthEvent{OldHealth: probe.HealthDegraded, NewHealth: probe.HealthSuspended}) {
		t.Fatal("应通知暂停")
	}
	if !shouldSendBarkHealthEvent(config, probe.HealthEvent{OldHealth: probe.HealthRecovering, NewHealth: probe.HealthHealthy}) {
		t.Fatal("应通知恢复")
	}
	if shouldSendBarkHealthEvent(config, probe.HealthEvent{OldHealth: probe.HealthSuspended, NewHealth: probe.HealthRecovering}) {
		t.Fatal("恢复观察中不应通知")
	}
}

func TestBuildChannelHealthBarkMessage(t *testing.T) {
	msg := buildChannelHealthBarkMessage(
		"Demo",
		channelBarkConfig{Group: "AirGate", Level: "timeSensitive", DetailURL: "https://example.com/channels"},
		appchannel.ChannelKey{ID: 9, Name: "主密钥", ChannelID: 3, ChannelName: "OpenAI"},
		probe.HealthEvent{
			KeyID:      9,
			OldHealth:  probe.HealthDegraded,
			NewHealth:  probe.HealthSuspended,
			Failures:   6,
			Reason:     "upstream 401",
			OccurredAt: time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC),
		},
	)
	if !strings.Contains(msg.Title, "已暂停") {
		t.Fatalf("title = %q", msg.Title)
	}
	if !strings.Contains(msg.Body, "OpenAI") || !strings.Contains(msg.Body, "主密钥") || !strings.Contains(msg.Body, "upstream 401") {
		t.Fatalf("body = %q", msg.Body)
	}
	if msg.URL != "https://example.com/channels" || msg.Group != "AirGate" {
		t.Fatalf("url/group = %q / %q", msg.URL, msg.Group)
	}
}

func TestChannelBarkNotifierSendsSuspendedAlert(t *testing.T) {
	sender := &barkSenderStub{}
	notifier := &channelBarkNotifier{
		settings: barkSettingsStub{groups: map[string][]appsettings.Setting{
			"bark": {
				{Key: "bark_enabled", Value: "true"},
				{Key: "bark_server", Value: "https://bark.example.com"},
				{Key: "bark_device_key", Value: "device-key-1"},
				{Key: "bark_group", Value: "Ops"},
				{Key: "bark_level", Value: "critical"},
				{Key: "bark_alert_detail_url", Value: "https://example.com/admin/channels"},
			},
			"site": {{Key: "site_name", Value: "AirGate"}},
		}},
		channels: barkChannelStub{key: appchannel.ChannelKey{
			ID: 12, Name: "备用", ChannelID: 2, ChannelName: "Claude",
		}},
		sender: sender,
	}

	err := notifier.Notify(t.Context(), probe.HealthEvent{
		KeyID:      12,
		OldHealth:  probe.HealthDegraded,
		NewHealth:  probe.HealthSuspended,
		Failures:   6,
		Reason:     "连续失败",
		OccurredAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("Notify() 返回错误：%v", err)
	}
	if sender.server != "https://bark.example.com" {
		t.Fatalf("server = %q", sender.server)
	}
	if sender.deviceKey != "device-key-1" {
		t.Fatalf("deviceKey = %q", sender.deviceKey)
	}
	if sender.msg.Group != "Ops" || sender.msg.Level != "critical" {
		t.Fatalf("msg = %#v", sender.msg)
	}
	if !strings.Contains(sender.msg.Body, "Claude") {
		t.Fatalf("body = %q", sender.msg.Body)
	}
}

func TestChannelBarkNotifierSendTest(t *testing.T) {
	sender := &barkSenderStub{}
	notifier := &channelBarkNotifier{sender: sender}
	err := notifier.SendTest(t.Context(), appsettings.TestBarkInput{
		Server:    "",
		DeviceKey: "abc123",
		Group:     "",
		Level:     "",
	})
	if err != nil {
		t.Fatalf("SendTest() 返回错误：%v", err)
	}
	if sender.server != defaultBarkServer {
		t.Fatalf("默认 server = %q", sender.server)
	}
	if sender.deviceKey != "abc123" {
		t.Fatalf("deviceKey = %q", sender.deviceKey)
	}
	if sender.msg.Group != defaultBarkGroup || sender.msg.Level != defaultBarkLevel {
		t.Fatalf("默认 group/level = %q / %q", sender.msg.Group, sender.msg.Level)
	}
}
