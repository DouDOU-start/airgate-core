package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ModerationLog 内容审核留痕表：风控中心的判定记录（命中与可选的未命中采样）。
//
// 设计要点（沿用 upstream_request_log 的零 FK 风格）：
//   - 零外键：user/api_key/group 全部为纯快照列，用户/分组硬删除零牵连，
//     异步批量插入不付 FK 校验成本。
//   - 输入摘录仅存脱敏截断后的片段（≤240 rune），原文永不落库。
//   - 双保留期 TTL 清理：命中长留（默认 180 天）、未命中短留（默认 3 天），
//     清理任务由 moderation.Engine 内置。
type ModerationLog struct {
	ent.Schema
}

func (ModerationLog) Fields() []ent.Field {
	return []ent.Field{
		// request_id 请求级 UUIDv7，与 X-Request-ID 响应头一致，便于与
		// usage_logs / upstream_request_logs 互查。
		field.String("request_id").NotEmpty(),
		// 归属快照（零 FK）。
		field.Int("user_id").Default(0),
		field.String("user_email_snapshot").Default(""),
		field.Int("api_key_id").Default(0),
		field.Int("group_id").Default(0),
		field.String("group_name_snapshot").Default(""),
		field.String("endpoint").Default(""),
		// protocol 输入抽取协议（openai_chat/openai_responses/openai_images/
		// anthropic_messages/gemini/openai_video/suno）。
		field.String("protocol").Default(""),
		field.String("model").Default(""),
		// mode 判定时的运行模式快照：observe / pre_block。
		field.String("mode").Default(""),
		// action 处置动作：allow 放行 / block 外部 API 命中拦截 /
		// keyword_block 关键词拦截 / hash_block 哈希缓存拦截 / error 审核出错放行。
		field.String("action").Default(""),
		field.Bool("flagged").Default(false),
		field.String("highest_category").Default(""),
		field.Float("highest_score").Default(0),
		field.String("matched_keyword").Default(""),
		// category_scores 外部审核 API 返回的全类别分值；threshold_snapshot
		// 判定时的阈值快照（两者对照可复盘为何命中/未命中）。
		field.JSON("category_scores", map[string]float64{}).Optional(),
		field.JSON("threshold_snapshot", map[string]float64{}).Optional(),
		// input_excerpt 已脱敏截断的输入摘录（≤240 rune）。
		field.String("input_excerpt").Default(""),
		// input_hash 输入内容 sha256（文本+图片），与 flagged hash 缓存同源。
		field.String("input_hash").Default(""),
		field.Int64("upstream_latency_ms").Default(0),
		// queue_delay_ms observe 异步审核的排队时延；同步判定为 0。
		field.Int64("queue_delay_ms").Default(0),
		field.String("error").Default(""),
		// violation_count 判定时刻该用户滑窗内的累计违规数（含本次）。
		field.Int("violation_count").Default(0),
		field.Bool("auto_banned").Default(false),
		field.Bool("email_sent").Default(false),
		field.Time("created_at").Default(timeNow).Immutable(),
	}
}

func (ModerationLog) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("created_at").
			StorageKey("moderation_log_created_at"),
		index.Fields("user_id", "created_at").
			StorageKey("moderation_log_user_created_at"),
		index.Fields("flagged", "created_at").
			StorageKey("moderation_log_flagged_created_at"),
		index.Fields("group_id", "created_at").
			StorageKey("moderation_log_group_created_at"),
		index.Fields("request_id").
			StorageKey("moderation_log_request_id"),
	}
}
