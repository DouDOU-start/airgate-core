package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// Plugin 已安装插件
type Plugin struct {
	ent.Schema
}

func (Plugin) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").Unique().NotEmpty(),
		field.String("platform").Default(""),
		field.String("version").Default(""),
		field.Enum("type").Values("gateway", "payment", "extension").Default("gateway"),
		field.Enum("status").Values("installed", "enabled", "disabled").Default("installed"),
		field.JSON("config", map[string]interface{}{}).Optional(),
		field.String("binary_path").Default(""),
		field.Time("created_at").Default(timeNow).Immutable(),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}
