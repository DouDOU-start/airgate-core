package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// InviteProfile 邀请返利用户画像（每用户一条，get-or-create）。
// user_id / inviter_id 均为快照字段（无 FK 边）：用户硬删除后保留邀请归属，
// 与 redemption_code.used_by_id 同一范式。
type InviteProfile struct {
	ent.Schema
}

func (InviteProfile) Fields() []ent.Field {
	return []ent.Field{
		field.Int("user_id").Unique().
			Comment("邀请画像归属用户 ID"),
		field.String("invite_code").Unique().NotEmpty().MaxLen(32).
			Comment("本人邀请码，注册时随机生成"),
		field.Int("inviter_id").Optional().Nillable().
			Comment("绑定的邀请人用户 ID，一旦绑定不可变更；NULL 表示未绑定"),
		field.Float("rebate_rate_override").Optional().Nillable().
			SchemaType(map[string]string{dialect.Postgres: "decimal(5,2)"}).
			Comment("专属返利比例（百分比 0-100），NULL 表示沿用全局比例"),
		field.Int("invited_count").Default(0).
			Comment("成功绑定的邀请人数"),
		field.Float("rebate_balance").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Default(0).
			Comment("待转入余额的返利额度"),
		field.Float("rebate_total").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Default(0).
			Comment("历史累计返利（含已转入余额部分）"),
		field.Time("created_at").Default(timeNow).Immutable(),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

func (InviteProfile) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("inviter_id"),
	}
}
