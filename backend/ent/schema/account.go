package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Account 上游订阅/OAuth 账号（Codex、Grok 等）。
//
// 与 ChannelKey（静态 API Key 透传）并列：账号走 provider 翻译+OAuth 转发，
// 渠道走零翻译透传。二者均可绑分组，按 priority+weight 统一调度。
//
// 状态机：
//
//	active        可调度
//	rate_limited  被上游限流，state_until 到期前不可调度，到期后自动恢复 active
//	degraded      软降级，state_until 到期前优先级最低仅兜底
//	disabled      凭证失效 / 运维禁用，需人工恢复或 reauth
//
// credentials_enc：AES-GCM 整包密文（JSON map），service 层加解密；
// 列表/详情 API 不回显完整 token；导出接口解密为明文便于再导入。
type Account struct {
	ent.Schema
}

func (Account) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").NotEmpty(),
		// platform：codex / xai 等内置 provider 标识。
		field.String("platform").NotEmpty(),
		// type：仅 oauth / api_key（历史 refresh_token 等在 service 层归一为 oauth）。
		field.String("type").Default("").Optional(),
		// credentials_enc AES-GCM(base64) 整包凭证 JSON；禁止明文落库。
		field.String("credentials_enc").Default("").Sensitive().
			Comment("AES-GCM 加密的凭证 JSON（map[string]string）"),
		// email 从凭证中抽出的展示/检索字段（非机密）。
		field.String("email").Default("").
			Comment("凭证中的邮箱（冗余展示/检索，非机密）"),

		field.Enum("state").
			Values("active", "rate_limited", "degraded", "disabled").
			Default("active"),
		field.Time("state_until").Optional().Nillable().
			Comment("state 的到期时间：rate_limited / degraded 到期自动恢复 active；disabled 无到期"),

		// priority / weight 与 ChannelKey 同语义，统一调度时混合加权。
		field.Int("priority").Default(50).Min(0).Max(999),
		field.Int("weight").Default(10).Min(0),
		field.Int("max_concurrency").Default(10),
		field.Float("rate_multiplier").Default(1.0).
			Comment("账号成本倍率：账号成本 = total × rate_multiplier"),
		field.String("error_msg").Default("").
			Comment("进入当前状态的原因（给运维看）"),
		// 历史字段：账号池已收敛到渠道配置，账号侧不再使用；保留列兼容旧库，默认 false。
		field.Bool("upstream_is_pool").Default(false).
			Comment("已废弃：账号池走渠道；账号侧恒为 false"),
		field.Time("last_used_at").Optional().Nillable(),
		field.JSON("extra", map[string]interface{}{}).Optional().Default(map[string]interface{}{}).
			Comment("扩展配置（max_rpm 等）"),
		field.Time("created_at").Default(timeNow).Immutable(),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

func (Account) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("platform", "state"),
		index.Fields("email"),
	}
}

func (Account) Edges() []ent.Edge {
	return []ent.Edge{
		// 绑定分组（多对多）：空集合 = 未分组，不参与任何分组调度。
		edge.To("groups", Group.Type),
		// 出站代理（可选 1:1）。
		edge.To("proxy", Proxy.Type).Unique(),
		// 使用记录：账号硬删除时置空 usage_log.account_id。
		edge.To("usage_logs", UsageLog.Type).
			Annotations(entsql.OnDelete(entsql.SetNull)),
	}
}
