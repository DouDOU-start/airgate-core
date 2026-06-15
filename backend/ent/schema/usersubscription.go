package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// UserSubscription 用户订阅
type UserSubscription struct {
	ent.Schema
}

func (UserSubscription) Fields() []ent.Field {
	return []ent.Field{
		field.Time("effective_at"),
		field.Time("expires_at"),
		field.JSON("usage", map[string]interface{}{}).Optional(), // 日/周/月使用量窗口
		field.Enum("status").Values("active", "expired", "suspended").Default("active"),
		field.Time("created_at").Default(timeNow).Immutable(),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

func (UserSubscription) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("status"),
	}
}

func (UserSubscription) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("subscriptions").Unique().Required(),
		edge.From("group", Group.Type).Ref("subscriptions").Unique().Required(),
	}
}
