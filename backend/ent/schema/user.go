package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// User 用户表
type User struct {
	ent.Schema
}

func (User) Fields() []ent.Field {
	return []ent.Field{
		field.String("email").Unique().NotEmpty(),
		field.String("password_hash").NotEmpty().Sensitive(),
		field.String("username").Default(""),
		field.Float("balance").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Enum("role").Values("admin", "user").Default("user"),
		field.Int("max_concurrency").Default(5),
		field.String("totp_secret").Optional().Nillable().Sensitive(),
		field.JSON("group_rates", map[int64]float64{}).Optional(),
		field.Enum("status").Values("active", "disabled").Default("active"),
		field.Time("created_at").Default(timeNow).Immutable(),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

func (User) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("api_keys", APIKey.Type),
		edge.To("subscriptions", UserSubscription.Type),
		edge.To("usage_logs", UsageLog.Type),
		// 用户可访问的专属分组（多对多）
		edge.To("allowed_groups", Group.Type),
		edge.To("balance_logs", BalanceLog.Type),
	}
}
