package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
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
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("per_request_price").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Comment("按次价：>0 时整条请求按次计费，忽略 token 单价"),
		field.Time("created_at").Default(timeNow).Immutable(),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}
