package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
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
		field.Int("max_concurrency").Default(0).Min(0).
			Comment("用户级并发上限：同一 user 所有 API Key 加起来同时在途的请求数。0 表示不限制（默认）。与 api_key.max_concurrency 是 AND 关系，两者都会检查。"),
		field.JSON("group_rates", map[int64]float64{}).Optional(),
		field.Float("balance_alert_threshold").Default(0), // 0 表示关闭预警
		field.Bool("balance_alert_notified").Default(false),
		field.Enum("status").Values("active", "disabled").Default("active"),
		field.Time("created_at").Default(timeNow).Immutable(),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

func (User) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("api_keys", APIKey.Type),
		// 用户硬删除时置空历史记录外键，归属信息由 *_snapshot 快照列保留
		//（业务层删除事务已显式做快照与清边，此处为 DB 级兜底；见 schema/channel.go 注解范式）。
		edge.To("usage_logs", UsageLog.Type).
			Annotations(entsql.OnDelete(entsql.SetNull)),
		// 用户可访问的专属分组（多对多）
		edge.To("allowed_groups", Group.Type),
		edge.To("balance_logs", BalanceLog.Type).
			Annotations(entsql.OnDelete(entsql.SetNull)),
	}
}
