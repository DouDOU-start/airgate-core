package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// Bookmark 备忘录：轻量地址簿，记录渠道名称、地址与备注，不参与转发调度。
type Bookmark struct {
	ent.Schema
}

func (Bookmark) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").NotEmpty(),
		field.String("base_url").Default(""),
		field.Text("remark").Default(""),
		field.Time("created_at").Default(timeNow).Immutable(),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}
