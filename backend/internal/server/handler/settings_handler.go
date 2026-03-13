package handler

import (
	"log/slog"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/setting"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// SettingsHandler 系统设置 Handler
type SettingsHandler struct {
	db *ent.Client
}

// NewSettingsHandler 创建 SettingsHandler
func NewSettingsHandler(db *ent.Client) *SettingsHandler {
	return &SettingsHandler{db: db}
}

// GetSettings 获取所有设置
func (h *SettingsHandler) GetSettings(c *gin.Context) {
	query := h.db.Setting.Query().Order(setting.ByGroup(), setting.ByKey())

	// 分组筛选
	if group := c.Query("group"); group != "" {
		query = query.Where(setting.GroupEQ(group))
	}

	settings, err := query.All(c.Request.Context())
	if err != nil {
		slog.Error("查询设置失败", "error", err)
		response.InternalError(c, "查询失败")
		return
	}

	list := make([]dto.SettingResp, 0, len(settings))
	for _, s := range settings {
		list = append(list, dto.SettingResp{
			Key:   s.Key,
			Value: s.Value,
			Group: s.Group,
		})
	}

	response.Success(c, list)
}

// UpdateSettings 批量更新设置
func (h *SettingsHandler) UpdateSettings(c *gin.Context) {
	var req dto.UpdateSettingsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}

	ctx := c.Request.Context()

	for _, item := range req.Settings {
		// 查询是否已存在
		existing, err := h.db.Setting.Query().
			Where(setting.KeyEQ(item.Key)).
			Only(ctx)
		if err != nil {
			if ent.IsNotFound(err) {
				// 不存在则创建
				_, err = h.db.Setting.Create().
					SetKey(item.Key).
					SetValue(item.Value).
					Save(ctx)
			}
			if err != nil {
				slog.Error("更新设置失败", "key", item.Key, "error", err)
				response.InternalError(c, "更新设置失败: "+item.Key)
				return
			}
		} else {
			// 存在则更新
			_, err = existing.Update().
				SetValue(item.Value).
				Save(ctx)
			if err != nil {
				slog.Error("更新设置失败", "key", item.Key, "error", err)
				response.InternalError(c, "更新设置失败: "+item.Key)
				return
			}
		}
	}

	response.Success(c, nil)
}
