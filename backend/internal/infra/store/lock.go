package store

import (
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

// forUpdateLock 是一个通用"谓词"：不追加 WHERE 条件，仅在 Postgres 上给
// SELECT 附加 FOR UPDATE 行锁，用于余额等读-改-写事务中锁定当前行，
// 防止与计费侧的原子扣减并发交错造成丢失更新。
//
// sqlite（测试环境）不支持 FOR UPDATE 语法；其整库单写者模型天然串行化
// 写事务，跳过加锁不影响正确性。
//
// 用法：tx.User.Query().Where(entuser.IDEQ(id)).Where(forUpdateLock).Only(ctx)
func forUpdateLock(s *entsql.Selector) {
	if s.Dialect() == dialect.Postgres {
		s.ForUpdate()
	}
}
