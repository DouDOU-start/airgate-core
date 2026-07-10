package schema

import (
	"encoding/json"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Task 异步任务（视频/音乐等「提交-轮询」型转发）。
//
// 生命周期：提交成功落库（预扣额随行快照）→ 后台轮询上游刷新状态 →
// 终态结算（success 差额多退少补 + 落 usage_log；failure/超时全额退款）。
// 客户端查询读本行快照，不穿透上游。
//
// 归属字段与 upstream_request_logs 同口径：纯快照整型，不建 FK 边——
// 用户/渠道删除后任务行保留（排障与对账需要），悬空 ID 由查询侧容忍。
type Task struct {
	ent.Schema
}

func (Task) Fields() []ent.Field {
	return []ent.Field{
		// task_id 上游任务 ID（对外查询即用此 ID，OpenAI 兼容）。
		// 不设唯一约束：不同渠道理论上可能撞 ID，查询按 (platform, task_id, user) 收敛。
		field.String("task_id").NotEmpty(),
		// platform 任务平台（= 渠道 Type = 入口协议）：openai_video / suno。
		field.String("platform").NotEmpty(),
		// action 平台内动作：video 恒 generate；suno 为 music / lyrics。
		field.String("action").Default(""),
		field.Enum("status").
			Values("submitted", "queued", "in_progress", "success", "failure").
			Default("submitted"),
		field.Int("progress").Default(0).
			Comment("进度 0-100（上游口径归一化）"),
		field.String("fail_reason").Default(""),
		field.String("request_model").NotEmpty().
			Comment("对外模型名（计费/展示；隐藏渠道 model_mapping）"),
		field.String("upstream_model").Default(""),
		// 计费快照：预扣发生在提交时（同步扣 user.balance），终态按快照结算差额。
		field.Float("hold_amount").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Comment("预扣的 actual = est_total × rate_multiplier"),
		field.Float("est_total").Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Comment("估价 total（未乘倍率），结算差额的计算基准"),
		field.Float("rate_multiplier").Default(1.0).
			Comment("快照：预扣时生效的平台计费倍率（结算复用，防结算期费率漂移）"),
		field.Float("sell_rate").Default(0).
			Comment("快照：预扣时生效的 sell_rate；0 表示该 key 当时未启用 markup"),
		field.Float("account_rate_multiplier").Default(1.0).
			Comment("快照：预扣时生效的渠道成本倍率（channel.cost_ratio）"),
		field.Bool("settled").Default(false).
			Comment("结算幂等闸：终态后只结算一次（CAS 置位）"),
		field.Int("seconds").Default(0).
			Comment("视频时长参数（请求侧估价值；结算以上游实际值为准）；suno 恒 0"),
		// data 上游最新原始响应快照（查询端点据此重建响应）；写入侧截断（超限不存）。
		field.JSON("data", json.RawMessage{}).Optional(),
		field.Time("submit_time").Default(timeNow).
			Comment("提交成功时刻（超时清扫基准）"),
		field.Time("finish_time").Optional().Nillable(),
		field.String("request_id").Default(""),
		field.Int("user_id").Default(0),
		field.String("user_email_snapshot").Default(""),
		field.Int("api_key_id").Default(0),
		field.Int("group_id").Default(0),
		field.Int("channel_id").Default(0),
		field.Time("created_at").Default(timeNow).Immutable(),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

func (Task) Indexes() []ent.Index {
	return []ent.Index{
		// 客户端查询：按平台 + 上游任务 ID + 归属用户收敛。
		index.Fields("platform", "task_id"),
		// 轮询扫描：未完成任务按更新时间升序。
		index.Fields("status", "updated_at"),
		index.Fields("user_id", "created_at"),
		index.Fields("request_id"),
	}
}
