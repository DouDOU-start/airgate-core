package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// Group 分组
type Group struct {
	ent.Schema
}

func (Group) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").NotEmpty(),
		// platform 分组的平台标识（如 anthropic / openai）。调度不消费此字段；
		// 仍被 API Key 平台识别链路使用（dto/user.go APIKeyPlatform → 前端 CCS 导入识别），保留。
		field.String("platform").Default(""),
		field.Float("rate_multiplier").Default(1.0),
		// alpha_search_price 分组对 codex /v1/alpha/search 联网搜索的按次覆盖价（USD/次）。
		// NULL=沿用全局 gateway 设置 alpha_search_price；0=该分组免费；>0=覆盖全局。
		// 实际扣费仍叠加分组 rate_multiplier（billing_rate 链）。
		field.Float("alpha_search_price").Optional().Nillable(),
		field.Bool("is_exclusive").Default(false),
		// status_visible 控制此分组是否在登录用户的「渠道状态」页展示。
		// 默认 true 保持旧行为兼容；管理员可在「分组管理」中关掉以对外隐藏
		// （比如仅限熟客的专属分组、调试中的分组等）。
		// 隐藏仅影响用户渠道状态页 (/channel-status)，不影响 admin 视图和 API 鉴权逻辑。
		field.Bool("status_visible").Default(true),
		// allowed_clients 客户端白名单：非空时仅允许指定类型的客户端访问此分组。
		// 空=不限制。值域: "claude_code", "codex"。
		field.JSON("allowed_clients", []string{}).Optional(),
		// fallback_group_id 客户端不匹配 allowed_clients 时降级到的分组；nil=直接拒绝。
		field.Int("fallback_group_id").Optional().Nillable(),
		field.String("note").Default(""),
		field.Int("sort_weight").Default(0),
		field.Time("created_at").Default(timeNow).Immutable(),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

func (Group) Edges() []ent.Edge {
	return []ent.Edge{
		// 分组关联的渠道密钥端点（多对多反向）
		edge.From("channel_keys", ChannelKey.Type).Ref("groups"),
		// 分组关联的订阅账号（多对多反向）；与 channel_keys 并列参与统一调度。
		edge.From("accounts", Account.Type).Ref("groups"),
		// 允许访问此专属分组的用户（多对多反向）
		edge.From("allowed_users", User.Type).Ref("allowed_groups"),
		edge.To("api_keys", APIKey.Type),
		edge.To("usage_logs", UsageLog.Type),
	}
}
