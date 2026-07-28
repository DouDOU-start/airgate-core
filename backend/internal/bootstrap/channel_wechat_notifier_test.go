package bootstrap

import (
	"context"
	"strings"
	"testing"
	"time"

	appchannel "github.com/DouDOU-start/airgate-core/internal/app/channel"
	appsettings "github.com/DouDOU-start/airgate-core/internal/app/settings"
	"github.com/DouDOU-start/airgate-core/internal/infra/wechat"
	"github.com/DouDOU-start/airgate-core/internal/probe"
)

type notifierSettingsStub struct {
	groups map[string][]appsettings.Setting
}

func (s notifierSettingsStub) List(_ context.Context, group string) ([]appsettings.Setting, error) {
	return s.groups[group], nil
}

func (s notifierSettingsStub) Update(_ context.Context, items []appsettings.ItemInput) error {
	for _, item := range items {
		groupItems := s.groups[item.Group]
		updated := false
		for i := range groupItems {
			if groupItems[i].Key == item.Key {
				groupItems[i].Value = item.Value
				updated = true
				break
			}
		}
		if !updated {
			groupItems = append(groupItems, appsettings.Setting(item))
		}
		s.groups[item.Group] = groupItems
	}
	return nil
}

type notifierChannelStub struct {
	key appchannel.ChannelKey
}

func (s notifierChannelStub) FindKeyByID(_ context.Context, _ int) (appchannel.ChannelKey, error) {
	return s.key, nil
}

type notifierSenderStub struct {
	credentials wechat.Credentials
	message     wechat.TemplateMessage
	oauthOpenID string
}

func (s *notifierSenderStub) SendTemplate(_ context.Context, credentials wechat.Credentials, message wechat.TemplateMessage) error {
	s.credentials = credentials
	s.message = message
	return nil
}

func (s *notifierSenderStub) ExchangeOAuthCode(_ context.Context, credentials wechat.Credentials, _ string) (string, error) {
	s.credentials = credentials
	if s.oauthOpenID == "" {
		return "openid-oauth", nil
	}
	return s.oauthOpenID, nil
}

func TestSplitOpenIDsDeduplicates(t *testing.T) {
	got := splitOpenIDs("openid-a\nopenid-b，openid-a; openid-c")
	want := []string{"openid-a", "openid-b", "openid-c"}
	if len(got) != len(want) {
		t.Fatalf("OpenID 数量 = %d，期望 %d：%v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("OpenID[%d] = %q，期望 %q", i, got[i], want[i])
		}
	}
}

func TestShouldSendWechatHealthEvent(t *testing.T) {
	config := channelWechatConfig{}
	if shouldSendWechatHealthEvent(config, probe.HealthEvent{OldHealth: probe.HealthHealthy, NewHealth: probe.HealthDegraded}) {
		t.Fatal("未开启降级提醒时不应发送")
	}
	config.NotifyDegraded = true
	if !shouldSendWechatHealthEvent(config, probe.HealthEvent{OldHealth: probe.HealthHealthy, NewHealth: probe.HealthDegraded}) {
		t.Fatal("开启降级提醒后应发送")
	}
	if !shouldSendWechatHealthEvent(config, probe.HealthEvent{OldHealth: probe.HealthDegraded, NewHealth: probe.HealthSuspended}) {
		t.Fatal("暂停状态必须发送")
	}
	if !shouldSendWechatHealthEvent(config, probe.HealthEvent{OldHealth: probe.HealthRecovering, NewHealth: probe.HealthHealthy}) {
		t.Fatal("恢复状态必须发送")
	}
	if shouldSendWechatHealthEvent(config, probe.HealthEvent{OldHealth: probe.HealthSuspended, NewHealth: probe.HealthRecovering}) {
		t.Fatal("恢复观察状态不应发送")
	}
}

