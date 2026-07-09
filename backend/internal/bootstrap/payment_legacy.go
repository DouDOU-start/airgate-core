package bootstrap

import (
	"context"
	"log/slog"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

// RenameLegacyPaymentTables 处理旧 airgate-epay 插件遗留的支付表。
//
// 必须在 ent Schema.Create **之前**调用：插件建的 payment_orders 主键是
// BIGSERIAL，ent 期望 IDENTITY 列，atlas 无法在两者间做变更
// （报错 "reverse alter table ... unexpected attribute change (expect IDENTITY)"）。
// 处理方式：把旧表改名为 *_plugin_legacy 原样保留（含数据），让 ent 全新建表；
// 数据由 RunStartupTasks 里的 backfillLegacyPaymentData 幂等回填。
// 旧表索引一并加 legacy_ 前缀改名，避免与新表的索引/约束名冲突
// （Postgres 索引名 schema 内全局唯一，改名表不改索引名）。
func RenameLegacyPaymentTables(ctx context.Context, drv *entsql.Driver) {
	if drv == nil || drv.Dialect() != dialect.Postgres {
		return
	}

	tables := []struct {
		name string
		// legacySQL 判定该表是否为插件旧结构（新 ent 表不满足条件则跳过改名）
		legacySQL string
	}{
		{
			name: "payment_orders",
			// 只有插件旧表有 channel 列（provider_id 的历史兼容冗余）
			legacySQL: `SELECT EXISTS (SELECT 1 FROM information_schema.columns
				WHERE table_schema = current_schema() AND table_name = 'payment_orders' AND column_name = 'channel')`,
		},
		{
			name: "payment_provider_configs",
			// 插件旧表主键 id 是 VARCHAR；新 ent 表 id 是整型 + provider_key 列
			legacySQL: `SELECT EXISTS (SELECT 1 FROM information_schema.columns
				WHERE table_schema = current_schema() AND table_name = 'payment_provider_configs'
				AND column_name = 'id' AND data_type = 'character varying')`,
		},
	}

	for _, table := range tables {
		if !queryStartupBool(ctx, drv, table.legacySQL) {
			continue
		}
		legacyName := table.name + "_plugin_legacy"
		steps := []startupSQL{
			{
				label: table.name + ".rename",
				sql:   `ALTER TABLE ` + table.name + ` RENAME TO ` + legacyName,
			},
			{
				// 逐个重命名旧表索引（含唯一约束底层索引），给新表让出默认命名空间
				label: table.name + ".rename_indexes",
				sql: `DO $$ DECLARE idx record; BEGIN
					FOR idx IN SELECT indexname FROM pg_indexes
						WHERE schemaname = current_schema() AND tablename = '` + legacyName + `'
					LOOP
						EXECUTE format('ALTER INDEX %I RENAME TO %I', idx.indexname, 'legacy_' || left(idx.indexname, 56));
					END LOOP;
				END $$`,
			},
		}
		ok := true
		for _, stmt := range steps {
			if !execStartupSQL(ctx, drv, stmt, "bootstrap_payment_legacy_rename_failed") {
				ok = false
				break
			}
		}
		if ok {
			slog.Info("bootstrap_payment_legacy_table_renamed", "table", table.name, "renamed_to", legacyName)
		}
	}
}

// backfillLegacyPaymentData 把 *_plugin_legacy 旧表数据幂等回填进 ent 新表。
// ON CONFLICT DO NOTHING：重复启动/部分回填过均安全。
// 服务商配置的敏感字段回填后仍是明文（插件时代不加密），
// payment service 解密失败时按明文兜底读取，管理端再次保存即转为密文。
func backfillLegacyPaymentData(drv *entsql.Driver) {
	if drv == nil || drv.Dialect() != dialect.Postgres {
		return
	}
	ctx := context.Background()

	if queryStartupBool(ctx, drv, `SELECT to_regclass('payment_provider_configs_plugin_legacy') IS NOT NULL`) {
		execStartupSQL(ctx, drv, startupSQL{
			label: "payment_provider_configs.backfill",
			sql: `INSERT INTO payment_provider_configs (provider_key, kind, enabled, config, created_at, updated_at)
				SELECT id, kind, enabled, config, created_at, updated_at
				FROM payment_provider_configs_plugin_legacy
				ON CONFLICT DO NOTHING`,
			logRows: true,
		}, "bootstrap_payment_legacy_backfill_failed")
	}

	if queryStartupBool(ctx, drv, `SELECT to_regclass('payment_orders_plugin_legacy') IS NOT NULL`) {
		execStartupSQL(ctx, drv, startupSQL{
			label: "payment_orders.backfill",
			sql: `INSERT INTO payment_orders (out_trade_no, user_id, method, provider_id, amount, status,
					subject, client_ip, payment_url, qr_code_content, notify_payload, paid_at, expires_at, created_at, updated_at)
				SELECT out_trade_no, user_id, COALESCE(method, ''),
					COALESCE(NULLIF(provider_id, ''), COALESCE(channel, '')),
					amount, COALESCE(status, 'pending'), COALESCE(subject, ''), COALESCE(client_ip, ''),
					COALESCE(payment_url, ''), COALESCE(qr_code_url, ''), COALESCE(notify_payload::text, ''),
					paid_at, COALESCE(expires_at, created_at), created_at, updated_at
				FROM payment_orders_plugin_legacy
				ON CONFLICT DO NOTHING`,
			logRows: true,
		}, "bootstrap_payment_legacy_backfill_failed")
	}
}

// queryStartupBool 执行返回单个布尔值的探测查询；失败按 false 处理（跳过后续步骤）。
func queryStartupBool(ctx context.Context, drv *entsql.Driver, query string) bool {
	var rows entsql.Rows
	if err := drv.Query(ctx, query, []any{}, &rows); err != nil {
		slog.Warn("bootstrap_startup_probe_failed", "sql", query, sdk.LogFieldError, err)
		return false
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return false
	}
	var ok bool
	if err := rows.Scan(&ok); err != nil {
		slog.Warn("bootstrap_startup_probe_scan_failed", sdk.LogFieldError, err)
		return false
	}
	return ok
}
