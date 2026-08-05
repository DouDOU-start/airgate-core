package bootstrap

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/migrate"
	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/pkg/logx"
)

// Migrate 执行数据库结构迁移：
//  1. 迁移前定点修复清单（幂等 SQL）：会让 ent 自动迁移本身失败的旧结构在此先行处理；
//  2. ent 自动迁移建齐缺失表与字段（非破坏性：不删列不删索引，存量库大变更由生产手动迁移）；
//  3. 存量库定点修复清单（幂等 SQL）：被新 schema 取代且会引发运行时冲突的旧结构在此登记清理。
//  4. 使用应用加密密钥执行敏感数据回填；失败会中止启动，避免静默丢失凭证。
//
// 两份定点 SQL 清单执行失败只 Warn（全新库可能没有旧结构）；敏感数据迁移失败会中断启动。
func Migrate(ctx context.Context, db *ent.Client, sqlDB *sql.DB, encryptionSecret string) error {
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
	return migrateLegacySensitiveData(ctx, sqlDB, encryptionSecret)
}

type legacyAccountCredential struct {
	id        int
	raw       string
	encrypted string
}

type legacyProxyPassword struct {
	id       int
	password string
}

func migrateLegacySensitiveData(ctx context.Context, db *sql.DB, secret string) error {
	if err := migrateLegacyAccountCredentials(ctx, db, secret); err != nil {
		return fmt.Errorf("迁移旧账号凭证失败: %w", err)
	}
	if err := migrateLegacyProxyPasswords(ctx, db, secret); err != nil {
		return fmt.Errorf("迁移旧代理密码失败: %w", err)
	}
	return nil
}

