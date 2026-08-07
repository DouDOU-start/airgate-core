package settings

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/smtp"
	"strconv"
	"strings"

	"github.com/DouDOU-start/airgate-core/internal/auth"

	"github.com/DouDOU-start/airgate-core/internal/pkg/logx"
)

// publicGroups 允许公开访问的设置分组。
// invite 组（开关+全局比例）无敏感信息，整组公开供导航栏判断是否展示邀请入口。
var publicGroups = []string{"site", "registration", "invite"}

// publicSafeKeys 允许公开的 key（不暴露敏感项）。
var publicSafeKeys = map[string]bool{
	"registration_enabled": true,
	"email_verify_enabled": true,
	// invite 组非 site，需单独加入白名单才会被 ListPublic 透出（否则加进
	// publicGroups 也不会生效，见下方 ListPublic 的 "site 全公开/其他白名单" 逻辑）。
	"invite_enabled": true,
}

// settings key 常量（管理员 API Key / SMTP）。
const (
	settingAdminKeyHint      = "admin_api_key_hint"
	settingAdminKeyHash      = "admin_api_key_hash"
	settingAdminKeyEncrypted = "admin_api_key_encrypted"
	settingGroupSecurity     = "security"
	settingGroupSMTP         = "smtp"
	settingSMTPPassword      = "smtp_password"
	settingGroupBark         = "bark"
	settingBarkDeviceKey     = "bark_device_key"
	settingGroupRiskControl  = "risk_control"
)

// MaskedValue 敏感设置在管理端回显时的掩码哨兵值。
// 读路径：掩码键已配置时回显该哨兵（明文永不出网）；
// 写路径：收到该哨兵表示"前端回显未改动，保持库中现值"；空串则真实清空。
const MaskedValue = "********"

// sensitiveGroups 管理端通用读写路径整组屏蔽的分组：security 组存放
// admin_api_key_hash / admin_api_key_encrypted 等凭证材料，只允许经
// GetAdminAPIKey / GenerateAdminAPIKey / DeleteAdminAPIKey 专用通道访问；
// risk_control 组存放审核 key 密文与风控配置，只允许经 /admin/risk-control/* 专用通道访问。
var sensitiveGroups = map[string]bool{
	settingGroupSecurity:    true,
	settingGroupRiskControl: true,
}

// maskedKeys 管理端回显时以 MaskedValue 掩码的敏感键清单（集中定义，勿散落 handler）。
var maskedKeys = map[string]bool{
	settingSMTPPassword:  true,
	settingBarkDeviceKey: true,
}

// securityKeys security 组键清单。UpsertMany 按 key 冲突合并（不校验 group），
// 仅拦 group=="security" 会被"换个 group 写同名 key"绕过，故按 key 一并拦截。
var securityKeys = map[string]bool{
	settingAdminKeyHint:      true,
	settingAdminKeyHash:      true,
	settingAdminKeyEncrypted: true,
}

// Service 提供设置域用例编排。
type Service struct {
	repo         Repository
	apiKeySecret string // AES-GCM 加密密钥
	barkTester   BarkTester
}

// SetBarkTester 注入 Bark 测试消息发送器。
func (s *Service) SetBarkTester(tester BarkTester) {
	s.barkTester = tester
}

// NewService 创建设置服务。
func NewService(repo Repository, apiKeySecret string) *Service {
	return &Service{repo: repo, apiKeySecret: apiKeySecret}
}

// List 查询设置列表。
func (s *Service) List(ctx context.Context, group string) ([]Setting, error) {
	items, err := s.repo.List(ctx, group)
	if err != nil {
		logx.LoggerFromContext(ctx).Error("settings_load_failed",
			"group", group,
			logx.LogFieldError, err)
	}
	return items, err
}