func TestBuildChannelHealthMessage(t *testing.T) {
	eventTime := time.Date(2026, 7, 28, 8, 30, 0, 0, time.UTC)
	message := buildChannelHealthMessage(
		"AirGate",
		"openid-1",
		"template-1",
		"https://example.com/admin/channels",
		appchannel.ChannelKey{ID: 7, ChannelID: 3, ChannelName: "主渠道", Name: "密钥 A"},
		probe.HealthEvent{
			KeyID:      7,
			OldHealth:  probe.HealthDegraded,
			NewHealth:  probe.HealthSuspended,
			Failures:   6,
			Reason:     "上游请求连续失败",
			OccurredAt: eventTime,
		},
	)
	if message.ToUser != "openid-1" || message.TemplateID != "template-1" {
		t.Fatalf("消息基本参数不正确：%+v", message)
	}
	if message.Data["keyword1"].Value != "主渠道 / 密钥 A" || message.Data["keyword2"].Value != "已暂停" {
		t.Fatalf("消息状态字段不正确：%+v", message.Data)
	}
	if message.Data["keyword5"].Value != "连续失败 6 次" {
		t.Fatalf("消息失败计数不正确：%+v", message.Data["keyword5"])
	}
}

func TestChannelWechatNotifierSendsSuspendedAlert(t *testing.T) {
	sender := &notifierSenderStub{}
	notifier := &channelWechatNotifier{
		settings: notifierSettingsStub{groups: map[string][]appsettings.Setting{
			"wechat": {
				{Key: "wechat_enabled", Value: "true"},
				{Key: "wechat_app_id", Value: "wx-test"},
				{Key: "wechat_app_secret", Value: "secret"},
				{Key: "wechat_template_id", Value: "template-1"},
				{Key: "wechat_admin_open_ids", Value: "openid-1"},
			},
			"site": {{Key: "site_name", Value: "测试站点"}},
		}},
		channels: notifierChannelStub{key: appchannel.ChannelKey{
			ID: 7, ChannelID: 3, ChannelName: "主渠道", Name: "密钥 A",
		}},
		sender: sender,
	}

	err := notifier.Notify(t.Context(), probe.HealthEvent{
		KeyID:      7,
		OldHealth:  probe.HealthDegraded,
		NewHealth:  probe.HealthSuspended,
		Failures:   6,
		Reason:     "上游请求连续失败",
		OccurredAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("Notify() 返回错误：%v", err)
	}
	if sender.credentials.AppID != "wx-test" || sender.credentials.AppSecret != "secret" {
		t.Fatalf("微信凭证不正确：%+v", sender.credentials)
	}
	if sender.message.ToUser != "openid-1" || sender.message.Data["first"].Value != "测试站点 渠道状态提醒" {
		t.Fatalf("发送的模板消息不正确：%+v", sender.message)
	}
}

func TestChannelWechatNotifierCompletesQRCodeBinding(t *testing.T) {
	settings := notifierSettingsStub{groups: map[string][]appsettings.Setting{
		"wechat": {
			{Key: "wechat_app_id", Value: "wx-test", Group: "wechat"},
			{Key: "wechat_app_secret", Value: "secret", Group: "wechat"},
			{Key: "wechat_oauth_callback_url", Value: "https://example.com/api/v1/wechat/admin-bind/callback", Group: "wechat"},
		},
	}}
	sender := &notifierSenderStub{oauthOpenID: "openid-bound"}
	notifier := &channelWechatNotifier{
		settings:    settings,
		sender:      sender,
		bindRecords: make(map[string]wechatBindRecord),
	}

	session, err := notifier.CreateBind(t.Context())
	if err != nil {
		t.Fatalf("CreateBind() 返回错误：%v", err)
	}
	if session.ID == "" || !strings.Contains(session.OAuthURL, "open.weixin.qq.com/connect/oauth2/authorize") ||
		!strings.Contains(session.OAuthURL, "scope=snsapi_base") {
		t.Fatalf("绑定会话不正确：%+v", session)
	}

	status, err := notifier.CompleteBind(t.Context(), "code-1", session.ID)
	if err != nil {
		t.Fatalf("CompleteBind() 返回错误：%v", err)
	}
	if status.Status != "bound" || status.OpenIDHint != "••••••-bound" {
		t.Fatalf("绑定状态不正确：%+v", status)
	}
	items := settings.groups["wechat"]
	found := false
	for _, item := range items {
		if item.Key == "wechat_admin_open_ids" && item.Value == "openid-bound" {
			found = true
		}
	}
	if !found {
		t.Fatalf("绑定后未保存管理员 OpenID：%+v", items)
	}
}
