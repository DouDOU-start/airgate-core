package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// Tier 用户等级：按等级批量分层定价。
// rates 是等级在各分组下的计费倍率（按 group_id 键），插在计费优先级链的
// 用户专属倍率（user.group_rates）与分组档位（group.rate_multiplier）之间，
// 见 internal/billing/rate.go。
type Tier struct {
	ent.Schema
}

func (Tier) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").Unique().NotEmpty(),
		field.JSON("rates", map[int64]float64{}).Optional(),
		field.String("note").Default(""),
		field.Int("sort_weight").Default(0),
		field.Time("created_at").Default(timeNow).Immutable(),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

func (Tier) Edges() []ent.Edge {
	return []ent.Edge{
		// 归属此等级的用户（一对多）
		edge.To("users", User.Type),
	}
}
