package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ChannelCredential 上游单协议物理凭证。真实 API Key、状态、限额、余额和
// 成本配置只保存一份，并与唯一的 ChannelKey 协议端点一一对应。
type ChannelCredential struct {
	ent.Schema
}

func (ChannelCredential) Fields() []ent.Field {
	return []ent.Field{
		field.Int("channel_id").Positive(),
		field.String("name").Default(""),
		field.String("api_key").NotEmpty().Sensitive(),
		field.Enum("status").
			Values("enabled", "disabled_manual", "disabled_auto").
			Default("enabled"),
		field.String("error_msg").Default("").
			Comment("凭证整体不可用的原因（给运维看）"),
		field.Int("max_concurrency").Default(0),
		field.Int("max_rpm").Default(0),
		field.Float("cost_ratio").Default(1.0).
			Comment("采购折扣率：官方 1.0、三折中转 0.3，用于渠道成本统计"),
		field.JSON("tags", []string{}).Optional(),
		field.Float("balance").Default(0).
			Comment("上游账户余额（USD）"),
		field.Time("balance_updated_at").Optional().Nillable().
			Comment("余额最近刷新时间；nil 表示从未刷新过"),
		field.Bool("balance_check_enabled").Default(true).
			Comment("是否参与主动余额刷新"),
		field.Bool("upstream_rate_enabled").Default(false).
			Comment("是否启用上游倍率探测"),
		field.String("upstream_rate_path").Default("").
			Comment("上游倍率端点路径；空串默认 /v1/airgate/billing"),
		field.Bool("use_upstream_rate_for_cost").Default(false).
			Comment("是否使用探测倍率覆盖手动成本倍率"),
		field.Float("upstream_rate").Default(0).
			Comment("最近一次探测到的上游计费倍率"),
		field.Time("upstream_rate_at").Optional().Nillable().
			Comment("上游倍率最近探测时间"),
		field.Time("created_at").Default(timeNow).Immutable(),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

func (ChannelCredential) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("channel_id", "status"),
	}
}

func (ChannelCredential) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("channel", Channel.Type).
			Ref("credentials").
			Field("channel_id").
			Unique().
			Required().
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("keys", ChannelKey.Type),
	}
}
