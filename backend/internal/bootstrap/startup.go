package bootstrap

import (
	"context"
	"log/slog"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/apikey"
	"github.com/DouDOU-start/airgate-core/internal/auth"
)

// RunStartupTasks 运行启动阶段的整理任务。
func RunStartupTasks(db *ent.Client, drv *entsql.Driver, apiKeySecret string) {
	slog.Info("bootstrap_startup_tasks_start")
	backfillKeyHints(db, apiKeySecret)
	backfillResellerMarkupColumns(drv)
	migrateUserHistoryRefs(drv)
	slog.Info("bootstrap_startup_tasks_done")
}

// migrateUserHistoryRefs 允许硬删除用户，同时保留历史使用记录和余额流水。
// 用量/计费聚合依赖 usage_logs 的成本快照字段；这里把历史表的 user 外键改为 SET NULL，
// 并回填 user_id/user_email 快照，避免删除用户后历史记录丢失归属信息。
func migrateUserHistoryRefs(drv *entsql.Driver) {
	if drv == nil {
		return
	}
	if drv.Dialect() != dialect.Postgres {
		return
	}
	ctx := context.Background()

	statements := []startupSQL{
		{
			label: "usage_logs.user_id_snapshot",
			sql:   `ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS user_id_snapshot integer NOT NULL DEFAULT 0`,
		},
		{
			label: "usage_logs.user_email_snapshot",
			sql:   `ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS user_email_snapshot text NOT NULL DEFAULT ''`,
		},
		{
			label: "usage_logs.snapshot_backfill",
			sql: `UPDATE usage_logs AS ul
				SET user_id_snapshot = CASE WHEN ul.user_id_snapshot = 0 THEN u.id ELSE ul.user_id_snapshot END,
					user_email_snapshot = CASE WHEN ul.user_email_snapshot = '' THEN u.email ELSE ul.user_email_snapshot END
				FROM users AS u
				WHERE ul.user_usage_logs = u.id
					AND (ul.user_id_snapshot = 0 OR ul.user_email_snapshot = '')`,
			logRows: true,
		},
		{
			label: "usage_logs.user_usage_logs_nullable",
			sql:   `ALTER TABLE usage_logs ALTER COLUMN user_usage_logs DROP NOT NULL`,
		},
		{
			label: "balance_logs.user_id_snapshot",
			sql:   `ALTER TABLE balance_logs ADD COLUMN IF NOT EXISTS user_id_snapshot integer NOT NULL DEFAULT 0`,
		},
		{
			label: "balance_logs.user_email_snapshot",
			sql:   `ALTER TABLE balance_logs ADD COLUMN IF NOT EXISTS user_email_snapshot text NOT NULL DEFAULT ''`,
		},
		{
			label: "balance_logs.snapshot_backfill",
			sql: `UPDATE balance_logs AS bl
				SET user_id_snapshot = CASE WHEN bl.user_id_snapshot = 0 THEN u.id ELSE bl.user_id_snapshot END,
					user_email_snapshot = CASE WHEN bl.user_email_snapshot = '' THEN u.email ELSE bl.user_email_snapshot END
				FROM users AS u
				WHERE bl.user_balance_logs = u.id
					AND (bl.user_id_snapshot = 0 OR bl.user_email_snapshot = '')`,
			logRows: true,
		},
		{
			label: "balance_logs.user_balance_logs_nullable",
			sql:   `ALTER TABLE balance_logs ALTER COLUMN user_balance_logs DROP NOT NULL`,
		},
	}

	for _, stmt := range statements {
		if !execStartupSQL(ctx, drv, stmt, "bootstrap_user_history_refs_migration_failed") {
			return
		}
	}

	ensureUserHistoryForeignKey(ctx, drv, userHistoryForeignKey{
		table:      "usage_logs",
		constraint: "usage_logs_users_usage_logs",
		column:     "user_usage_logs",
	})
	ensureUserHistoryForeignKey(ctx, drv, userHistoryForeignKey{
		table:      "balance_logs",
		constraint: "balance_logs_users_balance_logs",
		column:     "user_balance_logs",
	})
}

type startupSQL struct {
	label   string
	sql     string
	logRows bool
}

func execStartupSQL(ctx context.Context, drv *entsql.Driver, stmt startupSQL, failureLog string) bool {
	var r entsql.Result
	if err := drv.Exec(ctx, stmt.sql, []any{}, &r); err != nil {
		slog.Warn(failureLog, "label", stmt.label, "sql", stmt.sql, sdk.LogFieldError, err)
		return false
	}
	if stmt.logRows {
		if affected, err := r.RowsAffected(); err == nil && affected > 0 {
			slog.Info("bootstrap_startup_sql_done", "label", stmt.label, "rows", affected)
		}
	}
	return true
}

type userHistoryForeignKey struct {
	table      string
	constraint string
	column     string
}

