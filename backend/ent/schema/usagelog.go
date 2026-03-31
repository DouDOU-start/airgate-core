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
		field.Int("reasoning_output_tokens").Default(0),
		field.Float("input_cost").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("output_cost").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("cached_input_cost").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("total_cost").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("actual_cost").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("rate_multiplier").Default(1.0),
		field.Float("account_rate_multiplier").Default(1.0),
		field.String("service_tier").Default(""),
		field.Bool("stream").Default(false),
		field.Int64("duration_ms").Default(0),
		field.Int64("first_token_ms").Default(0),
		field.String("user_agent").Default(""),
		field.String("ip_address").Default(""),
		field.Time("created_at").Default(timeNow).Immutable(),
	}
}

func (UsageLog) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("usage_logs").Unique().Required(),
		edge.From("api_key", APIKey.Type).Ref("usage_logs").Unique(),
		edge.From("account", Account.Type).Ref("usage_logs").Unique().Required(),
		edge.From("group", Group.Type).Ref("usage_logs").Unique(),
	}
}
