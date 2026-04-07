package handler

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	appauth "github.com/DouDOU-start/airgate-core/internal/app/auth"
	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/infra/mailer"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// Login 用户登录。
func (h *AuthHandler) Login(c *gin.Context) {
	var req dto.LoginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	result, err := h.service.Login(c.Request.Context(), appauth.LoginInput{
		Email:    req.Email,
		Password: req.Password,
	})
	if err != nil {
		httpCode, message, unauthorized := h.handleLoginError(err)
		if unauthorized && httpCode == 401 {
			response.Unauthorized(c, message)
			return
		}
		if httpCode == 403 {
			response.Forbidden(c, message)
			return
		}
		if httpCode == 400 {
			response.BadRequest(c, message)
			return
		}
		response.InternalError(c, message)
		return
	}

	response.Success(c, dto.LoginResp{
		Token: result.Token,
		User:  userToResp(result.User),
	})
}

// LoginByAPIKey 使用 API Key 登录（仅能查看该 Key 的使用记录）。
func (h *AuthHandler) LoginByAPIKey(c *gin.Context) {
	var req dto.APIKeyLoginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	if !strings.HasPrefix(req.Key, "sk-") {
		response.BadRequest(c, "无效的 API Key 格式")
		return
	}

	info, err := auth.ValidateAPIKeyForLogin(c.Request.Context(), h.db, req.Key)
	if err != nil {
		switch err {
		case auth.ErrAPIKeyExpired:
			response.Unauthorized(c, "API Key 已过期")
		default:
			response.Unauthorized(c, "无效的 API Key")
		}
		return
	}

	// 查询用户信息
	user, err := h.service.FindByID(c.Request.Context(), info.UserID)
	if err != nil {
		response.InternalError(c, "查询用户失败")
		return
	}

	if user.Status != "active" {
		response.Forbidden(c, "用户已被禁用")
		return
	}

	// 签发带 api_key_id 的 JWT
	token, err := h.jwtMgr.GenerateAPIKeyToken(user.ID, user.Role, user.Email, info.KeyID)
	if err != nil {
		response.InternalError(c, "生成 Token 失败")
		return
	}

	response.Success(c, dto.LoginResp{
		Token:      token,
		User:       userToResp(user),
		APIKeyID:   int64(info.KeyID),
		APIKeyName: info.KeyName,
	})
}

// Register 用户注册。
func (h *AuthHandler) Register(c *gin.Context) {
	// 检查是否允许注册
	if !h.isRegistrationEnabled(c) {
		response.Forbidden(c, "注册功能已关闭")
		return
	}

	var req dto.RegisterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	// 检查是否开启了邮箱验证
	if h.isEmailVerifyEnabled(c) {
		if req.VerifyCode == "" {
			response.BadRequest(c, "请输入验证码")
			return
		}
		if !h.codeStore.Verify(req.Email, req.VerifyCode) {
			response.BadRequest(c, "验证码无效或已过期")
			return
		}
	}

	// 读取新用户默认值
	defaultBalance, defaultConcurrency := h.getNewUserDefaults(c)

	result, err := h.service.Register(c.Request.Context(), appauth.RegisterInput{
		Email:          req.Email,
		Password:       req.Password,
		Username:       req.Username,
		Balance:        defaultBalance,
		MaxConcurrency: defaultConcurrency,
	})
	if err != nil {
		httpCode, message := h.handleRegisterError(err)
		if httpCode == 400 {
			response.BadRequest(c, message)
			return
		}
		response.InternalError(c, message)
		return
	}

	response.Success(c, dto.LoginResp{
		Token: result.Token,
		User:  userToResp(result.User),
	})
}

