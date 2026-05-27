package bootstrap

import (
	"context"
	"log/slog"
	"strings"

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
	migrateAccountState(drv)
	migrateUserHistoryRefs(drv)
	dropUsageLogLegacyDetailColumns(drv)
	slog.Info("bootstrap_startup_tasks_done")
}

func dropUsageLogLegacyDetailColumns(drv *entsql.Driver) {
	if drv == nil {
		return
	}
	ctx := context.Background()
	statements := []string{
		`ALTER TABLE usage_logs DROP COLUMN IF EXISTS image_size`,
		`ALTER TABLE usage_logs DROP COLUMN IF EXISTS cache_creation_5m_tokens`,
		`ALTER TABLE usage_logs DROP COLUMN IF EXISTS cache_creation_1h_tokens`,
		`ALTER TABLE usage_logs DROP COLUMN IF EXISTS cache_creation_1h_price`,
		`ALTER TABLE usage_logs DROP COLUMN IF EXISTS usage_attributes`,
		`ALTER TABLE usage_logs DROP COLUMN IF EXISTS usage_metrics`,
		`ALTER TABLE usage_logs DROP COLUMN IF EXISTS usage_cost_details`,
	}
	for _, sql := range statements {
		var r entsql.Result
		if err := drv.Exec(ctx, sql, []any{}, &r); err != nil {
			slog.Warn("bootstrap_usage_log_legacy_column_drop_failed", "sql", sql, sdk.LogFieldError, err)
			return
		}
	}
	slog.Info("bootstrap_usage_log_legacy_columns_dropped")
}

// migrateUserHistoryRefs 允许硬删除用户，同时保留历史使用记录和余额流水。
// 用量/计费聚合依赖 usage_logs 的成本快照字段；这里把历史表的 user 外键改为 SET NULL，
// 并回填 user_id/user_email 快照，避免删除用户后历史记录丢失归属信息。
func migrateUserHistoryRefs(drv *entsql.Driver) {
	if drv == nil {
		return
	}
	ctx := context.Background()
	statements := []string{
		`ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS user_id_snapshot integer NOT NULL DEFAULT 0`,
		`ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS user_email_snapshot text NOT NULL DEFAULT ''`,
		`UPDATE usage_logs AS ul
			SET user_id_snapshot = CASE WHEN ul.user_id_snapshot = 0 THEN u.id ELSE ul.user_id_snapshot END,
				user_email_snapshot = CASE WHEN ul.user_email_snapshot = '' THEN u.email ELSE ul.user_email_snapshot END
			FROM users AS u
			WHERE ul.user_usage_logs = u.id`,
		`ALTER TABLE usage_logs ALTER COLUMN user_usage_logs DROP NOT NULL`,
		`ALTER TABLE usage_logs DROP CONSTRAINT IF EXISTS usage_logs_users_usage_logs`,
		`ALTER TABLE usage_logs ADD CONSTRAINT usage_logs_users_usage_logs
			FOREIGN KEY (user_usage_logs) REFERENCES users(id) ON DELETE SET NULL`,
		`ALTER TABLE balance_logs ADD COLUMN IF NOT EXISTS user_id_snapshot integer NOT NULL DEFAULT 0`,
		`ALTER TABLE balance_logs ADD COLUMN IF NOT EXISTS user_email_snapshot text NOT NULL DEFAULT ''`,
		`UPDATE balance_logs AS bl
			SET user_id_snapshot = CASE WHEN bl.user_id_snapshot = 0 THEN u.id ELSE bl.user_id_snapshot END,
				user_email_snapshot = CASE WHEN bl.user_email_snapshot = '' THEN u.email ELSE bl.user_email_snapshot END
			FROM users AS u
			WHERE bl.user_balance_logs = u.id`,
		`ALTER TABLE balance_logs ALTER COLUMN user_balance_logs DROP NOT NULL`,
		`ALTER TABLE balance_logs DROP CONSTRAINT IF EXISTS balance_logs_users_balance_logs`,
		`ALTER TABLE balance_logs ADD CONSTRAINT balance_logs_users_balance_logs
			FOREIGN KEY (user_balance_logs) REFERENCES users(id) ON DELETE SET NULL`,
	}

	for _, sql := range statements {
		var r entsql.Result
		if err := drv.Exec(ctx, sql, []any{}, &r); err != nil {
			slog.Warn("bootstrap_user_history_refs_migration_failed", "sql", sql, sdk.LogFieldError, err)
			return
		}
	}
}

