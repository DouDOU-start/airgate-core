package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// APIKey API 密钥
type APIKey struct {
	ent.Schema
}

func (APIKey) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").NotEmpty(),
		field.String("key_hint").Default(""),
		field.String("key_hash").NotEmpty().Sensitive(),
		field.String("key_encrypted").Optional().Sensitive(),
		field.JSON("ip_whitelist", []string{}).Optional(),
		field.JSON("ip_blacklist", []string{}).Optional(),
		field.Float("quota_usd").Default(0),
		field.Float("used_quota").Default(0),
		field.Time("expires_at").Optional().Nillable(),
		field.Enum("status").Values("active", "disabled").Default("active"),
		field.Time("created_at").Default(timeNow).Immutable(),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

func (APIKey) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("api_keys").Unique().Required(),
		edge.From("group", Group.Type).Ref("api_keys").Unique(),
		edge.To("usage_logs", UsageLog.Type),
	}
}
