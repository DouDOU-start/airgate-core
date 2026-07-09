package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// OAuthClient 第三方应用接入（OAuth2 授权码 + PKCE 客户端）。
//
// core 作为统一身份源：外部应用（如独立部署的对话/创作中心）注册为
// OAuth client 后经授权码流程获取用户身份，再经 provision-key 获取
// 该用户名下的 sk- key 调用 /v1 网关。core 不认识任何具体应用——
// 应用只是这张表里的数据行。
type OAuthClient struct {
	ent.Schema
}

func (OAuthClient) Fields() []ent.Field {
	return []ent.Field{
		field.String("client_id").Unique().NotEmpty().Immutable().
			Comment("对外客户端标识，创建时生成，不可变"),
		field.String("secret_hash").NotEmpty().
			Comment("client secret 的 SHA-256 哈希，仅用于校验；明文只在创建/重置时返回一次"),
		field.String("secret_hint").Default("").
			Comment("secret 尾 4 位提示，用于管理面展示"),
		field.String("name").NotEmpty().
			Comment("应用名称（管理面与授权确认页展示）"),
		field.String("description").Default(""),
		field.JSON("redirect_uris", []string{}).
			Comment("允许的回调地址白名单，授权时精确匹配"),
		field.Bool("first_party").Default(false).
			Comment("第一方应用：授权时跳过确认页，静默签发授权码"),
		field.Bool("enabled").Default(true),
		field.Bool("show_in_nav").Default(false).
			Comment("是否在用户端导航展示入口"),
		field.String("launch_url").Default("").
			Comment("导航入口跳转地址（应用首页）"),
		field.String("icon").Default("").
			Comment("导航图标（emoji 或图片 URL）"),
		field.Int("sort_order").Default(0).
			Comment("导航排序，越小越靠前"),
		field.Time("created_at").Default(timeNow).Immutable(),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

func (OAuthClient) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("enabled", "show_in_nav", "sort_order"),
	}
}
