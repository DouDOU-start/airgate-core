package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// UsageLog 使用日志（只追加）
type UsageLog struct {
	ent.Schema
}

func (UsageLog) Fields() []ent.Field {
	return []ent.Field{
		field.String("model").NotEmpty(),
		field.Int("input_tokens").Default(0),
		field.Int("output_tokens").Default(0),
		field.Int("cached_input_tokens").Default(0),
		field.Int("cache_creation_tokens").Default(0),
		field.Int("cache_creation_5m_tokens").Default(0),
		field.Int("cache_creation_1h_tokens").Default(0),
		field.Int("calls").Default(0).
			Comment("计费数量或产出数量：per_request=次数，per_image=张数，per_second=秒数；token 图片端点可记录产出张数。"),
		field.String("billing_mode").Default("").
			Comment("计费模式快照：token / per_request / per_image / per_second；空值为历史记录。"),
		field.Float("input_price").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("output_price").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("cached_input_price").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("cache_creation_price").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("cache_creation_1h_price").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("input_cost").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("output_cost").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("cached_input_cost").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("cache_creation_cost").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("total_cost").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("actual_cost").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Comment("平台对 reseller 的真实扣费 = total × billing_rate（group/user）"),
		field.Float("billed_cost").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Comment("账面消耗：reseller 对最终客户的计费金额。sell_rate=0 时等于 actual_cost。永远不参与平台账户/统计。"),
		field.Float("rate_multiplier").Default(1.0).
			Comment("快照：本次请求生效的平台计费倍率（ResolveBillingRate 结果）"),
		field.Float("sell_rate").Default(0).
			Comment("快照：本次请求生效的 sell_rate；0 表示该 key 当时未启用 markup"),
		field.Float("account_rate_multiplier").Default(1.0).
			Comment("快照：成本倍率。渠道路径=channel cost_ratio/upstream_rate；账号路径=account.rate_multiplier。成本 = total_cost × 本列，查询期现算不落列。"),
		field.String("service_tier").Default(""),
		// 推理强度档位（low/medium/high/xhigh/max）：OpenAI reasoning_effort/
		// Responses reasoning.effort，Anthropic output_config.effort，三协议统一后的扁平字符串。
		field.String("reasoning_effort").Default(""),
		// 图像端点实际产出档位（响应顶层 size/quality，以响应为准）：
		// 分辨率价表计费的留痕依据，事后可还原该单按哪档、几张扣费；非图像端点恒空。
		field.String("image_size").Default(""),
		field.String("image_quality").Default(""),
		field.String("video_resolution").Default("").
			Comment("视频任务计费分辨率档位（如 480p/720p/1080p）；非视频任务恒空"),
		field.String("usage_status").Default("completed").
			Comment("计量状态：completed / usage_missing / stream_aborted / stream_aborted_usage_missing"),
		field.Bool("stream").Default(false),
		field.Int64("duration_ms").Default(0),
		field.Int64("first_token_ms").Default(0),
		// user_agent 写入侧截断（recordUsage），schema 不设 MaxLen 以免存量超长行阻塞迁移。
		field.String("user_agent").Default(""),
		field.String("ip_address").Default(""),
		// 请求端点。
		field.String("endpoint").Default(""),
		// 记账来源：relay 用户转发 / channel_test 渠道测试（管理员操作，无用户归属）。
		field.String("source").Default("relay"),
		// 请求 ID（X-Request-ID）：与 upstream_request_logs.request_id 互查，
		// 定位一次请求的计费行与失败/重试留痕。
		field.String("request_id").Default(""),
		field.Int("user_id_snapshot").Default(0).
			Comment("用户 ID 快照。用户硬删除后保留历史使用记录与计费归属。"),
		field.String("user_email_snapshot").Default("").
			Comment("用户邮箱快照。用户硬删除后后台使用记录仍能展示历史归属。"),
		field.Time("created_at").Default(timeNow).Immutable(),
		// 边 FK 显式建模（storage key 沿用 ent 隐式边列名，存量数据零迁移）：
		// 让组合索引能以 FK 打头（ent 索引列序固定 Fields 在前），
		// 同时生成直连 FK 谓词替代 EXISTS 子查询。
		field.Int("user_id").Optional().StorageKey("user_usage_logs"),
		field.Int("api_key_id").Optional().StorageKey("api_key_usage_logs"),
		field.Int("channel_id").Optional().StorageKey("channel_usage_logs"),
		field.Int("channel_key_id").Optional().StorageKey("channel_key_usage_logs"),
		// account_id 订阅账号路径写入；与 channel_key_id 互斥填充。
		field.Int("account_id").Optional().StorageKey("account_usage_logs"),
		field.Int("group_id").Optional().StorageKey("group_usage_logs"),
	}
}

func (UsageLog) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("usage_logs").Unique().Field("user_id"),
		edge.From("api_key", APIKey.Type).Ref("usage_logs").Unique().Field("api_key_id"),
		// channel FK 的 ON DELETE SET NULL 声明在 Channel 侧 assoc 边
		//（ent 生成 FK 时只读 edge.To 的注解）；见 schema/channel.go。
		edge.From("channel", Channel.Type).Ref("usage_logs").Unique().Field("channel_id"),
		// channel_key FK 的 ON DELETE SET NULL 声明在 ChannelKey 侧 assoc 边。
		edge.From("channel_key", ChannelKey.Type).Ref("usage_logs").Unique().Field("channel_key_id"),
		// account FK 的 ON DELETE SET NULL 声明在 Account 侧 assoc 边。
		edge.From("account", Account.Type).Ref("usage_logs").Unique().Field("account_id"),
		edge.From("group", Group.Type).Ref("usage_logs").Unique().Field("group_id"),
	}
}

func (UsageLog) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("created_at").
			StorageKey("usage_log_created_at"),
		index.Fields("user_id_snapshot", "created_at").
			StorageKey("usage_log_user_snapshot_created_at"),
		index.Fields("model", "created_at").
			StorageKey("usage_log_model_created_at"),
		index.Edges("user").
			StorageKey("usage_log_user"),
		index.Edges("api_key").
			StorageKey("usage_log_api_key"),
		index.Edges("channel").
			StorageKey("usage_log_channel"),
		index.Edges("group").
			StorageKey("usage_log_group"),
		// 边 FK + created_at 组合索引：列表页按 key/渠道/分组过滤时
		// 走前缀等值 + 时间序索引扫描，免去大结果集排序与全表计数。
		index.Fields("api_key_id", "created_at").
			StorageKey("usage_log_api_key_created_at"),
		index.Fields("channel_id", "created_at").
			StorageKey("usage_log_channel_created_at"),
		index.Fields("channel_key_id", "created_at").
			StorageKey("usage_log_channel_key_created_at"),
		index.Fields("account_id", "created_at").
			StorageKey("usage_log_account_created_at"),
		index.Fields("group_id", "created_at").
			StorageKey("usage_log_group_created_at"),
		// request_id 互查：从失败留痕跳查计费行，无索引则大表全扫。
		index.Fields("request_id").
			StorageKey("usage_log_request_id"),
	}
}
