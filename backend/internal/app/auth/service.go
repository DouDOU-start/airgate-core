package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"

	corauth "github.com/DouDOU-start/airgate-core/internal/auth"
	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

// SettingsLister 读取系统设置（由 settings 服务实现）。
type SettingsLister interface {
	List(ctx context.Context, group string) ([]Setting, error)
}

// VerifyCodeStore 验证码存储接口。
type VerifyCodeStore interface {
	Generate(email string) string
	Check(email, code string) bool
	Verify(email, code string) bool
}

// MailSender 邮件发送接口。
type MailSender interface {
	Send(to, subject, body string) error
}

// MailSenderFactory 从系统设置构建邮件发送器的工厂函数。
// 由 bootstrap 层注入，避免 app 层直接依赖 infra/mailer。
type MailSenderFactory func(ctx context.Context) (MailSender, error)

// Service 提供认证域用例编排。
type Service struct {
	repo          Repository
	jwtMgr        *corauth.JWTManager
	settings      SettingsLister
	codeStore     VerifyCodeStore
	mailerFactory MailSenderFactory
}

// NewService 创建认证服务。
func NewService(repo Repository, jwtMgr *corauth.JWTManager) *Service {
	return &Service{
		repo:   repo,
		jwtMgr: jwtMgr,
	}
}

// SetSettingsLister 注入设置读取依赖（注册/验证码/邮件需要）。
func (s *Service) SetSettingsLister(sl SettingsLister) {
	s.settings = sl
}

// SetVerifyCodeStore 注入验证码存储。
func (s *Service) SetVerifyCodeStore(cs VerifyCodeStore) {
	s.codeStore = cs
}

// SetMailerFactory 注入邮件发送器工厂。
func (s *Service) SetMailerFactory(f MailSenderFactory) {
	s.mailerFactory = f
}

// Login 用户登录。
func (s *Service) Login(ctx context.Context, input LoginInput) (LoginResult, error) {
	logger := sdk.LoggerFromContext(ctx)

	user, err := s.repo.FindByEmail(ctx, input.Email)
	if err != nil {
		if IsUserMissing(err) {
			logger.Warn("user_login_rejected", sdk.LogFieldReason, "user_not_found")
		} else {
			logger.Error("user_lookup_failed", sdk.LogFieldError, err)
		}
		return LoginResult{}, ErrInvalidCredentials
	}

	if user.Status != "active" {
		logger.Warn("user_login_rejected", sdk.LogFieldReason, "user_disabled", sdk.LogFieldUserID, user.ID)
		return LoginResult{}, ErrUserDisabled
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(input.Password)); err != nil {
		logger.Warn("user_login_rejected", sdk.LogFieldReason, "password_mismatch", sdk.LogFieldUserID, user.ID)
		return LoginResult{}, ErrInvalidCredentials
	}

	token, err := s.jwtMgr.GenerateToken(user.ID, user.Role, user.Email)
	if err != nil {
		logger.Error("jwt_issue_failed", sdk.LogFieldUserID, user.ID, sdk.LogFieldError, err)
		return LoginResult{}, err
	}

	logger.Info("user_login_succeeded", sdk.LogFieldUserID, user.ID)

	return LoginResult{
		Token: token,
		User:  user,
	}, nil
}

