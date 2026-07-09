package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// ModelPrice 模型价目表：平台统一标价（USD/1M tokens 或按次价），与落到哪个渠道无关。
type ModelPrice struct {
	ent.Schema
}

func (ModelPrice) Fields() []ent.Field {
	return []ent.Field{
		field.String("model").Unique().NotEmpty(),
		field.Float("input_price").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("output_price").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("cached_input_price").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("cache_creation_price").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Comment("缓存写入 5m TTL 单价（与 usage_log.cache_creation_price 口径一致）"),
		field.Float("cache_creation_1h_price").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Comment("缓存写入 1h TTL 单价（Claude 双档缓存写入的长档）"),
		field.Float("per_request_price").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Comment("按次价：>0 时整条请求按次计费，忽略 token 单价"),
		// pricing_extra 承载服务档倍率（priority/flex）与长上下文阶梯等长尾维度，
		// 多数模型为空。结构见 relay/pricing 与 modelprice service Loader。
		field.JSON("pricing_extra", map[string]interface{}{}).Optional(),
		// tag_id 模型标签外键（家族归类，可空）；删除标签时由 store 层先清引用。
		field.Int("tag_id").Optional().Nillable(),
		field.Time("created_at").Default(timeNow).Immutable(),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

func (ModelPrice) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("tag", ModelTag.Type).
			Ref("prices").
			Field("tag_id").
			Unique(),
	}
}
