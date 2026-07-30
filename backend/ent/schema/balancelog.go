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
			Comment("幂等键。支付回调 / 兑换码等入账链路防重复；NULL 表示无幂等要求。"),
		field.String("transaction_id").Optional().Nillable().Unique().
			Comment("对外钱包交易号；仅 OAuth 钱包等需要对外引用的流水设置。"),
		field.String("source").Default("").
			Comment("余额变更来源，如 oauth_wallet；空值表示历史或通用流水。"),
		field.String("oauth_client_id").Default("").
			Comment("发起余额变更的 OAuth client_id；非外部应用流水为空。"),
		field.String("external_order_no").Default("").
			Comment("外部应用订单号或退款单号，用于业务审计与幂等校验。"),
		field.Int("related_log_id").Default(0).
			Comment("退款所关联的原扣款 BalanceLog ID；0 表示无关联。"),
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
		index.Fields("oauth_client_id", "external_order_no"),
		index.Fields("related_log_id"),
		index.Fields("user_id_snapshot", "created_at"),
	}
}
