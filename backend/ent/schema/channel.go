package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// Channel 上游渠道：供应商级容器，聚合同一上游（base_url）下的多把密钥端点。
//
// 协议类型、模型、转发配置与调度/故障隔离状态均下沉到子实体 ChannelKey；
// Channel 仅保留供应商级共享字段（base_url、上游余额）。
type Channel struct {
	ent.Schema
}

func (Channel) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").NotEmpty(),
		field.String("base_url").NotEmpty(),
		field.Time("created_at").Default(timeNow).Immutable(),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

func (Channel) Edges() []ent.Edge {
	return []ent.Edge{
		// 渠道下的密钥端点（一对多）：删渠道级联删 key（OnDelete 声明在 ChannelKey 侧的 assoc 边）。
		edge.To("keys", ChannelKey.Type),
		// 渠道硬删除时置空存量 usage_log 的渠道外键（显式声明，与 ent 对可空 FK 的
		// 默认行为一致）；删除后新插入的悬空引用由 billing recorder 的降级重插兜底。
		edge.To("usage_logs", UsageLog.Type).
			Annotations(entsql.OnDelete(entsql.SetNull)),
	}
}
