package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// AnnouncementRead 公告已读记录。
//
// 每用户每公告至多一条（联合唯一索引兜底），重复标记幂等、不覆盖首次已读时间。
// 公告删除时由 store 层在事务内清理关联记录。
type AnnouncementRead struct {
	ent.Schema
}

func (AnnouncementRead) Fields() []ent.Field {
	return []ent.Field{
		field.Int("announcement_id").Positive(),
		field.Int("user_id").Positive(),
		field.Time("read_at").Default(timeNow).
			Comment("首次已读时间"),
		field.Time("created_at").Default(timeNow).Immutable(),
	}
}

func (AnnouncementRead) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("announcement_id", "user_id").Unique(),
		index.Fields("user_id"),
	}
}
