package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ChannelKey 渠道凭证对应的唯一协议端点：路由、模型、协议配置与故障隔离的最小单元。
//
// 真实 API Key、凭证限额、余额与成本配置由 ChannelCredential 保存；本实体继续
// 保留旧凭证列仅用于存量数据库和旧测试代码兼容，新业务不再读写其中的真实密钥。
// 状态语义与旧 Channel 一致：
//
//	enabled          可调度
//	disabled_manual  手动禁用，永不被自动恢复
//	disabled_auto    协议端点自动禁用（上游 403），端点测试通过或转发成功可恢复
//
// 上游 401 属物理凭证整体失效，由 ChannelCredential.status 承载。
type ChannelKey struct {
	ent.Schema
}

func (ChannelKey) Fields() []ent.Field {
	return []ent.Field{
		// credential_id 在自动迁移阶段保持可空，旧数据随后由敏感数据迁移回填。
		field.Int("credential_id").Optional().Nillable(),
		// name key 的可选标签（如「claude」「gemini」），仅用于 UI 区分。
		// 兼容列：新业务以 ChannelCredential.name 为准。
		field.String("name").Default(""),
		// 类型枚举与旧 Channel.type、adaptor、入口协议常量同值。
		field.Enum("type").Values("openai_compatible", "anthropic", "gemini", "openai_video", "suno"),
		// 兼容列：新端点固定留空，真实密文只存 ChannelCredential.api_key。
		field.String("api_key").Default("").Sensitive(),
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
		// balance_check_enabled 是否参与主动余额刷新（进页自动刷新/一键刷新）；
		// 官方直连等不支持余额接口的上游关掉，避免反复打无效请求刷 403 日志。
		// 关闭后手动单把查询仍可用。
		field.Bool("balance_check_enabled").Default(true).
			Comment("是否参与主动余额刷新；官方直连等无余额接口的上游可关闭"),

		// ---- 健康探针 ----
		field.Bool("probe_enabled").Default(false).
			Comment("是否启用主动健康探针"),
		field.String("probe_model").Default("").
			Comment("探针使用的模型；空串回退 test_model → 首个 model"),
		field.Enum("health_status").
			Values("healthy", "degraded", "suspended", "recovering").
			Default("healthy").
			Comment("健康状态机：healthy→degraded→suspended→recovering→healthy"),
		field.Int("consecutive_failures").Default(0).
			Comment("连续失败计数（用于降级/暂停判定）"),
		field.Int("consecutive_successes").Default(0).
			Comment("连续成功计数（用于恢复判定）"),
		field.Time("last_probe_at").Optional().Nillable().
			Comment("最近一次探针执行时间"),

		// ---- 上游倍率探测 ----
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

func (ChannelKey) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("type", "status"),
		index.Fields("credential_id", "type"),
	}
}

func (ChannelKey) Edges() []ent.Edge {
	return []ent.Edge{
		// 所属物理凭证。迁移期间允许旧端点暂时没有凭证；迁移完成后由唯一索引保证一对一。
		edge.From("credential", ChannelCredential.Type).
			Ref("keys").
			Field("credential_id").
			Unique().
			Annotations(entsql.OnDelete(entsql.Cascade)),
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
