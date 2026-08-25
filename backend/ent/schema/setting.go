package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// Setting 系统设置
type Setting struct {
	ent.Schema
}

func (Setting) Fields() []ent.Field {
	return []ent.Field{
		field.String("key").Unique().NotEmpty(),
		field.String("value").Default(""),
		field.String("group").Default("general"), // 设置分组
		field.Time("created_at").Default(timeNow).Immutable(),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}
