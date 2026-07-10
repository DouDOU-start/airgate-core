package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// APIKey API 密钥
type APIKey struct {
	ent.Schema
}

func (APIKey) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").NotEmpty(),
		field.String("key_hint").Default(""),
		field.String("key_hash").NotEmpty().Sensitive(),
		field.String("key_encrypted").Optional().Sensitive(),
		field.JSON("ip_whitelist", []string{}).Optional(),
		field.JSON("ip_blacklist", []string{}).Optional(),
		field.Float("quota_usd").Default(0),
		field.Float("used_quota").Default(0).
			Comment("账面已用：累加 billed_cost（含 sell_rate markup）。end customer 看到的就是这个数字。"),
		field.Float("used_quota_actual").Default(0).
			Comment("真实成本已用：累加 actual_cost。reseller 用于成本核算/利润计算，end customer 不可见。"),
		field.Float("sell_rate").Default(0).Min(0).
			Comment("销售倍率：>0 时启用 reseller markup, billed_cost = base_cost × sell_rate；=0 表示不加价，billed_cost = actual_cost"),
		field.Int("max_concurrency").Default(0).Min(0).
			Comment("API Key 级并发上限：同一把 key 同时在途的请求数。0 表示不限制（默认）。达到上限时返回 429 + apikey_concurrency_limit，保护单个客户端不因并发过高被自己打死或耗光上游账号的并发预算。"),
		field.Time("expires_at").Optional().Nillable(),
		field.Enum("status").Values("active", "disabled").Default("active"),
		field.String("provisioned_by").Default("").
			Comment("经 OAuth provision-key 自动创建时记录来源应用的 client_id；空 = 用户手动创建。同一用户同一应用同一分组只保留一把 provisioned key（get-or-create 幂等依据），应用可按分组为用户领多把 key。"),
		field.Time("created_at").Default(timeNow).Immutable(),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

func (APIKey) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("key_hash").Unique(),
		index.Fields("status", "created_at"),
		// 同一用户同一应用同一分组只允许一把 provisioned key（部分唯一索引，
		// 空串 = 手动创建不受约束）；并发 provision 时第二个事务撞唯一约束后重查。
		index.Fields("provisioned_by").Edges("user", "group").Unique().
			Annotations(entsql.IndexWhere("provisioned_by <> ''")),
	}
}

func (APIKey) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("api_keys").Unique().Required(),
		edge.From("group", Group.Type).Ref("api_keys").Unique(),
		edge.To("usage_logs", UsageLog.Type),
	}
}