// migrateAccountState 把老的 status / rate_limit_reset_at 字段一次性迁移到新的
// state / state_until，然后 DROP 旧列。幂等：首次启动或升级时有效，之后旧列已不存在时跳过。
//
// 映射规则：
//
//	status='error'    → state='disabled'
//	status='disabled' → state='disabled'
//	rate_limit_reset_at > now() → state='rate_limited', state_until = rate_limit_reset_at
//	其它               → state='active'（ent 默认值，不需要改）
func migrateAccountState(drv *entsql.Driver) {
	if drv == nil {
		return
	}
	ctx := context.Background()

	hasStatus, ok := accountColumnExists(ctx, drv, "status")
	if !ok {
		return
	}
	hasRateLimitResetAt, ok := accountColumnExists(ctx, drv, "rate_limit_reset_at")
	if !ok {
		return
	}
	if !hasStatus && !hasRateLimitResetAt {
		return
	}

	slog.Info("bootstrap_account_state_migration_start")

	updates := make([]string, 0, 2)
	if hasStatus || hasRateLimitResetAt {
		stateCase := []string{"state = CASE"}
		if hasStatus {
			stateCase = append(stateCase, "WHEN status IN ('error', 'disabled') THEN 'disabled'")
		}
		if hasRateLimitResetAt {
			stateCase = append(stateCase, "WHEN rate_limit_reset_at IS NOT NULL AND rate_limit_reset_at > NOW() THEN 'rate_limited'")
		}
		stateCase = append(stateCase, "ELSE 'active' END")
		updates = append(updates, strings.Join(stateCase, " "))
	}
	if hasRateLimitResetAt {
		updates = append(updates, `state_until = CASE
			WHEN rate_limit_reset_at IS NOT NULL AND rate_limit_reset_at > NOW() THEN rate_limit_reset_at
			ELSE NULL
		END`)
	}

	var res entsql.Result
	updateSQL := "UPDATE accounts SET " + strings.Join(updates, ", ")
	if err := drv.Exec(ctx, updateSQL, []any{}, &res); err != nil {
		slog.Error("bootstrap_account_state_migration_failed", sdk.LogFieldError, err)
		return
	}
	if affected, err := res.RowsAffected(); err == nil {
		slog.Info("bootstrap_account_state_migration_done", "rows", affected)
	}

	// 然后删旧列。WithDropColumn(false) 让 ent 不自动删，所以手工 DROP。
	drops := []string{
		`ALTER TABLE accounts DROP COLUMN IF EXISTS status`,
		`ALTER TABLE accounts DROP COLUMN IF EXISTS rate_limit_reset_at`,
	}
	for _, sql := range drops {
		var r entsql.Result
		if err := drv.Exec(ctx, sql, []any{}, &r); err != nil {
			slog.Warn("bootstrap_drop_legacy_column_failed", "sql", sql, sdk.LogFieldError, err)
		}
	}
	slog.Info("bootstrap_account_legacy_columns_dropped")
}

func accountColumnExists(ctx context.Context, drv *entsql.Driver, column string) (bool, bool) {
	var exists entsql.Rows
	const checkSQL = `SELECT 1 FROM information_schema.columns
		WHERE table_name='accounts' AND column_name=$1 LIMIT 1`
	if err := drv.Query(ctx, checkSQL, []any{column}, &exists); err != nil {
		slog.Warn("bootstrap_account_state_check_failed", "column", column, sdk.LogFieldError, err)
		return false, false
	}
	defer func() { _ = exists.Close() }()
	return exists.Next(), true
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
