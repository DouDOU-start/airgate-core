package handler

import (
	"context"
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
	"github.com/DouDOU-start/airgate-core/ent/group"
	entusagelog "github.com/DouDOU-start/airgate-core/ent/usagelog"
	"github.com/DouDOU-start/airgate-core/ent/user"
	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// APIKeyHandler API 密钥管理 Handler
type APIKeyHandler struct {
	db     *ent.Client
	secret string // JWT secret，用于 AES-GCM 加密 API 密钥
}

// NewAPIKeyHandler 创建 APIKeyHandler
func NewAPIKeyHandler(db *ent.Client, secret string) *APIKeyHandler {
	return &APIKeyHandler{db: db, secret: secret}
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
		response.BindError(c, err)
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

	// 批量查询 API Key 用量（今日 + 近30天）
	keyIDs := make([]int, len(keys))
	for i, k := range keys {
		keyIDs[i] = k.ID
	}
	todayCostMap, thirtyDayCostMap := h.batchKeyUsage(c.Request.Context(), keyIDs)

	list := make([]dto.APIKeyResp, 0, len(keys))
	for _, k := range keys {
		resp := toAPIKeyResp(k, "")
		resp.UserID = int64(uid)
		resp.TodayCost = todayCostMap[k.ID]
		resp.ThirtyDayCost = thirtyDayCostMap[k.ID]
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
		response.BindError(c, err)
		return
	}

	if ok, err := h.canUserUseGroup(c, uid, int(req.GroupID)); err != nil {
		slog.Error("校验用户分组权限失败", "error", err, "user_id", uid, "group_id", req.GroupID)
		response.InternalError(c, "创建失败")
		return
	} else if !ok {
		response.Forbidden(c, "无权使用该分组")
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

	// AES-GCM 加密存储（用于后续查看原文）
	encrypted, err := auth.EncryptAPIKey(rawKey, h.secret)
	if err != nil {
		slog.Error("加密 API 密钥失败", "error", err)
		response.InternalError(c, "创建失败")
		return
	}

	// 保存 key hint：sk-...后4位
	keyHint := "sk-..." + rawKey[len(rawKey)-4:]

	builder := h.db.APIKey.Create().
		SetName(req.Name).
		SetKeyHint(keyHint).
		SetKeyHash(keyHash).
		SetKeyEncrypted(encrypted).
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
		response.BindError(c, err)
		return
	}

	builder := h.db.APIKey.UpdateOneID(id)
	if req.Name != nil {
		builder = builder.SetName(*req.Name)
	}
	if req.GroupID != nil {
		ok, err := h.canUserUseGroup(c, uid, int(*req.GroupID))
		if err != nil {
			slog.Error("校验用户分组权限失败", "error", err, "user_id", uid, "group_id", *req.GroupID)
			response.InternalError(c, "更新失败")
			return
		}
		if !ok {
			response.Forbidden(c, "无权使用该分组")
			return
		}
		builder = builder.SetGroupID(int(*req.GroupID))
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

	tx, err := h.db.Tx(c.Request.Context())
	if err != nil {
		slog.Error("开启事务失败", "error", err)
		response.InternalError(c, "删除失败")
		return
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := tx.UsageLog.Update().
		Where(entusagelog.HasAPIKeyWith(apikey.IDEQ(id))).
		ClearAPIKey().
		Exec(c.Request.Context()); err != nil {
		slog.Error("清理密钥使用记录关联失败", "error", err)
		response.InternalError(c, "删除失败")
		return
	}
	if err := tx.APIKey.DeleteOneID(id).Exec(c.Request.Context()); err != nil {
		slog.Error("删除密钥失败", "error", err)
		response.InternalError(c, "删除失败")
		return
	}
	if err := tx.Commit(); err != nil {
		slog.Error("提交删除事务失败", "error", err)
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
		response.BindError(c, err)
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

func (h *APIKeyHandler) canUserUseGroup(c *gin.Context, uid, groupID int) (bool, error) {
	exists, err := h.db.Group.Query().
		Where(group.IDEQ(groupID)).
		Exist(c.Request.Context())
	if err != nil {
		return false, err
	}
	if !exists {
		response.NotFound(c, "分组不存在")
		return false, nil
	}

	allowed, err := h.db.Group.Query().
		Where(
			group.IDEQ(groupID),
			group.Or(
				group.IsExclusiveEQ(false),
				group.And(
					group.IsExclusiveEQ(true),
					group.HasAllowedUsersWith(user.IDEQ(uid)),
				),
			),
		).
		Exist(c.Request.Context())
	if err != nil {
		return false, err
	}
	return allowed, nil
}

// RevealKey 查看 API 密钥原文
func (h *APIKeyHandler) RevealKey(c *gin.Context) {
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

	// 查询密钥（验证归属）
	k, err := h.db.APIKey.Query().
		Where(apikey.IDEQ(id), apikey.HasUserWith(user.IDEQ(uid))).
		WithGroup().
		Only(c.Request.Context())
	if err != nil {
		if ent.IsNotFound(err) {
			response.NotFound(c, "密钥不存在")
			return
		}
		slog.Error("查询密钥失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}

	// 旧密钥没有加密字段
	if k.KeyEncrypted == "" {
		response.BadRequest(c, "该密钥创建于加密存储启用前，无法查看原文")
		return
	}

	plainKey, err := auth.DecryptAPIKey(k.KeyEncrypted, h.secret)
	if err != nil {
		slog.Error("解密 API 密钥失败", "error", err)
		response.BadRequest(c, "该密钥无法解密，可能创建于不同加密密钥下，无法查看原文")
		return
	}

	resp := toAPIKeyResp(k, plainKey)
	resp.UserID = int64(uid)
	response.Success(c, resp)
}

// batchKeyUsage 批量查询 API Key 的今日和近30天用量
func (h *APIKeyHandler) batchKeyUsage(ctx context.Context, keyIDs []int) (todayMap, thirtyDayMap map[int]float64) {
	todayMap = make(map[int]float64, len(keyIDs))
	thirtyDayMap = make(map[int]float64, len(keyIDs))
	if len(keyIDs) == 0 {
		return
	}

	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	thirtyDaysAgo := now.AddDate(0, 0, -30)

	type costRow struct {
		APIKeyID int     `json:"api_key_usage_logs"`
		Cost     float64 `json:"cost"`
	}

	// 今日用量
	var todayRows []costRow
	err := h.db.UsageLog.Query().
		Where(
			entusagelog.HasAPIKeyWith(apikey.IDIn(keyIDs...)),
			entusagelog.CreatedAtGTE(todayStart),
		).
		GroupBy(entusagelog.ForeignKeys[0]).
		Aggregate(ent.As(ent.Sum(entusagelog.FieldActualCost), "cost")).
		Scan(ctx, &todayRows)
	if err != nil {
		slog.Error("查询 API Key 今日用量失败", "error", err)
	}
	for _, r := range todayRows {
		todayMap[r.APIKeyID] = r.Cost
	}

	// 近30天用量
	var thirtyDayRows []costRow
	err = h.db.UsageLog.Query().
		Where(
			entusagelog.HasAPIKeyWith(apikey.IDIn(keyIDs...)),
			entusagelog.CreatedAtGTE(thirtyDaysAgo),
		).
		GroupBy(entusagelog.ForeignKeys[0]).
		Aggregate(ent.As(ent.Sum(entusagelog.FieldActualCost), "cost")).
		Scan(ctx, &thirtyDayRows)
	if err != nil {
		slog.Error("查询 API Key 近30天用量失败", "error", err)
	}
	for _, r := range thirtyDayRows {
		thirtyDayMap[r.APIKeyID] = r.Cost
	}

	return
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
		groupID := int64(k.Edges.Group.ID)
		resp.GroupID = &groupID
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
