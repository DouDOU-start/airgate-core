package bootstrap

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/redis/go-redis/v9"

	appchannel "github.com/DouDOU-start/airgate-core/internal/app/channel"
	appsettings "github.com/DouDOU-start/airgate-core/internal/app/settings"
	"github.com/DouDOU-start/airgate-core/internal/infra/store"
	"github.com/DouDOU-start/airgate-core/internal/infra/wechat"
	"github.com/DouDOU-start/airgate-core/internal/probe"
)

const (
	wechatSettingsGroup = "wechat"
	wechatDedupPrefix   = "agw:wechat:alert:"
	wechatDedupTTL      = 10 * time.Minute
	wechatBindPrefix    = "agw:wechat:admin_bind:"
	wechatBindTTL       = 10 * time.Minute
	wechatOAuthBaseURL  = "https://open.weixin.qq.com/connect/oauth2/authorize"
)

type wechatSettingsStore interface {
	List(ctx context.Context, group string) ([]appsettings.Setting, error)
	Update(ctx context.Context, items []appsettings.ItemInput) error
}

type channelKeyFinder interface {
	FindKeyByID(ctx context.Context, keyID int) (appchannel.ChannelKey, error)
}

type wechatTemplateSender interface {
	SendTemplate(ctx context.Context, credentials wechat.Credentials, message wechat.TemplateMessage) error
	ExchangeOAuthCode(ctx context.Context, credentials wechat.Credentials, code string) (string, error)
}

type channelWechatConfig struct {
	Enabled        bool
	AppID          string
	AppSecret      string
	TemplateID     string
	OpenIDs        []string
	DetailURL      string
	OAuthCallback  string
	NotifyDegraded bool
}

