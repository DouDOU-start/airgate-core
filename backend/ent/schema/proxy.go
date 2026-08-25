package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// Proxy 代理
type Proxy struct {
	ent.Schema
}

func (Proxy) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").NotEmpty(),
		field.Enum("protocol").Values("http", "socks5").Default("http"),
		field.String("address").NotEmpty(),
		field.Int("port"),
		field.String("username").Default(""),
		field.String("password").Default("").Sensitive(),
		field.Enum("status").Values("active", "disabled").Default("active"),
		field.Time("created_at").Default(timeNow).Immutable(),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

func (Proxy) Edges() []ent.Edge {
	return []ent.Edge{
		// 反向关联：哪些账号使用了此代理
		edge.From("accounts", Account.Type).Ref("proxy"),
	}
}
