package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// ModelTag 模型标签：用于给价目表模型按家族归类（claude / openai / gemini…）。
// 一个模型至多挂一个标签；删除标签时引用侧置空（由 store 层在事务内清引用）。
type ModelTag struct {
	ent.Schema
}

func (ModelTag) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").Unique().NotEmpty(),
		field.Time("created_at").Default(timeNow).Immutable(),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

func (ModelTag) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("prices", ModelPrice.Type),
	}
}