// ListMasked 管理端通用设置读路径：security 组的键整组不返回；
// 掩码键已配置（非空）时回显 MaskedValue、未配置回空串——前端据此区分
// "已配置需保持"与"尚未配置"，且明文永不出网。
func (s *Service) ListMasked(ctx context.Context, group string) ([]Setting, error) {
	items, err := s.List(ctx, group)
	if err != nil {
		return nil, err
	}
	out := make([]Setting, 0, len(items))
	for _, item := range items {
		if sensitiveGroups[item.Group] {
			continue
		}
		if maskedKeys[item.Key] && item.Value != "" {
			item.Value = MaskedValue
		}
		out = append(out, item)
	}
	return out, nil
}

// Update 批量更新设置（管理端通用写路径）。
//
// 掩码语义：掩码键的值 == MaskedValue 表示前端回显未改动，跳过不落库（保持现值）；
// 空串正常落库（保留清空能力）；其余值原样落库。
// security 组键禁止经此通用路径写入（防绕过 GenerateAdminAPIKey 专用通道）。
func (s *Service) Update(ctx context.Context, items []ItemInput) error {
	filtered := make([]ItemInput, 0, len(items))
	for _, item := range items {
		if sensitiveGroups[item.Group] || securityKeys[item.Key] {
			logx.LoggerFromContext(ctx).Warn("settings_update_rejected",
				"key", item.Key,
				logx.LogFieldReason, "security_setting_readonly")
			return fmt.Errorf("%w: %s", ErrSecuritySettingReadOnly, item.Key)
		}
		if maskedKeys[item.Key] && item.Value == MaskedValue {
			continue
		}
		filtered = append(filtered, item)
	}
	return s.upsert(ctx, filtered)
}

// upsert 内部落库路径（无掩码/security 校验），供 Update 与
// GenerateAdminAPIKey / DeleteAdminAPIKey 专用通道共用。
func (s *Service) upsert(ctx context.Context, items []ItemInput) error {
	logger := logx.LoggerFromContext(ctx)
	cloned := make([]ItemInput, 0, len(items))
	keys := make([]string, 0, len(items))
	for _, item := range items {
		cloned = append(cloned, ItemInput{
			Key:   item.Key,
			Value: item.Value,
			Group: item.Group,
		})
		keys = append(keys, item.Key)
	}
	if err := s.repo.UpsertMany(ctx, cloned); err != nil {
		logger.Error("settings_updated_failed",
			"keys", keys,
			logx.LogFieldError, err)
		return err
	}
	// 仅打印 key 列表；values 可能含敏感配置（API key、密钥等），绝不日志化。
	logger.Info("settings_updated", "keys", keys, "count", len(cloned))
	return nil
}

// NewUserDefaults 读取新用户默认余额与并发数（defaults 组），
// 缺省或读取失败时返回 balance=0、concurrency=5。
// 注册（app/auth）与管理员建用户（handler）共用此解析，避免两处漂移。
func (s *Service) NewUserDefaults(ctx context.Context) (balance float64, concurrency int) {
	concurrency = 5 // 默认值
	items, err := s.repo.List(ctx, "defaults")
	if err != nil {
		logx.LoggerFromContext(ctx).Error("settings_load_failed",
			"group", "defaults",
			logx.LogFieldError, err)
		return
	}
	for _, item := range items {
		switch item.Key {
		case "default_balance":
			if v, e := strconv.ParseFloat(strings.TrimSpace(item.Value), 64); e == nil {
				balance = v
			}
		case "default_concurrency":
			if v, e := strconv.Atoi(strings.TrimSpace(item.Value)); e == nil && v > 0 {
				concurrency = v
			}
		}
	}
	return
}

// SiteOGImage 读取分享卡片封面图设置（site 组 og_image 键），未配置时返回空串，
// 调用方据此回退到内置默认封面。供 server 层渲染 index.html 的 og:image/twitter:image 用。
func (s *Service) SiteOGImage(ctx context.Context) string {
	items, err := s.repo.List(ctx, "site")
	if err != nil {
		logx.LoggerFromContext(ctx).Error("settings_load_failed",
			"group", "site",
			logx.LogFieldError, err)
		return ""
	}
	for _, item := range items {
		if item.Key == "og_image" {
			return strings.TrimSpace(item.Value)
		}
	}
	return ""
}