// LoginByAPIKey 使用 API Key 登录（仅能查看该 Key 的使用记录）。
func (s *Service) LoginByAPIKey(ctx context.Context, input LoginByAPIKeyInput) (LoginByAPIKeyResult, error) {
	logger := sdk.LoggerFromContext(ctx)

	if !strings.HasPrefix(input.Key, "sk-") {
		return LoginByAPIKeyResult{}, ErrInvalidAPIKeyFormat
	}

	info, err := s.repo.ValidateAPIKeyForLogin(ctx, input.Key)
	if err != nil {
		logger.Warn("api_key_login_rejected", sdk.LogFieldError, err)
		return LoginByAPIKeyResult{}, err
	}

	// 查询用户信息
	user, err := s.repo.FindByID(ctx, info.UserID, true)
	if err != nil {
		logger.Error("user_lookup_failed", sdk.LogFieldUserID, info.UserID, sdk.LogFieldError, err)
		return LoginByAPIKeyResult{}, err
	}

	if user.Status != "active" {
		logger.Warn("api_key_login_rejected", sdk.LogFieldReason, "user_disabled", sdk.LogFieldUserID, user.ID)
		return LoginByAPIKeyResult{}, ErrUserDisabled
	}

	// 签发带 api_key_id 的受限 JWT。API Key 登录不继承管理员角色。
	token, err := s.jwtMgr.GenerateAPIKeyToken(user.ID, corauth.APIKeySessionRole, user.Email, info.KeyID)
	if err != nil {
		logger.Error("jwt_issue_failed", sdk.LogFieldUserID, user.ID, sdk.LogFieldError, err)
		return LoginByAPIKeyResult{}, err
	}

	result := LoginByAPIKeyResult{
		Token:      token,
		User:       user,
		APIKeyID:   info.KeyID,
		APIKeyName: info.KeyName,
	}

	// 填充 API Key 维度的字段（额度/已用/到期/倍率），与 GetMe 行为对齐，
	// 否则前端首屏拿不到 quota，会先显示"无限"再因 /me 刷新而跳变。
	if brief, briefErr := s.repo.GetAPIKeyBrief(ctx, info.KeyID); briefErr == nil {
		result.QuotaUSD = brief.QuotaUSD
		result.UsedQuota = brief.UsedQuota
		result.ExpiresAt = brief.ExpiresAt
		if brief.SellRate > 0 {
			result.Rate = brief.SellRate
		} else {
			result.Rate = brief.GroupRate
		}
	}

	logger.Info("api_key_login_succeeded", sdk.LogFieldUserID, user.ID, sdk.LogFieldAPIKeyID, info.KeyID)
	return result, nil
}

// Register 用户注册（含注册开关/验证码/默认值等业务编排）。
func (s *Service) Register(ctx context.Context, input RegisterInput) (LoginResult, error) {
	logger := sdk.LoggerFromContext(ctx)

	// 检查注册开关
	if !s.isRegistrationEnabled(ctx) {
		return LoginResult{}, ErrRegistrationDisabled
	}

	// 检查邮箱验证
	if s.isEmailVerifyEnabled(ctx) {
		if input.VerifyCode == "" {
			return LoginResult{}, ErrVerifyCodeRequired
		}
		if s.codeStore == nil || !s.codeStore.Verify(input.Email, input.VerifyCode) {
			return LoginResult{}, ErrVerifyCodeInvalid
		}
	}

	// 读取新用户默认值
	defaultBalance, defaultConcurrency := s.getNewUserDefaults(ctx)

	exists, err := s.repo.EmailExists(ctx, input.Email)
	if err != nil {
		logger.Error("user_lookup_failed", sdk.LogFieldError, err)
		return LoginResult{}, err
	}
	if exists {
		logger.Warn("user_register_rejected", sdk.LogFieldReason, "email_already_exists")
		return LoginResult{}, ErrEmailAlreadyExists
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		logger.Error("user_register_failed", sdk.LogFieldReason, "password_hash", sdk.LogFieldError, err)
		return LoginResult{}, err
	}

	user, err := s.repo.Create(ctx, CreateUserInput{
		Email:          input.Email,
		PasswordHash:   string(hash),
		Username:       input.Username,
		Role:           "user",
		Status:         "active",
		Balance:        defaultBalance,
		MaxConcurrency: defaultConcurrency,
	})
	if err != nil {
		logger.Error("user_register_failed", sdk.LogFieldReason, "create_user", sdk.LogFieldError, err)
		return LoginResult{}, err
	}

	token, err := s.jwtMgr.GenerateToken(user.ID, user.Role, user.Email)
	if err != nil {
		logger.Error("jwt_issue_failed", sdk.LogFieldUserID, user.ID, sdk.LogFieldError, err)
		return LoginResult{}, err
	}

	logger.Info("user_register_succeeded", sdk.LogFieldUserID, user.ID)

	return LoginResult{
		Token: token,
		User:  user,
	}, nil
}

