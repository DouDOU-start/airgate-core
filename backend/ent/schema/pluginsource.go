package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// PluginSource 插件源
type PluginSource struct {
	ent.Schema
}

func (PluginSource) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").Unique().NotEmpty(),
		field.String("url").NotEmpty(),
		field.Bool("is_official").Default(false),
		field.Time("last_sync_at").Optional().Nillable(),
		field.Time("created_at").Default(timeNow).Immutable(),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}
