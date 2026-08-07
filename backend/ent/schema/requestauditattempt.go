package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// RequestAuditAttempt 保存一次真实上游尝试。一个请求可因 429、认证失败或网络错误
// 产生多行，seq 按实际触网顺序递增。
type RequestAuditAttempt struct {
	ent.Schema
}

func (RequestAuditAttempt) Fields() []ent.Field {
	return []ent.Field{
		field.Int("request_audit_id"),
		field.Int("seq"),
		field.Enum("route_kind").Values("channel", "account"),
		field.Int("channel_id").Default(0),
		field.String("channel_name").Default(""),
		field.Int("channel_key_id").Default(0),
		field.String("channel_key_name").Default(""),
		field.Int("account_id").Default(0),
		field.String("account_name").Default(""),
		field.String("account_email").Default(""),
		field.String("account_platform").Default(""),
		field.String("account_type").Default(""),
		field.String("method").Default(""),
		field.String("upstream_url_enc").Default("").SchemaType(largeTextSchemaType()),
		field.String("forward_headers_enc").Default("").SchemaType(largeTextSchemaType()),
		field.String("forward_body_enc").Default("").SchemaType(largeTextSchemaType()),
		field.Int64("forward_body_bytes").Default(0),
		field.Int("status_code").Default(0),
		field.String("verdict").Default(""),
		field.String("reason").Default("").SchemaType(largeTextSchemaType()),
		field.Int64("retry_after_ms").Default(0),
		field.Int64("latency_ms").Default(0),
		field.Int64("first_token_ms").Default(0),
		field.Bool("response_started").Default(false),
		field.Bool("stream_completed").Default(false),
		field.Bool("finished").Default(false),
		field.Time("created_at").Default(timeNow).Immutable(),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

func (RequestAuditAttempt) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("request", RequestAuditLog.Type).
			Ref("attempts").
			Field("request_audit_id").
			Unique().
			Required().
			Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

func (RequestAuditAttempt) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("request_audit_id", "seq").Unique().StorageKey("request_audit_attempt_seq"),
		index.Fields("account_id", "created_at").StorageKey("request_audit_attempt_account_created_at"),
		index.Fields("channel_id", "created_at").StorageKey("request_audit_attempt_channel_created_at"),
		index.Fields("status_code", "created_at").StorageKey("request_audit_attempt_status_created_at"),
	}
}
