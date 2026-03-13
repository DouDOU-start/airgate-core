package handler

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/apikey"
	"github.com/DouDOU-start/airgate-core/ent/user"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// APIKeyHandler API 密钥管理 Handler
type APIKeyHandler struct {
	db *ent.Client
}

// NewAPIKeyHandler 创建 APIKeyHandler
func NewAPIKeyHandler(db *ent.Client) *APIKeyHandler {
	return &APIKeyHandler{db: db}
}

// ListKeys 查询当前用户的 API 密钥列表
func (h *APIKeyHandler) ListKeys(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid, ok := userID.(int)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}

	var page dto.PageReq
	if err := c.ShouldBindQuery(&page); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}

	query := h.db.APIKey.Query().
		Where(apikey.HasUserWith(user.IDEQ(uid))).
		WithGroup()

	// 关键词搜索
	if page.Keyword != "" {
		query = query.Where(apikey.NameContains(page.Keyword))
	}

	total, err := query.Count(c.Request.Context())
	if err != nil {
		slog.Error("查询密钥总数失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}

	keys, err := query.
		Offset((page.Page - 1) * page.PageSize).
		Limit(page.PageSize).
		Order(ent.Desc(apikey.FieldCreatedAt)).
		All(c.Request.Context())
	if err != nil {
		slog.Error("查询密钥列表失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}

	list := make([]dto.APIKeyResp, 0, len(keys))
	for _, k := range keys {
		resp := toAPIKeyResp(k, "")
		resp.UserID = int64(uid)
		list = append(list, resp)
	}

	response.Success(c, response.PagedData(list, int64(total), page.Page, page.PageSize))
}

// CreateKey 创建 API 密钥
func (h *APIKeyHandler) CreateKey(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid, ok := userID.(int)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}

	var req dto.CreateAPIKeyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}

	// 生成 sk- 前缀的随机密钥
	rawKey, err := generateAPIKey()
	if err != nil {
		slog.Error("生成 API 密钥失败", "error", err)
		response.InternalError(c, "创建失败")
		return
	}

	// 计算 SHA256 哈希存储
	keyHash := hashAPIKey(rawKey)

	builder := h.db.APIKey.Create().
		SetName(req.Name).
		SetKeyHash(keyHash).
		SetUserID(uid).
		SetGroupID(int(req.GroupID)).
		SetQuotaUsd(req.QuotaUSD)

	if req.IPWhitelist != nil {
		builder = builder.SetIPWhitelist(req.IPWhitelist)
	}
	if req.IPBlacklist != nil {
		builder = builder.SetIPBlacklist(req.IPBlacklist)
	}
	if req.ExpiresAt != nil {
		t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			response.BadRequest(c, "过期时间格式错误，请使用 RFC3339 格式")
			return
		}
		builder = builder.SetExpiresAt(t)
	}

	k, err := builder.Save(c.Request.Context())
	if err != nil {
		slog.Error("创建 API 密钥失败", "error", err)
		response.InternalError(c, "创建失败")
		return
	}

	// 重新加载关联数据
	k, err = h.db.APIKey.Query().
		Where(apikey.IDEQ(k.ID)).
		WithUser().
		WithGroup().
		Only(c.Request.Context())
	if err != nil {
		slog.Error("加载密钥关联数据失败", "error", err)
		response.InternalError(c, "创建成功但加载关联数据失败")
		return
	}

	// 仅创建时返回完整密钥
	response.Success(c, toAPIKeyResp(k, rawKey))
}

// UpdateKey 更新 API 密钥（普通用户）
func (h *APIKeyHandler) UpdateKey(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid, ok := userID.(int)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}

	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的密钥 ID")
		return
	}

	// 检查密钥是否属于当前用户
	exists, err := h.db.APIKey.Query().
		Where(apikey.IDEQ(id), apikey.HasUserWith(user.IDEQ(uid))).
		Exist(c.Request.Context())
	if err != nil {
		slog.Error("检查密钥归属失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}
	if !exists {
		response.NotFound(c, "密钥不存在")
		return
	}

	var req dto.UpdateAPIKeyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}

	builder := h.db.APIKey.UpdateOneID(id)
	if req.Name != nil {
		builder = builder.SetName(*req.Name)
	}
	if req.IPWhitelist != nil {
		builder = builder.SetIPWhitelist(req.IPWhitelist)
	}
	if req.IPBlacklist != nil {
		builder = builder.SetIPBlacklist(req.IPBlacklist)
	}
	if req.QuotaUSD != nil {
		builder = builder.SetQuotaUsd(*req.QuotaUSD)
	}
	if req.ExpiresAt != nil {
		t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			response.BadRequest(c, "过期时间格式错误")
			return
		}
		builder = builder.SetExpiresAt(t)
	}
	if req.Status != nil {
		builder = builder.SetStatus(apikey.Status(*req.Status))
	}

	updated, err := builder.Save(c.Request.Context())
	if err != nil {
		slog.Error("更新密钥失败", "error", err)
		response.InternalError(c, "更新失败")
		return
	}

	// 重新加载关联数据
	updated, err = h.db.APIKey.Query().
		Where(apikey.IDEQ(updated.ID)).
		WithUser().
		WithGroup().
		Only(c.Request.Context())
	if err != nil {
		slog.Error("加载密钥关联数据失败", "error", err)
		response.InternalError(c, "更新成功但加载关联数据失败")
		return
	}

	response.Success(c, toAPIKeyResp(updated, ""))
}