// SendVerifyCode 发送邮箱验证码。
func (s *Service) SendVerifyCode(ctx context.Context, input SendVerifyCodeInput) error {
	logger := sdk.LoggerFromContext(ctx)

	// 检查邮箱是否已注册
	exists, err := s.repo.EmailExists(ctx, input.Email)
	if err != nil {
		logger.Error("email_check_failed", sdk.LogFieldError, err)
		return fmt.Errorf("检查邮箱失败: %w", err)
	}
	if exists {
		return ErrEmailAlreadyExists
	}

	if s.codeStore == nil {
		return ErrMailerNotConfigured
	}

	// 生成验证码
	code := s.codeStore.Generate(input.Email)

	// 构建邮件发送器
	if s.mailerFactory == nil {
		return ErrMailerNotConfigured
	}
	m, err := s.mailerFactory(ctx)
	if err != nil {
		logger.Error("构建邮件发送器失败", sdk.LogFieldError, err)
		return ErrMailerNotConfigured
	}

	// 读取站点名称和邮件模板
	siteName, subjectTpl, bodyTpl := s.loadEmailTemplate(ctx)

	// 变量替换
	replacer := strings.NewReplacer(
		"{{site_name}}", siteName,
		"{{code}}", code,
		"{{email}}", input.Email,
	)
	subject := replacer.Replace(subjectTpl)
	body := replacer.Replace(bodyTpl)

	if err := m.Send(input.Email, subject, body); err != nil {
		logger.Error("发送验证码邮件失败", "email", input.Email, sdk.LogFieldError, err)
		return fmt.Errorf("%w: %v", ErrSendMailFailed, err)
	}

	return nil
}

// FindByID 根据 ID 查询用户。
func (s *Service) FindByID(ctx context.Context, id int) (User, error) {
	return s.repo.FindByID(ctx, id, true)
}

// EmailExists 检查邮箱是否已注册。
func (s *Service) EmailExists(ctx context.Context, email string) (bool, error) {
	return s.repo.EmailExists(ctx, email)
}

// CheckVerifyCode 校验验证码（不消耗），供 VerifyCode 接口使用。
func (s *Service) CheckVerifyCode(email, code string) bool {
	if s.codeStore == nil {
		return false
	}
	return s.codeStore.Check(email, code)
}

// isRegistrationEnabled 检查是否允许注册（默认允许）。
func (s *Service) isRegistrationEnabled(ctx context.Context) bool {
	if s.settings == nil {
		return true
	}
	settings, err := s.settings.List(ctx, "registration")
	if err != nil {
		return true
	}
	for _, item := range settings {
		if item.Key == "registration_enabled" && item.Value == "false" {
			return false
		}
	}
	return true
}

// isEmailVerifyEnabled 检查是否开启了邮箱验证。
func (s *Service) isEmailVerifyEnabled(ctx context.Context) bool {
	if s.settings == nil {
		return false
	}
	settings, err := s.settings.List(ctx, "registration")
	if err != nil {
		return false
	}
	for _, item := range settings {
		if item.Key == "email_verify_enabled" && item.Value == "true" {
			return true
		}
	}
	return false
}

