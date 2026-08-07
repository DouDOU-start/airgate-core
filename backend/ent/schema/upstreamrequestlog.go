package schema

import (
	"encoding/json"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// UpstreamRequestLog 打上游失败的留痕表：用户转发失败 + 渠道测试失败。
// 成功一律走 usage_logs（含渠道测试成功——真实上游消耗按 0 费用落账本）；
// 拉取模型不留痕（结果当场可见）。留痕判据：事后排障需要且当场看不到。
//
// 设计要点（区别于 usage_logs）：
//   - 零外键：user/api_key/channel/group 全部为纯快照列，渠道/用户硬删除
//     零牵连，批量插入不付 FK 校验成本（billing recorder 的 flushOneByOne
//     降级路径就是 FK 违约的教训）。
//   - 允许有损：错误风暴下经采样/限速落库，量级由 repeat_count 还原。
//   - 短保留：默认 30 天 TTL 清理（errlog.Recorder 内置任务）。
type UpstreamRequestLog struct {
	ent.Schema
}

func (UpstreamRequestLog) Fields() []ent.Field {
	return []ent.Field{
		// request_id 请求级 UUIDv7，与 X-Request-ID 响应头一致；
		// 渠道测试也生成，便于和服务端日志互查。
		field.String("request_id").NotEmpty(),
		// source 发起方：relay 用户转发 / channel_test 渠道测试。
		// 仅用于展示（前端把测试行标为「渠道测试」），不驱动筛选。
		field.Enum("source").Values("relay", "channel_test"),
		// phase 失败阶段（relay 专用细分；渠道测试行留空）：
		// precheck_balance 余额预检 402 / precheck_price 缺价 400 /
		// local_limit user·key 并发闸门 / queue_timeout 排队超时 /
		// upstream_exhausted failover 耗尽 / upstream_client_error 4xx 透传 /
		// canceled 未写出即取消 / stream_aborted 流式中断（含未收到 [DONE] 的静默不完整流）。
		field.String("phase").Default(""),
		// status_code 返回给客户端的 HTTP 状态码（admin-ops 为上游状态码）。
		field.Int("status_code").Default(0),
		// error_type / error_code 与 relay errfmt 写给客户端的错误体三元组同源，禁止另造枚举。
		field.String("error_type").Default(""),
		field.String("error_code").Default(""),
		// message 最终失败原因摘要（≤500 rune，已脱敏）。
		field.String("message").Default(""),
		// attempts 真实上游尝试次数；attempt_chain 重试链 JSON
		//（元素见 errlog.AttemptHop：渠道快照/verdict/上游状态/脱敏 reason/耗时等）。
		field.Int("attempts").Default(0),
		field.JSON("attempt_chain", json.RawMessage{}).Optional(),
		// billed 该请求是否同时产生了 usage_logs 落账（流式中断、4xx 带 usage）。
		// 失败率分母去重依赖本列：failure_rate = errors / (usage_count + errors[billed=false])。
		field.Bool("billed").Default(false),
		field.String("model").Default(""),
		field.String("endpoint").Default(""),
		field.Bool("stream").Default(false),
		// 归属快照（零 FK）；渠道测试行为 0/空。
		field.Int("user_id").Default(0),
		field.String("user_email_snapshot").Default(""),
		field.Int("api_key_id").Default(0),
		field.Int("group_id").Default(0),
		// 末次尝试路由快照：渠道与账号字段互斥；渠道测试行写目标渠道。
		field.Int("channel_id").Default(0),
		field.String("channel_name").Default(""),
		// channel_key_id 末次（或唯一次）选中的密钥端点；健康监测 key 级失败率依赖本列。
		// 账号路径恒 0；历史行迁移前亦为 0（仅能按 channel 粗聚合）。
		field.Int("channel_key_id").Default(0),
		field.Int("account_id").Default(0),
		field.String("account_name").Default(""),
		field.String("ip_address").Default(""),
		field.String("user_agent").Default(""),
		field.Int64("duration_ms").Default(0),
		// repeat_count 风暴折叠计数：键控去重窗口内折叠的同类记录数（含本行）。
		// 一切错误计数统计必须用 SUM(repeat_count) 而非 COUNT(*)。
		field.Int("repeat_count").Default(1),
		field.Time("created_at").Default(timeNow).Immutable(),
	}
}

func (UpstreamRequestLog) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("created_at").
			StorageKey("upstream_req_log_created_at"),
		index.Fields("user_id", "created_at").
			StorageKey("upstream_req_log_user_created_at"),
		index.Fields("api_key_id", "created_at").
			StorageKey("upstream_req_log_api_key_created_at"),
		index.Fields("channel_id", "created_at").
			StorageKey("upstream_req_log_channel_created_at"),
		index.Fields("channel_key_id", "created_at").
			StorageKey("upstream_req_log_channel_key_created_at"),
		index.Fields("phase", "created_at").
			StorageKey("upstream_req_log_phase_created_at"),
		index.Fields("request_id").
			StorageKey("upstream_req_log_request_id"),
	}
}
