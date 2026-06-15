package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Account 上游 AI 账户。
//
// 状态机（详见 scheduler/state.go）：
//
//	active        可调度
//	rate_limited  被上游限流，state_until 到期前 NotSchedulable，到期后自动恢复 active
//	degraded      软降级（池账号临时抖动），state_until 到期前优先级降到最低仅兜底
//	disabled      凭证失效 / 连续失败超阈值，需要人工重新验证
type Account struct {
	ent.Schema
}

func (Account) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").NotEmpty(),
		field.String("platform").NotEmpty(),
		field.String("type").Default("").Optional(),
		field.JSON("credentials", map[string]string{}).Default(map[string]string{}),

		// state / state_until 是账号状态的单一真相源。Redis 只做缓存加速。
		field.Enum("state").
			Values("active", "rate_limited", "degraded", "disabled").
			Default("active"),
		field.Time("state_until").Optional().Nillable().
			Comment("state 的到期时间：rate_limited / degraded 到期自动恢复 active；disabled 无到期"),

		field.Int("priority").Default(50).Min(0).Max(999),
		field.Int("max_concurrency").Default(10),
		field.Float("rate_multiplier").Default(1.0),
		field.String("error_msg").Default("").
			Comment("进入当前状态的原因（给运维看）"),
		field.Bool("upstream_is_pool").Default(false).
			Comment("上游是账号池：UpstreamTransient 走软降级 degraded；AccountDead 仍标 disabled"),
		field.Time("last_used_at").Optional().Nillable(),
		field.JSON("extra", map[string]interface{}{}).Optional().Default(map[string]interface{}{}).
			Comment("扩展配置（max_rpm / max_window_cost / max_sessions 等）"),
		field.Time("created_at").Default(timeNow).Immutable(),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

func (Account) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("platform", "state"),
	}
}

func (Account) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("groups", Group.Type),
		edge.To("proxy", Proxy.Type).Unique(),
		edge.To("usage_logs", UsageLog.Type),
	}
}
