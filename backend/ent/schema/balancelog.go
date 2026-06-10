package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// BalanceLog 余额变更日志
type BalanceLog struct {
	ent.Schema
}

func (BalanceLog) Fields() []ent.Field {
	return []ent.Field{
		field.Enum("action").Values("add", "subtract", "set"),
		field.Float("amount").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("before_balance").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("after_balance").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.String("remark").Default(""),
		field.Int("user_id_snapshot").Default(0).
			Comment("用户 ID 快照。用户硬删除后保留余额流水归属。"),
		field.String("user_email_snapshot").Default("").
			Comment("用户邮箱快照。用户硬删除后保留余额流水归属。"),
		field.String("idempotency_key").Optional().Nillable().
			Comment("幂等键。插件经 user.update_balance 入账时防重复；NULL 表示无幂等要求。"),
		field.Time("created_at").Default(timeNow).Immutable(),
	}
}

func (BalanceLog) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("balance_logs").Unique(),
	}
}

func (BalanceLog) Indexes() []ent.Index {
	return []ent.Index{
		// Postgres 唯一索引对 NULL 不互斥，仅约束显式提供的幂等键
		index.Fields("idempotency_key").Unique(),
	}
}