func migrateLegacyAccountCredentials(ctx context.Context, db *sql.DB, secret string) error {
	dataType, hasLegacyColumn, err := columnDataType(ctx, db, "accounts", "credentials")
	if err != nil {
		return err
	}
	_, hasEncryptedColumn, err := columnDataType(ctx, db, "accounts", "credentials_enc")
	if err != nil {
		return err
	}
	if !hasEncryptedColumn {
		return fmt.Errorf("accounts.credentials_enc 字段不存在")
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	querySQL := `
SELECT id, '', COALESCE(credentials_enc, '')
FROM accounts
WHERE credentials_enc <> ''`
	if hasLegacyColumn {
		querySQL = `
SELECT id, COALESCE(credentials::text, ''), COALESCE(credentials_enc, '')
FROM accounts
WHERE credentials IS NOT NULL OR credentials_enc <> ''`
	}
	rows, err := tx.QueryContext(ctx, querySQL)
	if err != nil {
		return err
	}
	items := make([]legacyAccountCredential, 0)
	for rows.Next() {
		var item legacyAccountCredential
		if err := rows.Scan(&item.id, &item.raw, &item.encrypted); err != nil {
			_ = rows.Close()
			return err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	updateSQL := ""
	if hasLegacyColumn {
		emptyValue, err := emptyLegacyCredentialSQL(dataType)
		if err != nil {
			return err
		}
		updateSQL = `UPDATE accounts
SET credentials_enc = $1,
    email = CASE WHEN $2 <> '' THEN $2 ELSE email END,
    credentials = ` + emptyValue + `
WHERE id = $3`
	}
	for _, item := range items {
		encrypted, email, shouldMigrate, err := prepareLegacyAccountCredential(item, secret)
		if err != nil {
			return err
		}
		if !shouldMigrate {
			continue
		}
		if !hasLegacyColumn {
			return fmt.Errorf("账号 %d 缺少旧凭证列却需要迁移", item.id)
		}
		if _, err := tx.ExecContext(ctx, updateSQL, encrypted, email, item.id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func prepareLegacyAccountCredential(item legacyAccountCredential, secret string) (string, string, bool, error) {
	var encryptedCredentials map[string]string
	var encryptedNormalized []byte
	if item.encrypted != "" {
		plain, err := auth.DecryptAPIKey(item.encrypted, secret)
		if err != nil {
			return "", "", false, fmt.Errorf("账号 %d 的现有密文无法解密: %w", item.id, err)
		}
		if err := json.Unmarshal([]byte(plain), &encryptedCredentials); err != nil {
			return "", "", false, fmt.Errorf("账号 %d 的现有密文不是合法凭证 JSON: %w", item.id, err)
		}
		encryptedNormalized, err = json.Marshal(encryptedCredentials)
		if err != nil {
			return "", "", false, fmt.Errorf("序列化账号 %d 的现有密文失败: %w", item.id, err)
		}
	}

	raw := strings.TrimSpace(item.raw)
	if raw == "" || raw == "{}" || raw == "null" {
		return "", "", false, nil
	}
	var credentials map[string]string
	if err := json.Unmarshal([]byte(raw), &credentials); err != nil {
		return "", "", false, fmt.Errorf("账号 %d 的旧凭证不是合法 JSON: %w", item.id, err)
	}
	normalized, err := json.Marshal(credentials)
	if err != nil {
		return "", "", false, fmt.Errorf("序列化账号 %d 的旧凭证失败: %w", item.id, err)
	}
	encrypted := item.encrypted
	if encrypted == "" {
		encrypted, err = auth.EncryptAPIKey(string(normalized), secret)
		if err != nil {
			return "", "", false, err
		}
	} else if !bytes.Equal(normalized, encryptedNormalized) {
		return "", "", false, fmt.Errorf("账号 %d 的旧明文凭证与现有密文不一致", item.id)
	}
	email := credentials["email"]
	if encryptedEmail := encryptedCredentials["email"]; encryptedEmail != "" {
		email = encryptedEmail
	}
	return encrypted, email, true, nil
}

func migrateLegacyProxyPasswords(ctx context.Context, db *sql.DB, secret string) error {
	_, exists, err := columnDataType(ctx, db, "proxies", "password")
	if err != nil || !exists {
		return err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `SELECT id, password FROM proxies WHERE password <> ''`)
	if err != nil {
		return err
	}
	items := make([]legacyProxyPassword, 0)
	for rows.Next() {
		var item legacyProxyPassword
		if err := rows.Scan(&item.id, &item.password); err != nil {
			_ = rows.Close()
			return err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	for _, item := range items {
		if auth.IsEncryptedSecretValue(item.password) {
			if _, err := auth.DecryptSecretValue(item.password, secret); err != nil {
				return fmt.Errorf("代理 %d 的现有密文无法解密: %w", item.id, err)
			}
			continue
		}
		encrypted, err := auth.EncryptSecretValue(item.password, secret)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE proxies SET password = $1 WHERE id = $2`, encrypted, item.id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func columnDataType(ctx context.Context, db *sql.DB, table, column string) (string, bool, error) {
	var dataType string
	err := db.QueryRowContext(ctx, `
SELECT data_type
FROM information_schema.columns
WHERE table_schema = current_schema() AND table_name = $1 AND column_name = $2`, table, column).Scan(&dataType)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return dataType, true, nil
}

func emptyLegacyCredentialSQL(dataType string) (string, error) {
	switch dataType {
	case "jsonb":
		return `'{}'::jsonb`, nil
	case "json":
		return `'{}'::json`, nil
	case "text", "character varying":
		return `''`, nil
	default:
		return "", fmt.Errorf("不支持的旧 credentials 类型: %s", dataType)
	}
}

// preMigrationFixups 在 ent 自动迁移之前执行的定点修复清单（幂等；按时间序追加，勿改历史条目）。
var preMigrationFixups = []string{
	// 2026-08：OAuth 客户端增加 scope 白名单。Ent 的 JSON 默认值只在 Go 创建器生效，
	// 对存量表直接增加 NOT NULL JSON 列会因旧行无值而失败，因此在自动迁移前带默认值补列。
	`ALTER TABLE IF EXISTS oauth_clients
ADD COLUMN IF NOT EXISTS allowed_scopes jsonb NOT NULL
DEFAULT '["profile","wallet.read","wallet.debit","wallet.refund","payment.read","payment.create"]'::jsonb`,

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

	// 2026-07：渠道多 key 独立配置化——把旧 channels 表的 api_keys[] 及随行配置
	// （type/models/mapping/override/priority/weight/并发/RPM/cost_ratio/tags/status/...）
	// 拆成独立的 channel_keys 子实体，每把 key 继承当时渠道配置，并复制分组绑定。
	// 幂等：仅当旧 api_keys 列仍存在、且渠道尚无 channel_key 时回填；回填后把该渠道
	// api_keys 清空，避免管理员日后清空某渠道全部 key 时旧值被再次复活。
	`DO $$
DECLARE c record; k text; new_id int;
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema() AND table_name = 'channels' AND column_name = 'api_keys'
    ) THEN
        FOR c IN SELECT * FROM channels ch WHERE NOT EXISTS (
            SELECT 1 FROM channel_keys ck WHERE ck.channel_keys = ch.id
        ) LOOP
            FOR k IN SELECT jsonb_array_elements_text(c.api_keys::jsonb) LOOP
                INSERT INTO channel_keys (
                    name, type, api_key, models, model_mapping, param_override, header_override,
                    status, error_msg, priority, weight, max_concurrency, max_rpm, cost_ratio,
                    tags, test_model, response_time_ms, tested_at, last_used_at,
                    created_at, updated_at, channel_keys
                ) VALUES (
                    '', c.type, k, c.models, c.model_mapping, c.param_override, c.header_override,
                    c.status, c.error_msg, c.priority, c.weight, c.max_concurrency, c.max_rpm, c.cost_ratio,
                    c.tags, c.test_model, c.response_time_ms, c.tested_at, c.last_used_at,
                    now(), now(), c.id
                ) RETURNING id INTO new_id;
                IF to_regclass('channel_groups') IS NOT NULL THEN
                    INSERT INTO channel_key_groups (channel_key_id, group_id)
                    SELECT new_id, cg.group_id FROM channel_groups cg WHERE cg.channel_id = c.id;
                END IF;
            END LOOP;
            UPDATE channels SET api_keys = '[]'::jsonb WHERE id = c.id;
        END LOOP;
    END IF;
END $$`,

	// 2026-07：渠道多 key 化后，channels 表的配置列全部下沉到 channel_keys（见上一条回填）。
	// 这些遗留列多为 NOT NULL 且无 DB 级默认值（ent 的 .Default() 在 Go 层生效，不落 DB
	// DEFAULT），新的渠道插入（仅 name/base_url）会逐个撞 not-null 约束。逐列去掉 NOT NULL
	// 约束以放行新插入（保留旧列供回滚，不删数据）；全新库无这些列则空转，幂等。
	`DO $$
DECLARE col text;
BEGIN
    FOREACH col IN ARRAY ARRAY[
        'type','api_keys','models','model_mapping','param_override','header_override',
        'status','error_msg','priority','weight','max_concurrency','max_rpm','cost_ratio',
        'tags','test_model','response_time_ms','tested_at','last_used_at'
    ] LOOP
        IF EXISTS (
            SELECT 1 FROM information_schema.columns
            WHERE table_schema = current_schema() AND table_name = 'channels'
              AND column_name = col AND is_nullable = 'NO'
        ) THEN
            EXECUTE format('ALTER TABLE channels ALTER COLUMN %I DROP NOT NULL', col);
        END IF;
    END LOOP;
END $$`,

	// 2026-07：usage_logs 曾有 platform 列（String NOT NULL 且无 DB 级默认值）。渠道多 key 化后
	// platform 维度改由 channel_key_id → channel_keys.type 表达，schema 已删除该字段。但自动迁移
	// 不删列（WithDropColumn(false)），存量库仍保留 platform NOT NULL，新计费记录（不含 platform）
	// 写入即撞 not-null，导致整批 UsageLog 被丢弃。去掉 NOT NULL 放行插入（保留列供回滚，不删数据）；
	// 全新库无此列则空转，幂等。
	`DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema() AND table_name = 'usage_logs'
          AND column_name = 'platform' AND is_nullable = 'NO'
    ) THEN
        ALTER TABLE usage_logs ALTER COLUMN platform DROP NOT NULL;
    END IF;
END $$`,

	// 2026-08：账号凭证改走 credentials_enc（AES-GCM），schema 已无明文 credentials 字段。
	// 自动迁移不删列，存量库若仍留 credentials（jsonb）且 NOT NULL，Create 只写 credentials_enc
	// 会撞 not-null（import-refresh / OAuth 建号均复现）。去掉 NOT NULL、补默认空对象；保留列供回滚。
	`DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema() AND table_name = 'accounts'
          AND column_name = 'credentials'
    ) THEN
        IF EXISTS (
            SELECT 1 FROM information_schema.columns
            WHERE table_schema = current_schema() AND table_name = 'accounts'
              AND column_name = 'credentials' AND is_nullable = 'NO'
        ) THEN
            ALTER TABLE accounts ALTER COLUMN credentials DROP NOT NULL;
        END IF;
        -- 类型可能是 jsonb 或 text；按 data_type 设兼容默认值
        IF EXISTS (
            SELECT 1 FROM information_schema.columns
            WHERE table_schema = current_schema() AND table_name = 'accounts'
              AND column_name = 'credentials' AND data_type = 'jsonb'
        ) THEN
            ALTER TABLE accounts ALTER COLUMN credentials SET DEFAULT '{}'::jsonb;
            UPDATE accounts SET credentials = '{}'::jsonb WHERE credentials IS NULL;
        ELSE
            ALTER TABLE accounts ALTER COLUMN credentials SET DEFAULT '';
            UPDATE accounts SET credentials = '' WHERE credentials IS NULL;
        END IF;
    END IF;
END $$`,
}
