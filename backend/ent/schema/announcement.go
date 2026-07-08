package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Announcement 站内公告。
//
// 管理员创建维护，登录用户经铃铛/弹窗查看。生效窗口（starts_at/ends_at）
// 在查询时惰性判定，不依赖定时任务翻状态。
type Announcement struct {
	ent.Schema
}

func (Announcement) Fields() []ent.Field {
	return []ent.Field{
		field.String("title").NotEmpty(),
		field.Text("content").
			Comment("公告内容（Markdown）"),
		field.String("status").Default("draft").
			Comment("状态: draft(草稿) / active(生效) / archived(归档)"),
		field.String("notify_mode").Default("silent").
			Comment("通知模式: silent(仅铃铛) / popup(弹窗提醒)"),
		field.Time("starts_at").Optional().Nillable().
			Comment("开始展示时间，空=立即生效"),
		field.Time("ends_at").Optional().Nillable().
			Comment("结束展示时间，空=永久展示"),
		field.Time("created_at").Default(timeNow).Immutable(),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

func (Announcement) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("status", "created_at"),
	}
}