func ensureUserHistoryForeignKey(ctx context.Context, drv *entsql.Driver, fk userHistoryForeignKey) {
	if userHistoryForeignKeyIsSetNull(ctx, drv, fk.constraint) {
		return
	}

	statements := []startupSQL{
		{
			label: fk.constraint + ".drop",
			sql:   "ALTER TABLE " + fk.table + " DROP CONSTRAINT IF EXISTS " + fk.constraint,
		},
		{
			label: fk.constraint + ".add_set_null",
			sql: "ALTER TABLE " + fk.table + " ADD CONSTRAINT " + fk.constraint +
				" FOREIGN KEY (" + fk.column + ") REFERENCES users(id) ON DELETE SET NULL",
		},
	}
	for _, stmt := range statements {
		if !execStartupSQL(ctx, drv, stmt, "bootstrap_user_history_refs_fk_migration_failed") {
			return
		}
	}
}

func userHistoryForeignKeyIsSetNull(ctx context.Context, drv *entsql.Driver, constraint string) bool {
	var rows entsql.Rows
	const query = `SELECT confdeltype = 'n'
		FROM pg_constraint
		WHERE conname = $1 AND contype = 'f'
		LIMIT 1`
	if err := drv.Query(ctx, query, []any{constraint}, &rows); err != nil {
		slog.Warn("bootstrap_user_history_refs_fk_check_failed", "constraint", constraint, sdk.LogFieldError, err)
		return false
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return false
	}
	var ok bool
	if err := rows.Scan(&ok); err != nil {
		slog.Warn("bootstrap_user_history_refs_fk_check_scan_failed", "constraint", constraint, sdk.LogFieldError, err)
		return false
	}
	return ok
}

// backfillResellerMarkupColumns 一次性回填 reseller markup 改造引入的两个新列：
//   - usage_logs.billed_cost：历史行未启用 markup，账面 = 真实成本
//   - api_keys.used_quota_actual：历史 key 未启用 markup，actual 累加值 = used_quota
//
// SQL 使用 idempotent 条件 WHERE billed_cost = 0 / used_quota_actual = 0，
// 多次启动重复执行也不会污染已经被新代码正确写入的数据。
func backfillResellerMarkupColumns(drv *entsql.Driver) {
	if drv == nil {
		return
	}
	ctx := context.Background()

	statements := []struct {
		label string
		sql   string
	}{
		{"usage_logs.billed_cost", "UPDATE usage_logs SET billed_cost = actual_cost WHERE billed_cost = 0 AND actual_cost > 0"},
		// 历史 account_rate 全是 1.0，account_cost 等价 total_cost
		{"usage_logs.account_cost", "UPDATE usage_logs SET account_cost = total_cost WHERE account_cost = 0 AND total_cost > 0"},
		{"api_keys.used_quota_actual", "UPDATE api_keys SET used_quota_actual = used_quota WHERE used_quota_actual = 0 AND used_quota > 0"},
	}

	for _, stmt := range statements {
		var res entsql.Result
		if err := drv.Exec(ctx, stmt.sql, []any{}, &res); err != nil {
			slog.Warn("bootstrap_reseller_backfill_failed", "table", stmt.label, sdk.LogFieldError, err)
			continue
		}
		if affected, err := res.RowsAffected(); err == nil && affected > 0 {
			slog.Info("bootstrap_reseller_backfill_done", "table", stmt.label, "rows", affected)
		}
	}
}

// backfillKeyHints 为缺少或格式过旧的 key_hint 回填 sk-xxxx...xxxx。
func backfillKeyHints(db *ent.Client, secret string) {
	ctx := context.Background()
	keys, err := db.APIKey.Query().
		Where(apikey.Or(
			apikey.KeyHint(""),
			apikey.KeyHintHasPrefix("sk-..."),
		)).
		All(ctx)
	if err != nil {
		slog.Warn("bootstrap_keyhint_query_failed", sdk.LogFieldError, err)
		return
	}
	if len(keys) == 0 {
		return
	}

	slog.Info("bootstrap_keyhint_backfill_start", "count", len(keys))
	for _, item := range keys {
		if item.KeyEncrypted == "" {
			continue
		}
		plain, err := auth.DecryptAPIKey(item.KeyEncrypted, secret)
		if err != nil {
			slog.Warn("bootstrap_keyhint_decrypt_failed", sdk.LogFieldAPIKeyID, item.ID, sdk.LogFieldError, err)
			continue
		}
		hint := plain[:7] + "..." + plain[len(plain)-4:]
		if err := db.APIKey.UpdateOneID(item.ID).SetKeyHint(hint).Exec(ctx); err != nil {
			slog.Warn("bootstrap_keyhint_update_failed", sdk.LogFieldAPIKeyID, item.ID, sdk.LogFieldError, err)
		}
	}
	slog.Info("bootstrap_keyhint_backfill_done", "count", len(keys))
}