// ListPublic 获取可公开访问的设置（无需认证），按白名单过滤敏感项。
func (s *Service) ListPublic(ctx context.Context) (map[string]string, error) {
	result := make(map[string]string)

	for _, group := range publicGroups {
		list, err := s.repo.List(ctx, group)
		if err != nil {
			logx.LoggerFromContext(ctx).Error("settings_load_failed",
				"group", group,
				logx.LogFieldError, err)
			continue
		}
		for _, item := range list {
			// site 分组全部公开；其他分组只公开白名单 key
			if group == "site" || publicSafeKeys[item.Key] {
				result[item.Key] = item.Value
			}
		}
	}

	return result, nil
}

// resolveTestSMTPPassword 解析测试发信实际使用的密码：
// 掩码哨兵（前端回显未改动）→ 回退读取库中存量 smtp_password；
// 其余原样使用（空串 = 无认证 / 空密码测试）。
func (s *Service) resolveTestSMTPPassword(ctx context.Context, password string) (string, error) {
	if password != MaskedValue {
		return password, nil
	}
	items, err := s.repo.List(ctx, settingGroupSMTP)
	if err != nil {
		return "", err
	}
	for _, item := range items {
		if item.Key == settingSMTPPassword {
			return item.Value, nil
		}
	}
	return "", nil
}

// resolveMaskedSetting 解析测试接口传入的敏感设置：哨兵值回退存量，其他值原样使用。
func (s *Service) resolveMaskedSetting(ctx context.Context, group, key, value string) (string, error) {
	if value != MaskedValue {
		return value, nil
	}
	items, err := s.repo.List(ctx, group)
	if err != nil {
		return "", err
	}
	for _, item := range items {
		if item.Key == key {
			return item.Value, nil
		}
	}
	return "", nil
}

// TestBark 解析掩码后的 device key，并委托基础设施层发送测试推送。
func (s *Service) TestBark(ctx context.Context, input TestBarkInput) error {
	if s.barkTester == nil {
		return fmt.Errorf("%w：发送器未初始化", ErrBarkConnection)
	}
	key, err := s.resolveMaskedSetting(ctx, settingGroupBark, settingBarkDeviceKey, input.DeviceKey)
	if err != nil {
		return fmt.Errorf("%w：读取存量 Device Key 失败：%v", ErrBarkConnection, err)
	}
	input.DeviceKey = key
	if strings.TrimSpace(input.DeviceKey) == "" {
		return fmt.Errorf("%w：请填写 Device Key", ErrBarkConnection)
	}
	if err := s.barkTester.SendTest(ctx, input); err != nil {
		return fmt.Errorf("%w：%v", ErrBarkConnection, err)
	}
	return nil
}

