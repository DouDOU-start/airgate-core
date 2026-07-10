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
//  1. ent 自动迁移建齐缺失表与字段（非破坏性：不删列不删索引，存量库大变更由生产手动迁移）；
//  2. 存量库定点修复清单（幂等 SQL）：被新 schema 取代且会引发运行时冲突的旧结构在此登记清理。
//
// 修复项执行失败只 Warn 不中断启动（全新库本就没有旧结构，DROP IF EXISTS 幂等空转）。
func Migrate(ctx context.Context, db *ent.Client, sqlDB *sql.DB) error {
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

// legacyFixups 存量库定点修复清单（幂等；按时间序追加，勿改历史条目）。
var legacyFixups = []string{
	// 2026-07：provisioned key 幂等键从（用户×应用）扩为（用户×应用×分组，
	// 见 ent/schema/apikey.go）。旧的两维部分唯一索引已被三维索引取代，
	// 不清理会导致同应用按组领第二把 key 时撞旧约束。
	`DROP INDEX IF EXISTS apikey_provisioned_by_user_api_keys`,
}
