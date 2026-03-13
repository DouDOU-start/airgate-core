// Package handler 提供 HTTP 请求处理器
package handler

import (
	"log/slog"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"github.com/DouDOU-start/airgate-core/ent"
	entUser "github.com/DouDOU-start/airgate-core/ent/user"
	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// AuthHandler 认证相关 Handler
type AuthHandler struct {
	db     *ent.Client
	jwtMgr *auth.JWTManager
}

// NewAuthHandler 创建认证 Handler
func NewAuthHandler(db *ent.Client, jwtMgr *auth.JWTManager) *AuthHandler {
	return &AuthHandler{db: db, jwtMgr: jwtMgr}
}

// Login 用户登录
func (h *AuthHandler) Login(c *gin.Context) {
	var req dto.LoginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误: "+err.Error())
		return
	}

	// 查询用户
	u, err := h.db.User.Query().
		Where(entUser.Email(req.Email)).
		Only(c.Request.Context())
	if err != nil {
		response.Unauthorized(c, "邮箱或密码错误")
		return
	}

	// 检查用户状态
	if u.Status != entUser.StatusActive {
		response.Forbidden(c, "账户已禁用")
		return
	}

	// 验证密码
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Password)); err != nil {
		response.Unauthorized(c, "邮箱或密码错误")
		return
	}

	// 如果启用了 TOTP，验证验证码
	if hasTOTP(u) {
		if req.TOTPCode == "" {
			response.BadRequest(c, "需要 TOTP 验证码")
			return
		}
		if !auth.ValidateCode(getTOTPSecret(u), req.TOTPCode) {
			response.Unauthorized(c, "TOTP 验证码错误")
			return
		}
	}

	// 签发 JWT
	token, err := h.jwtMgr.GenerateToken(u.ID, string(u.Role), u.Email)
	if err != nil {
		slog.Error("生成 Token 失败", "error", err)
		response.InternalError(c, "登录失败")
		return
	}

	response.Success(c, dto.LoginResp{
		Token: token,
		User:  userToResp(u),
	})
}

// Register 用户注册
func (h *AuthHandler) Register(c *gin.Context) {
	var req dto.RegisterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误: "+err.Error())
		return
	}

	// 检查邮箱是否已注册
	exists, err := h.db.User.Query().
		Where(entUser.Email(req.Email)).
		Exist(c.Request.Context())
	if err != nil {
		slog.Error("查询用户失败", "error", err)
		response.InternalError(c, "注册失败")
		return
	}
	if exists {
		response.BadRequest(c, "邮箱已注册")
		return
	}

	// 加密密码
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		slog.Error("密码加密失败", "error", err)
		response.InternalError(c, "注册失败")
		return
	}

	// 创建用户
	u, err := h.db.User.Create().
		SetEmail(req.Email).
		SetPasswordHash(string(hash)).
		SetUsername(req.Username).
		SetRole(entUser.RoleUser).
		SetStatus(entUser.StatusActive).
		Save(c.Request.Context())
	if err != nil {
		slog.Error("创建用户失败", "error", err)
		response.InternalError(c, "注册失败")
		return
	}

	// 签发 JWT
	token, err := h.jwtMgr.GenerateToken(u.ID, string(u.Role), u.Email)
	if err != nil {
		slog.Error("生成 Token 失败", "error", err)
		response.InternalError(c, "注册失败")
		return
	}

	response.Success(c, dto.LoginResp{
		Token: token,
		User:  userToResp(u),
	})
}

// TOTPSetup 启用 TOTP — 生成密钥
func (h *AuthHandler) TOTPSetup(c *gin.Context) {
	userID, _ := c.Get(middleware.CtxKeyUserID)
	email, _ := c.Get(middleware.CtxKeyEmail)

	// 检查是否已启用 TOTP
	u, err := h.db.User.Get(c.Request.Context(), userID.(int))
	if err != nil {
		response.InternalError(c, "获取用户信息失败")
		return
	}
	if hasTOTP(u) {
		response.BadRequest(c, "TOTP 已启用")
		return
	}

	// 生成密钥
	secret, uri, err := auth.GenerateSecret(email.(string))
	if err != nil {
		slog.Error("生成 TOTP 密钥失败", "error", err)
		response.InternalError(c, "生成 TOTP 密钥失败")
		return
	}

	// 临时保存密钥（需要验证后才正式启用）
	// 先存到数据库，但在 TOTPVerify 中才真正确认
	_, err = h.db.User.UpdateOneID(userID.(int)).
		SetTotpSecret(secret).
		Save(c.Request.Context())
	if err != nil {
		slog.Error("保存 TOTP 密钥失败", "error", err)
		response.InternalError(c, "保存 TOTP 密钥失败")
		return
	}

	response.Success(c, dto.TOTPSetupResp{
		Secret: secret,
		URI:    uri,
	})
}

