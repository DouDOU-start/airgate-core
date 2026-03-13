// Package handler 实现所有管理 API 的 HTTP Handler
package handler

import (
	"log/slog"
	"strconv"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/user"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// UserHandler 用户管理 Handler
type UserHandler struct {
	db *ent.Client
}

// NewUserHandler 创建 UserHandler
func NewUserHandler(db *ent.Client) *UserHandler {
	return &UserHandler{db: db}
}

// GetMe 获取当前登录用户信息
func (h *UserHandler) GetMe(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid, ok := userID.(int)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}

	u, err := h.db.User.Get(c.Request.Context(), uid)
	if err != nil {
		response.NotFound(c, "用户不存在")
		return
	}

	response.Success(c, userToResp(u))
}

// UpdateProfile 更新当前用户资料
func (h *UserHandler) UpdateProfile(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid, ok := userID.(int)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}

	var req dto.UpdateProfileReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}

	u, err := h.db.User.UpdateOneID(uid).
		SetUsername(req.Username).
		Save(c.Request.Context())
	if err != nil {
		slog.Error("更新用户资料失败", "error", err)
		response.InternalError(c, "更新失败")
		return
	}

	response.Success(c, userToResp(u))
}

// ChangePassword 修改当前用户密码
func (h *UserHandler) ChangePassword(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid, ok := userID.(int)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}

	var req dto.ChangePasswordReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}

	// 获取当前用户
	u, err := h.db.User.Get(c.Request.Context(), uid)
	if err != nil {
		response.NotFound(c, "用户不存在")
		return
	}

	// 验证旧密码
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.OldPassword)); err != nil {
		response.BadRequest(c, "旧密码错误")
		return
	}

	// 生成新密码 hash
	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		slog.Error("生成密码哈希失败", "error", err)
		response.InternalError(c, "密码修改失败")
		return
	}

	if err := h.db.User.UpdateOneID(uid).
		SetPasswordHash(string(hash)).
		Exec(c.Request.Context()); err != nil {
		slog.Error("修改密码失败", "error", err)
		response.InternalError(c, "密码修改失败")
		return
	}

	response.Success(c, nil)
}

// ListUsers 管理员查询用户列表（支持分页、搜索、状态/角色筛选）
func (h *UserHandler) ListUsers(c *gin.Context) {
	var page dto.PageReq
	if err := c.ShouldBindQuery(&page); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}

	query := h.db.User.Query()

	// 关键词搜索（邮箱或用户名）
	if page.Keyword != "" {
		query = query.Where(
			user.Or(
				user.EmailContains(page.Keyword),
				user.UsernameContains(page.Keyword),
			),
		)
	}

	// 状态筛选
	if status := c.Query("status"); status != "" {
		query = query.Where(user.StatusEQ(user.Status(status)))
	}

	// 角色筛选
	if role := c.Query("role"); role != "" {
		query = query.Where(user.RoleEQ(user.Role(role)))
	}

	// 总数
	total, err := query.Count(c.Request.Context())
	if err != nil {
		slog.Error("查询用户总数失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}

	// 分页查询
	users, err := query.
		Offset((page.Page - 1) * page.PageSize).
		Limit(page.PageSize).
		Order(ent.Desc(user.FieldCreatedAt)).
		All(c.Request.Context())
	if err != nil {
		slog.Error("查询用户列表失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}

	list := make([]dto.UserResp, 0, len(users))
	for _, u := range users {
		list = append(list, userToResp(u))
	}

	response.Success(c, response.PagedData(list, int64(total), page.Page, page.PageSize))
}

// CreateUser 管理员创建用户
func (h *UserHandler) CreateUser(c *gin.Context) {
	var req dto.CreateUserReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}

	// 检查邮箱是否已存在
	exists, err := h.db.User.Query().Where(user.EmailEQ(req.Email)).Exist(c.Request.Context())
	if err != nil {
		slog.Error("检查邮箱是否存在失败", "error", err)
		response.InternalError(c, "创建失败")
		return
	}
	if exists {
		response.BadRequest(c, "邮箱已被注册")
		return
	}

	// 生成密码 hash
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		slog.Error("生成密码哈希失败", "error", err)
		response.InternalError(c, "创建失败")
		return
	}

	builder := h.db.User.Create().
		SetEmail(req.Email).
		SetPasswordHash(string(hash)).
		SetUsername(req.Username).
		SetRole(user.Role(req.Role))

	if req.MaxConcurrency > 0 {
		builder = builder.SetMaxConcurrency(req.MaxConcurrency)
	}
	if req.GroupRates != nil {
		builder = builder.SetGroupRates(req.GroupRates)
	}

	u, err := builder.Save(c.Request.Context())
	if err != nil {
		slog.Error("创建用户失败", "error", err)
		response.InternalError(c, "创建失败")
		return
	}

	response.Success(c, userToResp(u))
}

// UpdateUser 管理员更新用户
func (h *UserHandler) UpdateUser(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的用户 ID")
		return
	}

	var req dto.UpdateUserReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}

	builder := h.db.User.UpdateOneID(id)

	if req.Username != nil {
		builder = builder.SetUsername(*req.Username)
	}
	if req.Role != nil {
		builder = builder.SetRole(user.Role(*req.Role))
	}
	if req.MaxConcurrency != nil {
		builder = builder.SetMaxConcurrency(*req.MaxConcurrency)
	}
	if req.GroupRates != nil {
		builder = builder.SetGroupRates(req.GroupRates)
	}
	if req.Status != nil {
		builder = builder.SetStatus(user.Status(*req.Status))
	}

	u, err := builder.Save(c.Request.Context())
	if err != nil {
		if ent.IsNotFound(err) {
			response.NotFound(c, "用户不存在")
			return
		}
		slog.Error("更新用户失败", "error", err)
		response.InternalError(c, "更新失败")
		return
	}

	response.Success(c, userToResp(u))
}

// AdjustBalance 管理员调整用户余额
func (h *UserHandler) AdjustBalance(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的用户 ID")
		return
	}

	var req dto.AdjustBalanceReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}

	builder := h.db.User.UpdateOneID(id)

	switch req.Action {
	case "set":
		builder = builder.SetBalance(req.Amount)
	case "add":
		builder = builder.AddBalance(req.Amount)
	case "subtract":
		builder = builder.AddBalance(-req.Amount)
	default:
		response.BadRequest(c, "无效的操作类型")
		return
	}

	u, err := builder.Save(c.Request.Context())
	if err != nil {
		if ent.IsNotFound(err) {
			response.NotFound(c, "用户不存在")
			return
		}
		slog.Error("调整余额失败", "error", err)
		response.InternalError(c, "调整余额失败")
		return
	}

	response.Success(c, userToResp(u))
}

// toUserResp 在 auth_handler.go 中定义为 userToResp，此处复用
