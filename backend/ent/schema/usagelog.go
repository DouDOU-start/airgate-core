package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// UsageLog 使用日志（只追加）
type UsageLog struct {
	ent.Schema
}

func (UsageLog) Fields() []ent.Field {
	return []ent.Field{
		field.String("platform").NotEmpty(),
		field.String("model").NotEmpty(),
		field.Int("input_tokens").Default(0),
		field.Int("output_tokens").Default(0),
		field.Int("cached_input_tokens").Default(0),
		field.Int("cache_creation_tokens").Default(0),
		field.Int("cache_creation_5m_tokens").Default(0),
		field.Int("cache_creation_1h_tokens").Default(0),
		field.Int("reasoning_output_tokens").Default(0),
		field.Float("input_price").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("output_price").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("cached_input_price").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("cache_creation_price").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("cache_creation_1h_price").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("input_cost").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("output_cost").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("cached_input_cost").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("cache_creation_cost").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("total_cost").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("actual_cost").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Comment("平台对 reseller 的真实扣费 = total × billing_rate（group/user）"),
		field.Float("billed_cost").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Comment("账面消耗：reseller 对最终客户的计费金额。sell_rate=0 时等于 actual_cost。永远不参与平台账户/统计。"),
		field.Float("account_cost").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Comment("账号实际成本 = total × account_rate。用于账号管理后台的'账号计费'统计；与用户计费完全独立。"),
		field.Float("rate_multiplier").Default(1.0).
			Comment("快照：本次请求生效的平台计费倍率（ResolveBillingRate 结果）"),
		field.Float("sell_rate").Default(0).
			Comment("快照：本次请求生效的 sell_rate；0 表示该 key 当时未启用 markup"),
		field.Float("account_rate_multiplier").Default(1.0).
			Comment("快照：本次请求生效的 account_rate"),
		field.String("service_tier").Default(""),
		// image_size 图像生成请求实际出图尺寸（"WxH"）。非图像请求留空。
		// admin 后台展示用，让用户能直观看出"为什么这次图扣了 0.40"——按 1K/2K/4K 分档计费。
		field.String("image_size").Default(""),
		field.Bool("stream").Default(false),
		field.Int64("duration_ms").Default(0),
		field.Int64("first_token_ms").Default(0),
		field.String("user_agent").Default(""),
		field.String("ip_address").Default(""),
		// 请求端点。
		field.String("endpoint").Default(""),
		// 推理强度档位。
		field.String("reasoning_effort").Default(""),
		field.Time("created_at").Default(timeNow).Immutable(),
	}
}

func (UsageLog) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("usage_logs").Unique().Required(),
		edge.From("api_key", APIKey.Type).Ref("usage_logs").Unique(),
		edge.From("account", Account.Type).Ref("usage_logs").Unique(),
		edge.From("group", Group.Type).Ref("usage_logs").Unique(),
	}
}
