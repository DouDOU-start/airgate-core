package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// RequestAuditLog 保存已通过 API Key 鉴权并进入转发管线的完整入站请求快照。
// Header 与 Body 均为压缩后的 AES-GCM 密文，避免凭证和提示词以明文落库。
type RequestAuditLog struct {
	ent.Schema
}

func (RequestAuditLog) Fields() []ent.Field {
	return []ent.Field{
		field.String("request_id").NotEmpty(),
		field.Int("user_id").Default(0),
		field.String("user_email_snapshot").Default(""),
		field.Int("api_key_id").Default(0),
		field.Int("group_id").Default(0),
		field.String("client").Default(""),
		field.String("protocol").Default(""),
		field.String("endpoint").Default(""),
		field.String("model").Default(""),
		field.Bool("stream").Default(false),
		field.String("method").Default(""),
		field.String("path").Default(""),
		field.String("raw_query").Default(""),
		field.String("host").Default(""),
		field.String("request_proto").Default(""),
		field.String("remote_addr").Default(""),
		field.String("ip_address").Default(""),
		field.String("user_agent").Default(""),
		field.String("content_type").Default(""),
		field.Int64("content_length").Default(0),
		field.String("inbound_headers_enc").Default("").SchemaType(largeTextSchemaType()),
		field.String("inbound_body_enc").Default("").SchemaType(largeTextSchemaType()),
		field.Int64("inbound_body_bytes").Default(0),
		field.Int("status_code").Default(0),
		field.Int64("duration_ms").Default(0),
		field.Int64("response_bytes").Default(0),
		field.Bool("completed").Default(false),
		field.Time("created_at").Default(timeNow).Immutable(),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

// largeTextSchemaType 为完整请求快照使用大文本列。请求体上限为 32MB，
// AES-GCM + base64 后会继续膨胀，不能使用 Ent 对 string 的默认 VARCHAR(255)。
func largeTextSchemaType() map[string]string {
	return map[string]string{
		dialect.MySQL:    "longtext",
		dialect.Postgres: "text",
		dialect.SQLite:   "text",
	}
}

func (RequestAuditLog) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("attempts", RequestAuditAttempt.Type),
	}
}

func (RequestAuditLog) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("created_at").StorageKey("request_audit_created_at"),
		index.Fields("request_id").StorageKey("request_audit_request_id"),
		index.Fields("user_id", "created_at").StorageKey("request_audit_user_created_at"),
		index.Fields("api_key_id", "created_at").StorageKey("request_audit_api_key_created_at"),
		index.Fields("model", "created_at").StorageKey("request_audit_model_created_at"),
		index.Fields("status_code", "created_at").StorageKey("request_audit_status_created_at"),
	}
}
