package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// RedemptionCode 余额兑换码（管理员批量生成，用户兑换后 1:1 计入余额）。
// 状态机：unused → used（兑换入账，条件更新抢占）/ disabled（管理员停用，可恢复为 unused）。
// 过期不落状态列：expires_at < now 即视为不可兑换，列表/兑换时现算。
// used_by_id / used_by_email 为快照列（无 FK 边）：用户删除后保留兑换归属。
type RedemptionCode struct {
	ent.Schema
}

func (RedemptionCode) Fields() []ent.Field {
	return []ent.Field{
		// 兑换码明文（32 位随机 hex）。唯一约束是防重复入账的第一层。
		field.String("code").Unique().NotEmpty().MaxLen(64),
		field.Float("value").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Comment("面值（元），兑换后 1:1 计入余额"),
		field.String("status").Default("unused"),
		field.String("remark").Default("").
			Comment("批次备注（同一次生成的码共享同一备注，兼作批次标识）"),
		field.Int("used_by_id").Default(0).
			Comment("兑换人用户 ID 快照，0 表示未使用"),
		field.String("used_by_email").Default(""),
		field.Time("used_at").Optional().Nillable(),
		field.Time("expires_at").Optional().Nillable().
			Comment("过期时间，NULL 表示永不过期"),
		field.Time("created_at").Default(timeNow).Immutable(),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

func (RedemptionCode) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("status", "created_at").
			StorageKey("redemption_code_status_created_at"),
		index.Fields("used_by_id", "used_at").
			StorageKey("redemption_code_used_by_used_at"),
	}
}