type wechatBindRecord struct {
	Status    string    `json:"status"`
	OpenID    string    `json:"open_id,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
}

// channelWechatNotifier 将渠道健康状态变化发送到管理员配置的微信公众号 OpenID。
// 设置每次发送前动态读取，管理员保存后无需重启服务。
type channelWechatNotifier struct {
	settings wechatSettingsStore
	channels channelKeyFinder
	sender   wechatTemplateSender
	rdb      *redis.Client

	bindMu      sync.Mutex
	bindRecords map[string]wechatBindRecord
}

func newChannelWechatNotifier(settings *appsettings.Service, channels *store.ChannelStore, rdb *redis.Client) *channelWechatNotifier {
	return &channelWechatNotifier{
		settings:    settings,
		channels:    channels,
		sender:      wechat.New(rdb),
		rdb:         rdb,
		bindRecords: make(map[string]wechatBindRecord),
	}
}

// CreateBind 创建一次性管理员扫码绑定会话。
func (n *channelWechatNotifier) CreateBind(ctx context.Context) (appsettings.WeChatBindSession, error) {
	config, err := n.loadConfig(ctx)
	if err != nil {
		return appsettings.WeChatBindSession{}, err
	}
	if config.AppID == "" || config.AppSecret == "" {
		return appsettings.WeChatBindSession{}, errors.New("请先保存公众号 AppID 和 AppSecret")
	}
	callbackURL, err := validateWeChatOAuthCallback(config.OAuthCallback)
	if err != nil {
		return appsettings.WeChatBindSession{}, err
	}

	state, err := newWeChatBindState()
	if err != nil {
		return appsettings.WeChatBindSession{}, fmt.Errorf("生成绑定凭证失败：%w", err)
	}
	expiresAt := time.Now().Add(wechatBindTTL)
	if err := n.saveBindRecord(ctx, state, wechatBindRecord{Status: "pending", ExpiresAt: expiresAt}); err != nil {
		return appsettings.WeChatBindSession{}, fmt.Errorf("保存绑定会话失败：%w", err)
	}

	endpoint, _ := url.Parse(wechatOAuthBaseURL)
	query := endpoint.Query()
	query.Set("appid", config.AppID)
	query.Set("redirect_uri", callbackURL)
	query.Set("response_type", "code")
	query.Set("scope", "snsapi_base")
	query.Set("state", state)
	endpoint.RawQuery = query.Encode()
	oauthURL := endpoint.String() + "#wechat_redirect"
	return appsettings.WeChatBindSession{ID: state, OAuthURL: oauthURL, ExpiresAt: expiresAt}, nil
}

// CompleteBind 处理微信网页授权回调并保存唯一管理员 OpenID。
func (n *channelWechatNotifier) CompleteBind(ctx context.Context, code, state string) (appsettings.WeChatBindStatus, error) {
	state = strings.TrimSpace(state)
	code = strings.TrimSpace(code)
	if state == "" || code == "" {
		return appsettings.WeChatBindStatus{}, errors.New("微信授权回调缺少 code 或 state")
	}
	record, err := n.loadBindRecord(ctx, state)
	if err != nil {
		return appsettings.WeChatBindStatus{}, err
	}
	if record.Status == "bound" && record.OpenID != "" {
		return appsettings.WeChatBindStatus{Status: "bound", OpenIDHint: openIDHint(record.OpenID)}, nil
	}
	if time.Now().After(record.ExpiresAt) {
		return appsettings.WeChatBindStatus{}, errors.New("绑定二维码已过期，请重新生成")
	}

	config, err := n.loadConfig(ctx)
	if err != nil {
		return appsettings.WeChatBindStatus{}, err
	}
	openID, err := n.sender.ExchangeOAuthCode(ctx, wechat.Credentials{
		AppID: config.AppID, AppSecret: config.AppSecret,
	}, code)
	if err != nil {
		return appsettings.WeChatBindStatus{}, err
	}
	if err := n.settings.Update(ctx, []appsettings.ItemInput{{
		Key: "wechat_admin_open_ids", Value: openID, Group: wechatSettingsGroup,
	}}); err != nil {
		return appsettings.WeChatBindStatus{}, fmt.Errorf("保存管理员 OpenID 失败：%w", err)
	}
	record.Status = "bound"
	record.OpenID = openID
	if err := n.saveBindRecord(ctx, state, record); err != nil {
		return appsettings.WeChatBindStatus{}, fmt.Errorf("更新绑定会话失败：%w", err)
	}
	return appsettings.WeChatBindStatus{Status: "bound", OpenIDHint: openIDHint(openID)}, nil
}

// BindStatus 查询一次性绑定会话状态。
func (n *channelWechatNotifier) BindStatus(ctx context.Context, id string) (appsettings.WeChatBindStatus, error) {
	record, err := n.loadBindRecord(ctx, strings.TrimSpace(id))
	if err != nil {
		return appsettings.WeChatBindStatus{}, err
	}
	status := appsettings.WeChatBindStatus{Status: record.Status}
	if record.OpenID != "" {
		status.OpenIDHint = openIDHint(record.OpenID)
	}
	return status, nil
}

// Unbind 清空唯一管理员 OpenID。
func (n *channelWechatNotifier) Unbind(ctx context.Context) error {
	return n.settings.Update(ctx, []appsettings.ItemInput{{
		Key: "wechat_admin_open_ids", Value: "", Group: wechatSettingsGroup,
	}})
}

func (n *channelWechatNotifier) Notify(ctx context.Context, event probe.HealthEvent) error {
	config, err := n.loadConfig(ctx)
	if err != nil {
		return err
	}
	if !config.Enabled || !shouldSendWechatHealthEvent(config, event) {
		return nil
	}
	if config.AppID == "" || config.AppSecret == "" || config.TemplateID == "" || len(config.OpenIDs) == 0 {
		slog.Warn("wechat_channel_alert_incomplete_config")
		return nil
	}

	key, err := n.channels.FindKeyByID(ctx, event.KeyID)
	if err != nil {
		return fmt.Errorf("读取渠道密钥信息失败：%w", err)
	}
	siteName := n.siteName(ctx)

	var sendErrors []error
	for _, openID := range config.OpenIDs {
		claimed, claimErr := n.claimEvent(ctx, event, openID)
		if claimErr != nil {
			slog.Warn("wechat_channel_alert_dedup_failed", "channel_key_id", event.KeyID, "error", claimErr)
		}
		if claimErr == nil && !claimed {
			continue
		}
		message := buildChannelHealthMessage(siteName, openID, config.TemplateID, config.DetailURL, key, event)
		if err := n.sender.SendTemplate(ctx, wechat.Credentials{
			AppID:     config.AppID,
			AppSecret: config.AppSecret,
		}, message); err != nil {
			n.releaseEvent(ctx, event, openID)
			sendErrors = append(sendErrors, fmt.Errorf("向 OpenID 尾号 %s 发送失败：%w", openIDSuffix(openID), err))
		}
	}
	if len(sendErrors) > 0 {
		return errors.Join(sendErrors...)
	}
	return nil
}

// SendTest 实现 settings.WeChatTester，使用表单当前值发送测试消息。
func (n *channelWechatNotifier) SendTest(ctx context.Context, input appsettings.TestWeChatInput) error {
	message := wechat.TemplateMessage{
		ToUser:     strings.TrimSpace(input.OpenID),
		TemplateID: strings.TrimSpace(input.TemplateID),
		URL:        strings.TrimSpace(input.DetailURL),
		Data: map[string]wechat.TemplateData{
			"first":    {Value: "AirGate 微信公众号通知接入成功"},
			"keyword1": {Value: "测试渠道 / 测试密钥"},
			"keyword2": {Value: "健康"},
			"keyword3": {Value: "这是一条测试消息"},
			"keyword4": {Value: formatWechatTime(time.Now())},
			"keyword5": {Value: "配置验证"},
			"remark":   {Value: "收到此消息表示 AppID、AppSecret、OpenID 和模板 ID 均可正常使用。"},
		},
	}
	return n.sender.SendTemplate(ctx, wechat.Credentials{
		AppID:     strings.TrimSpace(input.AppID),
		AppSecret: strings.TrimSpace(input.AppSecret),
	}, message)
}

func (n *channelWechatNotifier) loadConfig(ctx context.Context) (channelWechatConfig, error) {
	items, err := n.settings.List(ctx, wechatSettingsGroup)
	if err != nil {
		return channelWechatConfig{}, fmt.Errorf("读取微信公众号设置失败：%w", err)
	}
	config := channelWechatConfig{}
	for _, item := range items {
		switch item.Key {
		case "wechat_enabled":
			config.Enabled = strings.EqualFold(strings.TrimSpace(item.Value), "true")
		case "wechat_app_id":
			config.AppID = strings.TrimSpace(item.Value)
		case "wechat_app_secret":
			config.AppSecret = strings.TrimSpace(item.Value)
		case "wechat_template_id":
			config.TemplateID = strings.TrimSpace(item.Value)
		case "wechat_admin_open_ids":
			config.OpenIDs = splitOpenIDs(item.Value)
		case "wechat_alert_detail_url":
			config.DetailURL = strings.TrimSpace(item.Value)
		case "wechat_oauth_callback_url":
			config.OAuthCallback = strings.TrimSpace(item.Value)
		case "wechat_notify_degraded":
			config.NotifyDegraded = strings.EqualFold(strings.TrimSpace(item.Value), "true")
		}
	}
	return config, nil
}

func newWeChatBindState() (string, error) {
	random := make([]byte, 24)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}

func validateWeChatOAuthCallback(value string) (string, error) {
	value = strings.TrimSpace(value)
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", errors.New("网页授权回调地址必须是公网 HTTPS 地址")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return "", errors.New("网页授权回调地址不能使用 localhost")
	}
	return parsed.String(), nil
}

func (n *channelWechatNotifier) saveBindRecord(ctx context.Context, id string, record wechatBindRecord) error {
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if n.rdb != nil {
		ttl := time.Until(record.ExpiresAt)
		if ttl <= 0 {
			ttl = time.Minute
		}
		if err := n.rdb.Set(ctx, wechatBindPrefix+id, encoded, ttl).Err(); err == nil {
			return nil
		}
	}
	n.bindMu.Lock()
	n.bindRecords[id] = record
	n.bindMu.Unlock()
	return nil
}

func (n *channelWechatNotifier) loadBindRecord(ctx context.Context, id string) (wechatBindRecord, error) {
	if id == "" {
		return wechatBindRecord{}, errors.New("绑定会话 ID 为空")
	}
	if n.rdb != nil {
		encoded, err := n.rdb.Get(ctx, wechatBindPrefix+id).Bytes()
		if err == nil {
			var record wechatBindRecord
			if err := json.Unmarshal(encoded, &record); err != nil {
				return wechatBindRecord{}, err
			}
			return record, nil
		}
		if err != redis.Nil {
			slog.Warn("wechat_bind_redis_load_failed", "error", err)
		}
	}
	n.bindMu.Lock()
	record, ok := n.bindRecords[id]
	if ok && time.Now().After(record.ExpiresAt) {
		delete(n.bindRecords, id)
		ok = false
	}
	n.bindMu.Unlock()
	if !ok {
		return wechatBindRecord{}, errors.New("绑定会话不存在或已过期")
	}
	return record, nil
}

func openIDHint(openID string) string {
	return "••••••" + openIDSuffix(openID)
}

func (n *channelWechatNotifier) siteName(ctx context.Context) string {
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

func (n *channelWechatNotifier) claimEvent(ctx context.Context, event probe.HealthEvent, openID string) (bool, error) {
	if n.rdb == nil {
		return true, nil
	}
	key := wechatDedupKey(event, openID)
	return n.rdb.SetNX(ctx, key, "1", wechatDedupTTL).Result()
}

func (n *channelWechatNotifier) releaseEvent(ctx context.Context, event probe.HealthEvent, openID string) {
	if n.rdb == nil {
		return
	}
	key := wechatDedupKey(event, openID)
	_ = n.rdb.Del(ctx, key).Err()
}

func wechatDedupKey(event probe.HealthEvent, openID string) string {
	sum := sha256.Sum256([]byte(openID))
	return fmt.Sprintf("%s%d:%s:%x", wechatDedupPrefix, event.KeyID, event.NewHealth, sum[:6])
}

func shouldSendWechatHealthEvent(config channelWechatConfig, event probe.HealthEvent) bool {
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

func buildChannelHealthMessage(siteName, openID, templateID, detailURL string, key appchannel.ChannelKey, event probe.HealthEvent) wechat.TemplateMessage {
	state := wechatHealthLabel(event.NewHealth)
	keyName := strings.TrimSpace(key.Name)
	if keyName == "" {
		keyName = fmt.Sprintf("密钥 #%d", key.ID)
	}
	channelName := strings.TrimSpace(key.ChannelName)
	if channelName == "" {
		channelName = fmt.Sprintf("渠道 #%d", key.ChannelID)
	}

	title := fmt.Sprintf("%s 渠道状态提醒", siteName)
	remark := "请登录管理后台查看渠道详情。"
	transitionSummary := fmt.Sprintf("连续失败 %d 次", event.Failures)
	if event.NewHealth == probe.HealthHealthy {
		remark = "渠道已经恢复，系统将重新参与正常调度。"
		transitionSummary = "恢复验证已通过"
	}
	return wechat.TemplateMessage{
		ToUser:     openID,
		TemplateID: templateID,
		URL:        detailURL,
		Data: map[string]wechat.TemplateData{
			"first":    {Value: title},
			"keyword1": {Value: channelName + " / " + keyName},
			"keyword2": {Value: state},
			"keyword3": {Value: event.Reason},
			"keyword4": {Value: formatWechatTime(event.OccurredAt)},
			"keyword5": {Value: transitionSummary},
			"remark":   {Value: remark},
		},
	}
}

func splitOpenIDs(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || r == ',' || r == '，' || r == ';' || r == '；'
	})
	seen := make(map[string]struct{}, len(parts))
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		result = append(result, part)
	}
	return result
}

func wechatHealthLabel(health probe.HealthStatus) string {
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

func formatWechatTime(value time.Time) string {
	if location, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		value = value.In(location)
	} else {
		value = value.Local()
	}
	return value.Format("2006-01-02 15:04:05")
}

func openIDSuffix(openID string) string {
	runes := []rune(openID)
	if len(runes) <= 6 {
		return openID
	}
	return string(runes[len(runes)-6:])
}
