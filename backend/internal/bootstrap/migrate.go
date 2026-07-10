package bootstrap

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/migrate"
	"github.com/DouDOU-start/airgate-core/internal/pkg/logx"
)

// Migrate 执行数据库结构迁移：
//  1. 迁移前定点修复清单（幂等 SQL）：会让 ent 自动迁移本身失败的旧结构在此先行处理；
//  2. ent 自动迁移建齐缺失表与字段（非破坏性：不删列不删索引，存量库大变更由生产手动迁移）；
//  3. 存量库定点修复清单（幂等 SQL）：被新 schema 取代且会引发运行时冲突的旧结构在此登记清理。
//
// 修复项执行失败只 Warn 不中断启动（全新库本就没有旧结构，条件判断幂等空转）。
func Migrate(ctx context.Context, db *ent.Client, sqlDB *sql.DB) error {
	for _, stmt := range preMigrationFixups {
		if _, err := sqlDB.ExecContext(ctx, stmt); err != nil {
			slog.Warn("db_pre_migration_fixup_failed", "stmt", stmt, logx.LogFieldError, err)
		}
	}
	if err := db.Schema.Create(ctx, migrate.WithDropIndex(false), migrate.WithDropColumn(false)); err != nil {
		return err
	}
	for _, stmt := range legacyFixups {
		if _, err := sqlDB.ExecContext(ctx, stmt); err != nil {
			slog.Warn("db_legacy_fixup_failed", "stmt", stmt, logx.LogFieldError, err)
		}
	}
	return nil
}

// preMigrationFixups 在 ent 自动迁移之前执行的定点修复清单（幂等；按时间序追加，勿改历史条目）。
var preMigrationFixups = []string{
	// 2026-07：master 插件线的旧 tasks 表与本分支异步任务 tasks 表同名不同构
	// （旧表无 task_id 列且有存量行，ent 给它补 NOT NULL 列必然失败）。
	// 处理：改名归档为 tasks_legacy（数据保留不删），并把序列/约束/索引一并改名，
	// 防止与 ent 新建 tasks 表的默认命名（tasks_pkey / tasks_id_seq）冲突。
	// 判据用「无 task_id 列」区分新旧表：新表已存在时整块空转，幂等。
	`DO $$
DECLARE item record;
BEGIN
    IF to_regclass('tasks') IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema() AND table_name = 'tasks' AND column_name = 'task_id'
    ) THEN
        ALTER TABLE tasks RENAME TO tasks_legacy;
        ALTER SEQUENCE IF EXISTS tasks_id_seq RENAME TO tasks_legacy_id_seq;
        FOR item IN SELECT conname FROM pg_constraint
            WHERE conrelid = 'tasks_legacy'::regclass AND contype IN ('p','u') LOOP
            EXECUTE format('ALTER TABLE tasks_legacy RENAME CONSTRAINT %I TO %I', item.conname, 'legacy_' || item.conname);
        END LOOP;
        FOR item IN SELECT indexname FROM pg_indexes
            WHERE schemaname = current_schema() AND tablename = 'tasks_legacy'
              AND indexname NOT LIKE 'legacy\_%' LOOP
            EXECUTE format('ALTER INDEX %I RENAME TO %I', item.indexname, 'legacy_' || item.indexname);
        END LOOP;
    END IF;
END $$`,
}

// legacyFixups 存量库定点修复清单（幂等；按时间序追加，勿改历史条目）。
var legacyFixups = []string{
	// 2026-07：provisioned key 幂等键从（用户×应用）扩为（用户×应用×分组，
	// 见 ent/schema/apikey.go）。旧的两维部分唯一索引已被三维索引取代，
	// 不清理会导致同应用按组领第二把 key 时撞旧约束。
	`DROP INDEX IF EXISTS apikey_provisioned_by_user_api_keys`,
}
