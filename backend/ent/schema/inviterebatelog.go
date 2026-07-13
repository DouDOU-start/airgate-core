package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// InviteRebateLog 邀请返利流水（计提 accrue / 转入余额 transfer）。
// 快照字段 + 幂等键范式照抄 balancelog.go：user_id 为流水归属人（邀请人），
// 无 FK 边，用户硬删除后保留流水归属。
type InviteRebateLog struct {
	ent.Schema
}

func (InviteRebateLog) Fields() []ent.Field {
	return []ent.Field{
		field.Int("user_id").
			Comment("流水归属人（邀请人）用户 ID"),
		field.Enum("action").Values("accrue", "transfer"),
		field.Float("amount").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Int("source_user_id").Optional().Nillable().
			Comment("accrue：触发本次计提的下线（被邀请人）用户 ID"),
		field.String("source_order_no").Optional().Nillable().
			Comment("accrue：触发本次计提的充值订单号"),
		field.Float("balance_after").Optional().Nillable().
			Comment("transfer：转入余额后的用户余额快照，仅审计用"),
		field.String("idempotency_key").Optional().Nillable().
			Comment("幂等键：accrue 为 invite-accrue:<订单号>，transfer 为 invite-transfer:<流水ID>；NULL 表示无幂等要求"),
		field.Time("created_at").Default(timeNow).Immutable(),
	}
}

func (InviteRebateLog) Indexes() []ent.Index {
	return []ent.Index{
		// Postgres 唯一索引对 NULL 不互斥，仅约束显式提供的幂等键
		index.Fields("idempotency_key").Unique(),
		index.Fields("user_id", "created_at"),
	}
}
