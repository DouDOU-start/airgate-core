package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Task 统一异步任务。
//
// Core 负责持久化和调度，插件实现 TaskProcessor 处理具体逻辑。
// 状态机：pending → processing → completed / failed / cancelling → cancelled / retrying → pending
type Task struct {
	ent.Schema
}

func (Task) Fields() []ent.Field {
	return []ent.Field{
		field.String("plugin_id").NotEmpty().
			Comment("所属插件 ID (e.g. airgate-playground)"),
		field.String("task_type").NotEmpty().
			Comment("任务类型 (e.g. image_generation, video_generation)"),
		field.Enum("status").
			Values("pending", "processing", "retrying", "completed", "failed", "cancelling", "cancelled").
			Default("pending"),
		field.String("stage").Default("").
			Comment("插件声明的当前阶段，仅用于展示和调试"),
		field.Int("user_id").Positive(),
		field.JSON("input", map[string]interface{}{}).
			Default(map[string]interface{}{}).
			Comment("任务输入参数 (JSONB)"),
		field.JSON("output", map[string]interface{}{}).
			Optional().
			Default(map[string]interface{}{}).
			Comment("任务结果 (JSONB)"),
		field.JSON("attributes", map[string]interface{}{}).
			Optional().
			Default(map[string]interface{}{}).
			Comment("插件提供的少量展示/筛选维度，Core 不理解业务含义"),
		field.JSON("execution", map[string]interface{}{}).
			Optional().
			Default(map[string]interface{}{}).
			Comment("插件内部执行状态，例如上游 task id、轮询状态"),
		field.String("error_type").Default(""),
		field.String("error_code").Default(""),
		field.String("error_message").Default(""),
		field.Int("usage_id").Optional().Nillable().
			Comment("关联 usage_log.id，完成后的模型、计量和费用事实以 usage 为准"),
		field.Int("progress").Default(0).Min(0).Max(100),
		field.Int("priority").Default(0).
			Comment("越高越优先处理"),
		field.Int("attempts").Default(0),
		field.Int("max_attempts").Default(3),
		field.String("public_task_id").Optional().Nillable().
			Comment("对外暴露的任务 ID，全局唯一；不参与幂等语义"),
		field.String("idempotency_key").Optional().Nillable().
			Comment("同一用户、插件、任务类型内的幂等键"),
		field.Time("created_at").Default(timeNow).Immutable(),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
		field.Time("started_at").Optional().Nillable(),
		field.Time("completed_at").Optional().Nillable(),
		field.Time("cancel_requested_at").Optional().Nillable(),
		field.Time("expires_at").Optional().Nillable(),
	}
}

func (Task) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("plugin_id", "status", "created_at"),
		index.Fields("user_id", "created_at"),
		index.Fields("status", "created_at"),
		index.Fields("public_task_id").Unique(),
		index.Fields("plugin_id", "user_id", "task_type", "idempotency_key").Unique(),
	}
}
