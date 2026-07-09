package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Channel 上游渠道：一个可转发的上游端点（base_url + 多 api_key + 模型列表）。
//
// 状态语义：
//
//	enabled          可调度
//	disabled_manual  手动禁用，永不被自动恢复
//	disabled_auto    自动禁用（401/403/禁用关键词等），渠道测试通过或 status_until 到期可恢复
type Channel struct {
	ent.Schema
}

func (Channel) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").NotEmpty(),
		field.Enum("type").Values("openai_compatible", "anthropic", "gemini"),
		field.String("base_url").NotEmpty(),
		// api_keys 存元素级 AES-GCM 密文（base64），加解密由 service 层负责，schema 不管加密。
		field.JSON("api_keys", []string{}).Default([]string{}).Sensitive(),
		field.JSON("models", []string{}).Default([]string{}),
		// model_mapping 对外模型名 → 上游模型名。
		field.JSON("model_mapping", map[string]string{}).Optional(),
		// param_override 请求体参数覆盖，形如 {"set": {...}, "remove": [...]}。
		field.JSON("param_override", map[string]interface{}{}).Optional(),
		field.JSON("header_override", map[string]string{}).Optional(),
		field.Enum("status").
			Values("enabled", "disabled_manual", "disabled_auto").
			Default("enabled"),
		field.Time("status_until").Optional().Nillable().
			Comment("429 冷却到期时间：到期后自动恢复可用；手动禁用不设此值"),
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
		// balance 上游账户余额（美元），经 key 查询 /dashboard/billing 拉取；
		// 仅 openai_compatible 中转站支持，官方直连渠道恒 0（不支持查询）。
		field.Float("balance").Default(0).
			Comment("上游账户余额（USD）；多 key 求和；仅 openai_compatible 中转站可查"),
		field.Time("balance_updated_at").Optional().Nillable().
			Comment("余额最近刷新时间；nil 表示从未刷新过"),
		field.Time("last_used_at").Optional().Nillable(),
		field.Time("created_at").Default(timeNow).Immutable(),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

func (Channel) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("type", "status"),
	}
}

func (Channel) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("groups", Group.Type),
		// 渠道硬删除时置空存量 usage_log 的渠道外键（显式声明，与 ent 对可空 FK 的
		// 默认行为一致）；删除后新插入的悬空引用由 billing recorder 的降级重插兜底。
		// 注：FK 的 OnDelete 取自 assoc 边（edge.To）注解——声明在 UsageLog 侧的
		// edge.From 上不会生效（entc 生成 FK 时跳过 inverse 边）。
		edge.To("usage_logs", UsageLog.Type).
			Annotations(entsql.OnDelete(entsql.SetNull)),
	}
}
