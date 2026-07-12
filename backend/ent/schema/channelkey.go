package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ChannelKey 渠道下的一把上游密钥端点：路由/调度/限流/故障隔离的最小单元。
//
// 每把 key 独立携带协议类型、上游密钥、模型清单与全部转发配置；
// 所属渠道（Channel）仅提供供应商级共享字段（base_url、上游余额）。
// 状态语义与旧 Channel 一致：
//
//	enabled          可调度
//	disabled_manual  手动禁用，永不被自动恢复
//	disabled_auto    自动禁用（上游 401/403），key 测试通过或转发成功可恢复
type ChannelKey struct {
	ent.Schema
}

func (ChannelKey) Fields() []ent.Field {
	return []ent.Field{
		// name key 的可选标签（如「claude」「gemini」），仅用于 UI 区分。
		field.String("name").Default(""),
		// 类型枚举与旧 Channel.type、adaptor、入口协议常量同值。
		field.Enum("type").Values("openai_compatible", "anthropic", "gemini", "custom", "openai_video", "suno"),
		// api_key 单把密钥的 AES-GCM 密文（base64），加解密由 service 层负责。
		field.String("api_key").NotEmpty().Sensitive(),
		field.JSON("models", []string{}).Default([]string{}),
		// model_mapping 对外模型名 → 上游模型名。
		field.JSON("model_mapping", map[string]string{}).Optional(),
		// param_override 请求体参数覆盖，形如 {"set": {...}, "remove": [...]}。
		field.JSON("param_override", map[string]interface{}{}).Optional(),
		field.JSON("header_override", map[string]string{}).Optional(),
		field.Enum("status").
			Values("enabled", "disabled_manual", "disabled_auto").
			Default("enabled"),
		field.String("error_msg").Default("").
			Comment("进入当前状态的原因（给运维看）"),
		field.Int("priority").Default(50).Min(0).Max(999),
		field.Int("weight").Default(10).Min(0),
		field.Int("max_concurrency").Default(0),
		field.Int("max_rpm").Default(0),
		field.Float("cost_ratio").Default(1.0).
			Comment("采购折扣率：官方 1.0、三折中转 0.3，用于渠道成本统计"),
		field.JSON("tags", []string{}).Optional(),
		field.String("test_model").Default(""),
		field.Int("response_time_ms").Default(0),
		field.Time("tested_at").Optional().Nillable(),
		field.Time("last_used_at").Optional().Nillable(),
		// balance 该把 key 的上游账户余额（美元），经 key 查 /dashboard/billing 拉取；
		// 仅 openai_compatible 中转站支持，官方直连恒 0（不支持查询）。
		field.Float("balance").Default(0).
			Comment("上游账户余额（USD）；仅 openai_compatible 中转站可查"),
		field.Time("balance_updated_at").Optional().Nillable().
			Comment("余额最近刷新时间；nil 表示从未刷新过"),
		field.Time("created_at").Default(timeNow).Immutable(),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

func (ChannelKey) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("type", "status"),
	}
}

func (ChannelKey) Edges() []ent.Edge {
	return []ent.Edge{
		// 所属渠道（多对一）：删渠道级联删 key。
		edge.From("channel", Channel.Type).
			Ref("keys").
			Unique().
			Required().
			Annotations(entsql.OnDelete(entsql.Cascade)),
		// key 绑定的分组集合（多对多）；空集合 = 公共 key，对所有分组可用。
		edge.To("groups", Group.Type),
		// key 硬删除时置空存量 usage_log 的 channel_key_id（OnDelete 声明在 assoc 边）。
		edge.To("usage_logs", UsageLog.Type).
			Annotations(entsql.OnDelete(entsql.SetNull)),
	}
}