// SendVerifyCode 发送邮箱验证码。
func (h *AuthHandler) SendVerifyCode(c *gin.Context) {
	var req dto.SendVerifyCodeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BindError(c, err)
		return
	}

	// 检查邮箱是否已注册
	exists, err := h.service.EmailExists(c.Request.Context(), req.Email)
	if err != nil {
		response.InternalError(c, "检查邮箱失败")
		return
	}
	if exists {
		response.BadRequest(c, "该邮箱已被注册")
		return
	}

	// 生成验证码
	code := h.codeStore.Generate(req.Email)

	// 从设置读取 SMTP 配置并发送
	m, err := h.buildMailer(c)
	if err != nil {
		slog.Error("构建邮件发送器失败", "error", err)
		response.InternalError(c, "邮件服务未配置")
		return
	}

	// 读取站点名称和邮件模板
	siteName := "AirGate"
	emailSubjectTpl := ""
	emailBodyTpl := ""

	smtpSettings, _ := h.settingsService.List(c.Request.Context(), "smtp")
	for _, s := range smtpSettings {
		switch s.Key {
		case "email_template_subject":
			emailSubjectTpl = s.Value
		case "email_template_body":
			emailBodyTpl = s.Value
		}
	}
	siteSettings, _ := h.settingsService.List(c.Request.Context(), "site")
	for _, s := range siteSettings {
		if s.Key == "site_name" && s.Value != "" {
			siteName = s.Value
		}
	}

	// 默认模板
	if emailSubjectTpl == "" {
		emailSubjectTpl = "{{site_name}} - 邮箱验证码"
	}
	if emailBodyTpl == "" {
		emailBodyTpl = `<div style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; max-width: 420px; margin: 0 auto; background: #ffffff; border-radius: 8px; border: 1px solid #e5e7eb;">
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
	}

	// 变量替换
	replacer := strings.NewReplacer(
		"{{site_name}}", siteName,
		"{{code}}", code,
		"{{email}}", req.Email,
	)
	subject := replacer.Replace(emailSubjectTpl)
	body := replacer.Replace(emailBodyTpl)

	if err := m.Send(req.Email, subject, body); err != nil {
		slog.Error("发送验证码邮件失败", "email", req.Email, "error", err)
		response.InternalError(c, fmt.Sprintf("发送邮件失败: %v", err))
		return
	}

	response.Success(c, nil)
}

// RefreshToken 刷新 JWT Token。
func (h *AuthHandler) RefreshToken(c *gin.Context) {
	identity, ok := authIdentityFromContext(c)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}

	token, err := h.service.RefreshToken(identity)
	if err != nil {
		response.InternalError(c, "刷新 Token 失败")
		return
	}

	response.Success(c, dto.RefreshResp{
		Token: token,
	})
}

// isRegistrationEnabled 检查是否允许注册（默认允许）。
func (h *AuthHandler) isRegistrationEnabled(c *gin.Context) bool {
	settings, err := h.settingsService.List(c.Request.Context(), "registration")
	if err != nil {
		return true
	}
	for _, s := range settings {
		if s.Key == "registration_enabled" && s.Value == "false" {
			return false
		}
	}
	return true
}

// getNewUserDefaults 读取新用户默认余额和并发数。
func (h *AuthHandler) getNewUserDefaults(c *gin.Context) (balance float64, concurrency int) {
	concurrency = 5 // 默认值
	settings, err := h.settingsService.List(c.Request.Context(), "defaults")
	if err != nil {
		return
	}
	for _, s := range settings {
		switch s.Key {
		case "default_balance":
			if v, e := strconv.ParseFloat(strings.TrimSpace(s.Value), 64); e == nil {
				balance = v
			}
		case "default_concurrency":
			if v, e := strconv.Atoi(strings.TrimSpace(s.Value)); e == nil && v > 0 {
				concurrency = v
			}
		}
	}
	return
}

// isEmailVerifyEnabled 检查是否开启了邮箱验证。
func (h *AuthHandler) isEmailVerifyEnabled(c *gin.Context) bool {
	settings, err := h.settingsService.List(c.Request.Context(), "registration")
	if err != nil {
		return false
	}
	for _, s := range settings {
		if s.Key == "email_verify_enabled" && s.Value == "true" {
			return true
		}
	}
	return false
}

// buildMailer 从系统设置构建邮件发送器。
func (h *AuthHandler) buildMailer(c *gin.Context) (*mailer.Mailer, error) {
	settings, err := h.settingsService.List(c.Request.Context(), "smtp")
	if err != nil {
		return nil, err
	}

	cfg := mailer.Config{}
	for _, s := range settings {
		switch s.Key {
		case "smtp_host":
			cfg.Host = s.Value
		case "smtp_port":
			cfg.Port, _ = strconv.Atoi(s.Value)
		case "smtp_username":
			cfg.Username = s.Value
		case "smtp_password":
			cfg.Password = s.Value
		case "smtp_from_email":
			cfg.FromAddr = s.Value
		case "smtp_from_name":
			cfg.FromName = s.Value
		case "smtp_use_tls":
			cfg.UseTLS = s.Value == "true"
		}
	}

	if cfg.Host == "" {
		return nil, fmt.Errorf("SMTP 未配置")
	}
	if cfg.Port == 0 {
		cfg.Port = 587
	}
	return mailer.New(cfg), nil
}
