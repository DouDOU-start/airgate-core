package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// PaymentProviderConfig 支付服务商实例配置。
// 一条记录 = 一套协议（kind）+ 一份商户凭证；同 kind 可配多实例。
// config 为 provider 特定字段的 JSON blob（各协议字段差异大，不拆列）；
// 敏感字段（密钥/私钥）由 service 层 AES-256-GCM 加密后写入。
type PaymentProviderConfig struct {
	ent.Schema
}

func (PaymentProviderConfig) Fields() []ent.Field {
	return []ent.Field{
		// provider 实例标识（如 epay_xunhu_1），订单 provider_id 与回调路由都引用它。
		field.String("provider_key").Unique().NotEmpty(),
		field.String("kind").NotEmpty(),
		field.Bool("enabled").Default(true),
		field.JSON("config", map[string]string{}),
		field.Time("created_at").Default(timeNow).Immutable(),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}