// getNewUserDefaults 读取新用户默认余额和并发数。
func (s *Service) getNewUserDefaults(ctx context.Context) (balance float64, concurrency int) {
	concurrency = 5 // 默认值
	if s.settings == nil {
		return
	}
	settings, err := s.settings.List(ctx, "defaults")
	if err != nil {
		return
	}
	for _, item := range settings {
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

// defaultVerifyEmailSubject 验证码邮件默认主题模板。
const defaultVerifyEmailSubject = "{{site_name}} - 邮箱验证码"

// defaultVerifyEmailBody 验证码邮件默认正文模板。
const defaultVerifyEmailBody = `<div style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; max-width: 420px; margin: 0 auto; background: #ffffff; border-radius: 8px; border: 1px solid #e5e7eb;">
<div style="padding: 32px 28px;">
<div style="font-size: 16px; font-weight: 600; color: #111; margin-bottom: 20px;">{{site_name}}</div>
<p style="color: #555; font-size: 14px; line-height: 1.6; margin: 0 0 24px;">您好，您正在注册账户，请使用以下验证码完成操作：</p>
<div style="background: #f7f8fa; border: 1px solid #eef0f3; border-radius: 8px; padding: 20px; text-align: center; margin-bottom: 24px;">
<span style="font-size: 32px; font-weight: 700; letter-spacing: 10px; color: #111;">{{code}}</span>
</div>
<p style="color: #999; font-size: 12px; line-height: 1.6; margin: 0;">验证码 10 分钟内有效，请勿泄露给他人。如非本人操作，请忽略此邮件。</p>
</div>
<div style="border-top: 1px solid #f0f0f0; padding: 14px 28px;">
<p style="color: #c0c0c0; font-size: 11px; margin: 0; text-align: center;">此邮件由 {{site_name}} 系统自动发送，请勿直接回复</p>
</div>
</div>`

// loadEmailTemplate 读取站点名称和邮件模板。
func (s *Service) loadEmailTemplate(ctx context.Context) (siteName, subjectTpl, bodyTpl string) {
	siteName = "AirGate"

	if s.settings == nil {
		return siteName, defaultVerifyEmailSubject, defaultVerifyEmailBody
	}

	smtpSettings, _ := s.settings.List(ctx, "smtp")
	for _, item := range smtpSettings {
		switch item.Key {
		case "email_template_subject":
			subjectTpl = item.Value
		case "email_template_body":
			bodyTpl = item.Value
		}
	}
	siteSettings, _ := s.settings.List(ctx, "site")
	for _, item := range siteSettings {
		if item.Key == "site_name" && item.Value != "" {
			siteName = item.Value
		}
	}

	if subjectTpl == "" {
		subjectTpl = defaultVerifyEmailSubject
	}
	if bodyTpl == "" {
		bodyTpl = defaultVerifyEmailBody
	}
	return
}

// RefreshToken 刷新 JWT。
func (s *Service) RefreshToken(ctx context.Context, identity AuthIdentity) (string, error) {
	if identity.APIKeyID > 0 {
		user, err := s.repo.ValidateAPIKeySession(ctx, identity.UserID, identity.APIKeyID)
		if err != nil {
			slog.Default().Warn("api_key_session_refresh_rejected",
				sdk.LogFieldUserID, identity.UserID,
				sdk.LogFieldAPIKeyID, identity.APIKeyID,
				sdk.LogFieldError, err,
			)
			return "", err
		}
		token, err := s.jwtMgr.GenerateAPIKeyToken(user.ID, corauth.APIKeySessionRole, user.Email, identity.APIKeyID)
		if err != nil {
			slog.Default().Error("jwt_issue_failed",
				sdk.LogFieldUserID, identity.UserID,
				sdk.LogFieldAPIKeyID, identity.APIKeyID,
				sdk.LogFieldError, err,
			)
		}
		return token, err
	}
	token, err := s.jwtMgr.GenerateToken(identity.UserID, identity.Role, identity.Email)
	if err != nil {
		slog.Default().Error("jwt_issue_failed",
			sdk.LogFieldUserID, identity.UserID,
			sdk.LogFieldError, err,
		)
	}
	return token, err
}

// IsUserMissing 判断错误是否为用户不存在。
func IsUserMissing(err error) bool {
	return errors.Is(err, ErrUserNotFound)
}