// TOTPVerify 验证 TOTP 验证码（首次启用时确认）
func (h *AuthHandler) TOTPVerify(c *gin.Context) {
	userID, _ := c.Get(middleware.CtxKeyUserID)

	var req dto.TOTPVerifyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误: "+err.Error())
		return
	}

	u, err := h.db.User.Get(c.Request.Context(), userID.(int))
	if err != nil {
		response.InternalError(c, "获取用户信息失败")
		return
	}
	if !hasTOTP(u) {
		response.BadRequest(c, "请先设置 TOTP")
		return
	}

	if !auth.ValidateCode(getTOTPSecret(u), req.Code) {
		response.BadRequest(c, "验证码错误")
		return
	}

	response.Success(c, nil)
}

// TOTPDisable 禁用 TOTP
func (h *AuthHandler) TOTPDisable(c *gin.Context) {
	userID, _ := c.Get(middleware.CtxKeyUserID)

	var req dto.TOTPVerifyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误: "+err.Error())
		return
	}

	u, err := h.db.User.Get(c.Request.Context(), userID.(int))
	if err != nil {
		response.InternalError(c, "获取用户信息失败")
		return
	}
	if !hasTOTP(u) {
		response.BadRequest(c, "TOTP 未启用")
		return
	}

	// 验证当前 TOTP 码
	if !auth.ValidateCode(getTOTPSecret(u), req.Code) {
		response.BadRequest(c, "验证码错误")
		return
	}

	// 清除 TOTP 密钥
	_, err = h.db.User.UpdateOneID(userID.(int)).
		ClearTotpSecret().
		Save(c.Request.Context())
	if err != nil {
		slog.Error("禁用 TOTP 失败", "error", err)
		response.InternalError(c, "禁用 TOTP 失败")
		return
	}

	response.Success(c, nil)
}

// RefreshToken 刷新 JWT Token
func (h *AuthHandler) RefreshToken(c *gin.Context) {
	userID, _ := c.Get(middleware.CtxKeyUserID)
	role, _ := c.Get(middleware.CtxKeyRole)
	email, _ := c.Get(middleware.CtxKeyEmail)

	token, err := h.jwtMgr.GenerateToken(userID.(int), role.(string), email.(string))
	if err != nil {
		slog.Error("刷新 Token 失败", "error", err)
		response.InternalError(c, "刷新 Token 失败")
		return
	}

	response.Success(c, dto.RefreshResp{
		Token: token,
	})
}

// hasTOTP 检查用户是否启用了 TOTP
func hasTOTP(u *ent.User) bool {
	return u.TotpSecret != nil && *u.TotpSecret != ""
}

// getTOTPSecret 获取用户的 TOTP 密钥
func getTOTPSecret(u *ent.User) string {
	if u.TotpSecret == nil {
		return ""
	}
	return *u.TotpSecret
}

// userToResp 将 ent User 转换为 DTO 响应
func userToResp(u *ent.User) dto.UserResp {
	return dto.UserResp{
		ID:             int64(u.ID),
		Email:          u.Email,
		Username:       u.Username,
		Balance:        u.Balance,
		Role:           string(u.Role),
		MaxConcurrency: u.MaxConcurrency,
		TOTPEnabled:    hasTOTP(u),
		GroupRates:     u.GroupRates,
		Status:         string(u.Status),
		TimeMixin: dto.TimeMixin{
			CreatedAt: u.CreatedAt,
			UpdatedAt: u.UpdatedAt,
		},
	}
}