// TestSMTP 测试 SMTP 连接并发送测试邮件。
// 入参密码为掩码哨兵时回退库中存量密码（否则已配置的 SMTP 测试必失败）。
func (s *Service) TestSMTP(ctx context.Context, input TestSMTPInput) error {
	logger := logx.LoggerFromContext(ctx)
	addr := fmt.Sprintf("%s:%d", input.Host, input.Port)

	password, err := s.resolveTestSMTPPassword(ctx, input.Password)
	if err != nil {
		logger.Error("读取存量 SMTP 密码失败", logx.LogFieldError, err)
		return fmt.Errorf("读取存量 SMTP 密码失败: %w", err)
	}
	input.Password = password

	// 构造邮件内容
	subject := "AirGate SMTP Test"
	body := "This is a test email from AirGate to verify your SMTP configuration."
	msg := strings.Join([]string{
		"From: " + input.From,
		"To: " + input.To,
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"",
		body,
	}, "\r\n")

	var smtpAuth smtp.Auth
	if input.Username != "" {
		smtpAuth = smtp.PlainAuth("", input.Username, input.Password, input.Host)
	}

	var sendErr error
	if input.UseTLS {
		// TLS 直连
		tlsConfig := &tls.Config{ServerName: input.Host}
		conn, err := tls.Dial("tcp", addr, tlsConfig)
		if err != nil {
			logger.Error("SMTP TLS 连接失败", logx.LogFieldError, err)
			return fmt.Errorf("%w: TLS connection failed: %v", ErrSMTPConnection, err)
		}
		defer func() { _ = conn.Close() }()

		client, err := smtp.NewClient(conn, input.Host)
		if err != nil {
			return fmt.Errorf("%w: SMTP client error: %v", ErrSMTPConnection, err)
		}
		defer func() { _ = client.Close() }()

		if smtpAuth != nil {
			if err := client.Auth(smtpAuth); err != nil {
				return fmt.Errorf("%w: SMTP auth failed: %v", ErrSMTPConnection, err)
			}
		}
		if err := client.Mail(input.From); err != nil {
			return fmt.Errorf("%w: SMTP MAIL FROM error: %v", ErrSMTPConnection, err)
		}
		if err := client.Rcpt(input.To); err != nil {
			return fmt.Errorf("%w: SMTP RCPT TO error: %v", ErrSMTPConnection, err)
		}
		w, err := client.Data()
		if err != nil {
			return fmt.Errorf("%w: SMTP DATA error: %v", ErrSMTPConnection, err)
		}
		_, sendErr = w.Write([]byte(msg))
		_ = w.Close()
	} else {
		sendErr = smtp.SendMail(addr, smtpAuth, input.From, []string{input.To}, []byte(msg))
	}

	if sendErr != nil {
		logger.Error("SMTP 发送测试邮件失败", logx.LogFieldError, sendErr)
		return fmt.Errorf("%w: Send failed: %v", ErrSMTPConnection, sendErr)
	}

	return nil
}

// GenerateAdminAPIKey 生成（或重新生成）管理员 API Key 并持久化。
func (s *Service) GenerateAdminAPIKey(ctx context.Context) (GenerateAdminAPIKeyResult, error) {
	logger := logx.LoggerFromContext(ctx)

	plainKey, hash, err := auth.GenerateAdminAPIKey()
	if err != nil {
		logger.Error("生成管理员 API Key 失败", logx.LogFieldError, err)
		return GenerateAdminAPIKeyResult{}, fmt.Errorf("%w: %v", ErrGenerateKey, err)
	}

	encrypted, err := auth.EncryptAPIKey(plainKey, s.apiKeySecret)
	if err != nil {
		logger.Error("加密管理员 API Key 失败", logx.LogFieldError, err)
		return GenerateAdminAPIKeyResult{}, fmt.Errorf("%w: %v", ErrEncryptKey, err)
	}

	hint := auth.AdminKeyHint(plainKey)

	items := []ItemInput{
		{Key: settingAdminKeyHint, Value: hint, Group: settingGroupSecurity},
		{Key: settingAdminKeyHash, Value: hash, Group: settingGroupSecurity},
		{Key: settingAdminKeyEncrypted, Value: encrypted, Group: settingGroupSecurity},
	}
	// security 组键经通用 Update 已被拒写，走内部 upsert 专用通道。
	if err := s.upsert(ctx, items); err != nil {
		logger.Error("保存管理员 API Key 失败", logx.LogFieldError, err)
		return GenerateAdminAPIKeyResult{}, err
	}

	return GenerateAdminAPIKeyResult{Hint: hint, Key: plainKey}, nil
}

// DeleteAdminAPIKey 删除管理员 API Key（把三个 security 键置空）。
// security 组键经通用 Update 已被拒写，此处为唯一删除通道。
func (s *Service) DeleteAdminAPIKey(ctx context.Context) error {
	items := []ItemInput{
		{Key: settingAdminKeyHint, Value: "", Group: settingGroupSecurity},
		{Key: settingAdminKeyHash, Value: "", Group: settingGroupSecurity},
		{Key: settingAdminKeyEncrypted, Value: "", Group: settingGroupSecurity},
	}
	return s.upsert(ctx, items)
}
