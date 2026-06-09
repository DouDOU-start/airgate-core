package settings

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/smtp"
	"strings"

	"github.com/DouDOU-start/airgate-core/internal/auth"

	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

// publicGroups 允许公开访问的设置分组。
var publicGroups = []string{"site", "registration", "storage"}

// publicSafeKeys 允许公开的 key（不暴露敏感项）。
var publicSafeKeys = map[string]bool{
	"registration_enabled":           true,
	"email_verify_enabled":           true,
	"asset_retention_generated_days": true,
}

// settings key 常量（管理员 API Key）。
const (
	settingAdminKeyHint      = "admin_api_key_hint"
	settingAdminKeyHash      = "admin_api_key_hash"
	settingAdminKeyEncrypted = "admin_api_key_encrypted"
	settingGroupSecurity     = "security"
)

// Service 提供设置域用例编排。
type Service struct {
	repo         Repository
	apiKeySecret string // AES-GCM 加密密钥
}

// NewService 创建设置服务。
func NewService(repo Repository, apiKeySecret string) *Service {
	return &Service{repo: repo, apiKeySecret: apiKeySecret}
}

// List 查询设置列表。
func (s *Service) List(ctx context.Context, group string) ([]Setting, error) {
	items, err := s.repo.List(ctx, group)
	if err != nil {
		sdk.LoggerFromContext(ctx).Error("settings_load_failed",
			"group", group,
			sdk.LogFieldError, err)
	}
	return items, err
}

// Update 批量更新设置。
func (s *Service) Update(ctx context.Context, items []ItemInput) error {
	logger := sdk.LoggerFromContext(ctx)
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
			sdk.LogFieldError, err)
		return err
	}
	// 仅打印 key 列表；values 可能含敏感配置（API key、密钥等），绝不日志化。
	logger.Info("settings_updated", "keys", keys, "count", len(cloned))
	return nil
}

// ListPublic 获取可公开访问的设置（无需认证），按白名单过滤敏感项。
func (s *Service) ListPublic(ctx context.Context) (map[string]string, error) {
	result := make(map[string]string)

	for _, group := range publicGroups {
		list, err := s.repo.List(ctx, group)
		if err != nil {
			sdk.LoggerFromContext(ctx).Error("settings_load_failed",
				"group", group,
				sdk.LogFieldError, err)
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

// TestSMTP 测试 SMTP 连接并发送测试邮件。
func (s *Service) TestSMTP(ctx context.Context, input TestSMTPInput) error {
	logger := sdk.LoggerFromContext(ctx)
	addr := fmt.Sprintf("%s:%d", input.Host, input.Port)

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
			logger.Error("SMTP TLS 连接失败", sdk.LogFieldError, err)
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
		logger.Error("SMTP 发送测试邮件失败", sdk.LogFieldError, sendErr)
		return fmt.Errorf("%w: Send failed: %v", ErrSMTPConnection, sendErr)
	}

	return nil
}

// GenerateAdminAPIKey 生成（或重新生成）管理员 API Key 并持久化。
func (s *Service) GenerateAdminAPIKey(ctx context.Context) (GenerateAdminAPIKeyResult, error) {
	logger := sdk.LoggerFromContext(ctx)

	plainKey, hash, err := auth.GenerateAdminAPIKey()
	if err != nil {
		logger.Error("生成管理员 API Key 失败", sdk.LogFieldError, err)
		return GenerateAdminAPIKeyResult{}, fmt.Errorf("%w: %v", ErrGenerateKey, err)
	}

	encrypted, err := auth.EncryptAPIKey(plainKey, s.apiKeySecret)
	if err != nil {
		logger.Error("加密管理员 API Key 失败", sdk.LogFieldError, err)
		return GenerateAdminAPIKeyResult{}, fmt.Errorf("%w: %v", ErrEncryptKey, err)
	}

	hint := auth.AdminKeyHint(plainKey)

	items := []ItemInput{
		{Key: settingAdminKeyHint, Value: hint, Group: settingGroupSecurity},
		{Key: settingAdminKeyHash, Value: hash, Group: settingGroupSecurity},
		{Key: settingAdminKeyEncrypted, Value: encrypted, Group: settingGroupSecurity},
	}
	if err := s.Update(ctx, items); err != nil {
		logger.Error("保存管理员 API Key 失败", sdk.LogFieldError, err)
		return GenerateAdminAPIKeyResult{}, err
	}

	return GenerateAdminAPIKeyResult{Hint: hint, Key: plainKey}, nil
}
