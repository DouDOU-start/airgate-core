package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// PaymentOrder 充值订单（自 airgate-epay 插件迁入的内置支付功能）。
// 状态机：pending → paid（回调验签成功并入账）/ expired（超时清理）。
// user_id 为普通列（无 FK 边）：用户删除后订单留存作为收款凭据。
type PaymentOrder struct {
	ent.Schema
}

func (PaymentOrder) Fields() []ent.Field {
	return []ent.Field{
		// 商户订单号（AG + 时间戳 + 随机 hex），唯一约束是防重复入账的第一层。
		field.String("out_trade_no").Unique().NotEmpty(),
		field.Int("user_id"),
		// method 用户选择的支付方式（alipay / wxpay）；provider_id 实际承接的服务商实例 ID。
		field.String("method").NotEmpty(),
		field.String("provider_id").NotEmpty(),
		field.Float("amount").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Comment("充值金额（元），1:1 计入余额"),
		field.String("status").Default("pending"),
		field.String("subject").Default(""),
		field.String("client_ip").Default(""),
		// 支付跳转链接与二维码内容（下单时由 provider 返回，供前端展示/续付）。
		field.String("payment_url").Default(""),
		field.String("qr_code_content").Default(""),
		// 回调原始载荷（审计留痕），仅成功入账时写入。
		field.String("notify_payload").Default(""),
		field.Time("paid_at").Optional().Nillable(),
		field.Time("expires_at"),
		field.Time("created_at").Default(timeNow).Immutable(),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

func (PaymentOrder) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id", "created_at").
			StorageKey("payment_order_user_created_at"),
		index.Fields("status", "expires_at").
			StorageKey("payment_order_status_expires_at"),
		index.Fields("created_at").
			StorageKey("payment_order_created_at"),
	}
}
