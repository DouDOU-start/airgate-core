package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// Proxy 出站代理（IP）：供账号绑定，多账号场景隔离出口。
//
// 语义：
//   - 账号未绑代理 → 直连；
//   - 账号绑了代理且 status=active → 出站经该代理；
//   - 账号绑了代理但 status=disabled → 账号不可调度（fail-closed，禁止静默直连）。
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
		field.String("password").Default("").Sensitive().
			Comment("带版本前缀的 AES-GCM 代理密码密文"),
		field.Enum("status").Values("active", "disabled").Default("active"),
		field.Time("created_at").Default(timeNow).Immutable(),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

func (Proxy) Edges() []ent.Edge {
	return []ent.Edge{
		// 反向：哪些账号使用了此代理
		edge.From("accounts", Account.Type).Ref("proxy"),
	}
}
