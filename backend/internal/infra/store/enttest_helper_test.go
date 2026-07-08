package store

import (
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/enttest"
	"github.com/DouDOU-start/airgate-core/ent/migrate"
)

// enttestOpen 打开共享内存 sqlite 的 ent 测试客户端。
func enttestOpen(t *testing.T) *ent.Client {
	t.Helper()
	return enttest.Open(t, "sqlite3", "file:store_test?mode=memory&cache=shared&_fk=1", enttest.WithMigrateOptions(migrate.WithGlobalUniqueID(false)))
}