// DeleteKey 删除 API 密钥
func (h *APIKeyHandler) DeleteKey(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid, ok := userID.(int)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}

	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的密钥 ID")
		return
	}

	// 检查密钥是否属于当前用户
	exists, err := h.db.APIKey.Query().
		Where(apikey.IDEQ(id), apikey.HasUserWith(user.IDEQ(uid))).
		Exist(c.Request.Context())
	if err != nil {
		slog.Error("检查密钥归属失败", "error", err)
		response.InternalError(c, "删除失败")
		return
	}
	if !exists {
		response.NotFound(c, "密钥不存在")
		return
	}

	if err := h.db.APIKey.DeleteOneID(id).Exec(c.Request.Context()); err != nil {
		slog.Error("删除密钥失败", "error", err)
		response.InternalError(c, "删除失败")
		return
	}

	response.Success(c, nil)
}

// AdminUpdateKey 管理员更新密钥（可修改 group_id）
func (h *APIKeyHandler) AdminUpdateKey(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "无效的密钥 ID")
		return
	}

	var req dto.AdminUpdateAPIKeyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}

	builder := h.db.APIKey.UpdateOneID(id)
	if req.Name != nil {
		builder = builder.SetName(*req.Name)
	}
	if req.IPWhitelist != nil {
		builder = builder.SetIPWhitelist(req.IPWhitelist)
	}
	if req.IPBlacklist != nil {
		builder = builder.SetIPBlacklist(req.IPBlacklist)
	}
	if req.QuotaUSD != nil {
		builder = builder.SetQuotaUsd(*req.QuotaUSD)
	}
	if req.ExpiresAt != nil {
		t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			response.BadRequest(c, "过期时间格式错误")
			return
		}
		builder = builder.SetExpiresAt(t)
	}
	if req.Status != nil {
		builder = builder.SetStatus(apikey.Status(*req.Status))
	}
	if req.GroupID != nil {
		builder = builder.SetGroupID(int(*req.GroupID))
	}

	updated, err := builder.Save(c.Request.Context())
	if err != nil {
		if ent.IsNotFound(err) {
			response.NotFound(c, "密钥不存在")
			return
		}
		slog.Error("管理员更新密钥失败", "error", err)
		response.InternalError(c, "更新失败")
		return
	}

	// 重新加载关联数据
	updated, err = h.db.APIKey.Query().
		Where(apikey.IDEQ(updated.ID)).
		WithUser().
		WithGroup().
		Only(c.Request.Context())
	if err != nil {
		slog.Error("加载密钥关联数据失败", "error", err)
		response.InternalError(c, "更新成功但加载关联数据失败")
		return
	}

	response.Success(c, toAPIKeyResp(updated, ""))
}

// generateAPIKey 生成 sk- 前缀的随机 API 密钥
func generateAPIKey() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return fmt.Sprintf("sk-%s", hex.EncodeToString(bytes)), nil
}

// hashAPIKey 计算 API 密钥的 SHA256 哈希
func hashAPIKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

// toAPIKeyResp 将 ent.APIKey 转换为 dto.APIKeyResp
func toAPIKeyResp(k *ent.APIKey, rawKey string) dto.APIKeyResp {
	resp := dto.APIKeyResp{
		ID:          int64(k.ID),
		Name:        k.Name,
		KeyPrefix:   truncateKey(k.KeyHash),
		IPWhitelist: k.IPWhitelist,
		IPBlacklist: k.IPBlacklist,
		QuotaUSD:    k.QuotaUsd,
		UsedQuota:   k.UsedQuota,
		Status:      string(k.Status),
		TimeMixin: dto.TimeMixin{
			CreatedAt: k.CreatedAt,
			UpdatedAt: k.UpdatedAt,
		},
	}

	// 仅创建时返回完整密钥
	if rawKey != "" {
		resp.Key = rawKey
		if len(rawKey) > 10 {
			resp.KeyPrefix = rawKey[:10] + "..."
		} else {
			resp.KeyPrefix = rawKey
		}
	}

	if k.ExpiresAt != nil {
		t := k.ExpiresAt.Format(time.RFC3339)
		resp.ExpiresAt = &t
	}

	// 关联的用户 ID
	if k.Edges.User != nil {
		resp.UserID = int64(k.Edges.User.ID)
	}

	// 关联的分组 ID
	if k.Edges.Group != nil {
		resp.GroupID = int64(k.Edges.Group.ID)
	}

	return resp
}

// truncateKey 截断哈希用于前缀展示
func truncateKey(hash string) string {
	if len(hash) > 8 {
		return "sk-" + hash[:8] + "..."
	}
	return hash
}
