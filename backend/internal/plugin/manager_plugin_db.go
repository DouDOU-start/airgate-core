package plugin

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"

	_ "github.com/lib/pq"

	"github.com/DouDOU-start/airgate-core/ent"
	settingent "github.com/DouDOU-start/airgate-core/ent/setting"
)

// manager_plugin_db.go：为每个插件 provision 独立 schema + 受限 postgres role
//（详见 ADR-0001 Decision 5）。
//
// 设计要点：
//   - 每个插件一个 schema：plugin_<plugin_id>，名字带前缀避开 core 表 (public schema)
//   - 每个插件一个 role：plugin_<plugin_id>_role，密码随机生成存 settings 表
//   - role 只授权访问自己的 schema：USAGE + CREATE on schema, ALL on tables in schema
//   - 显式 REVOKE ALL ON SCHEMA public：防止插件越权读 core 业务表
//   - 注入到插件的 plugin_dsn 含 search_path=plugin_<id>，所有 SQL 默认查自己的 schema
//
// 当前实现是非破坏性的：core 仍然继续注入 db_dsn（admin DSN），新增的 plugin_dsn 是
// 旁路。插件可以选择性地从 db_dsn 迁移到 plugin_dsn，迁移完成后再下线 db_dsn。

const (
	// pluginSchemaPrefix 插件 schema 名前缀，防止与 core 表冲突。
	pluginSchemaPrefix = "plugin_"
	// pluginRoleSuffix 插件 role 名后缀。
	pluginRoleSuffix = "_role"
	// pluginRolePasswordSettingPrefix settings 表里存插件 role 密码的 key 前缀。
	pluginRolePasswordSettingPrefix = "plugin_db_role_pw__"
	pluginRolePasswordSettingGroup  = "plugin_db"
)

// pluginDSNProvisioner 给插件创建并维护独立 DB 资源（schema + role + DSN）。
//
// 由 Manager 持有，spawn 插件之前调 EnsureFor 拿到 plugin_dsn 字符串。
type pluginDSNProvisioner struct {
	db *ent.Client // ent 客户端，仅用于读写 settings 表存 role 密码

	// adminDSN 是 core 启动时使用的高权限 DSN，用于执行 CREATE SCHEMA / CREATE ROLE。
	adminDSN string

	// adminFields 缓存 adminDSN 解析出的字段，构造 plugin DSN 时复用 host/port/dbname。
	adminFields dsnFields
}

// dsnFields 解析后的 PostgreSQL DSN 字段（key=value 空格分隔的格式）。
type dsnFields map[string]string

func parseDSNFields(dsn string) dsnFields {
	out := dsnFields{}
	for _, kv := range strings.Fields(dsn) {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		out[kv[:eq]] = kv[eq+1:]
	}
	return out
}

// newPluginDSNProvisioner 构造一个 provisioner。adminDSN 通常是 core 启动时的 cfg.Database.DSN()。
func newPluginDSNProvisioner(db *ent.Client, adminDSN string) *pluginDSNProvisioner {
	return &pluginDSNProvisioner{
		db:          db,
		adminDSN:    adminDSN,
		adminFields: parseDSNFields(adminDSN),
	}
}

// EnsureFor 为指定插件确保资源就绪并返回它专用的 DSN。
//
// 步骤（幂等）：
//  1. 创建 schema plugin_<id>（IF NOT EXISTS）
//  2. 创建 role plugin_<id>_role（IF NOT EXISTS），随机密码存 settings
//  3. 授权 role 在 schema 内的 USAGE / CREATE / table 全部权限
//  4. 显式 REVOKE 该 role 对 public schema 的所有访问，防止越权
//  5. 拼装并返回新 DSN：user=role + password=随机 + search_path=schema
//
// 任何步骤失败都返回 error；调用方决定是否仍以 admin DSN 启动插件。
func (p *pluginDSNProvisioner) EnsureFor(ctx context.Context, pluginID string) (string, error) {
	if !isValidPluginID(pluginID) {
		return "", fmt.Errorf("插件 ID %q 不合法（必须是字母数字_，长度 1-48）", pluginID)
	}

	schemaName := pluginSchemaPrefix + pluginID
	roleName := pluginSchemaPrefix + pluginID + pluginRoleSuffix

	conn, err := sql.Open("postgres", p.adminDSN)
	if err != nil {
		return "", fmt.Errorf("打开 admin DB 失败: %w", err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.PingContext(ctx); err != nil {
		return "", fmt.Errorf("admin DB 不可达: %w", err)
	}

	password, err := p.loadOrCreatePassword(ctx, pluginID)
	if err != nil {
		return "", err
	}

	// 1. CREATE SCHEMA
	if _, err := conn.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS "+quoteIdent(schemaName)); err != nil {
		return "", fmt.Errorf("创建 schema 失败: %w", err)
	}

	// 2. CREATE ROLE（用 DO $$ ... $$ 块做 IF NOT EXISTS，避免 ROLE 已存在时报错）
	createRoleSQL := fmt.Sprintf(`
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = %s) THEN
        CREATE ROLE %s WITH LOGIN PASSWORD %s;
    ELSE
        ALTER ROLE %s WITH LOGIN PASSWORD %s;
    END IF;
END $$;`,
		quoteString(roleName),
		quoteIdent(roleName),
		quoteString(password),
		quoteIdent(roleName),
		quoteString(password),
	)
	if _, err := conn.ExecContext(ctx, createRoleSQL); err != nil {
		return "", fmt.Errorf("创建/更新 role 失败: %w", err)
	}

	// 3. 授权 role 访问自己的 schema（schema 级 USAGE+CREATE，table 级 ALL）
	grants := []string{
		fmt.Sprintf("GRANT USAGE, CREATE ON SCHEMA %s TO %s", quoteIdent(schemaName), quoteIdent(roleName)),
		fmt.Sprintf("GRANT ALL ON ALL TABLES IN SCHEMA %s TO %s", quoteIdent(schemaName), quoteIdent(roleName)),
		fmt.Sprintf("GRANT ALL ON ALL SEQUENCES IN SCHEMA %s TO %s", quoteIdent(schemaName), quoteIdent(roleName)),
		// future-proof：未来在 schema 中新建的表也自动授权
		fmt.Sprintf("ALTER DEFAULT PRIVILEGES IN SCHEMA %s GRANT ALL ON TABLES TO %s", quoteIdent(schemaName), quoteIdent(roleName)),
		fmt.Sprintf("ALTER DEFAULT PRIVILEGES IN SCHEMA %s GRANT ALL ON SEQUENCES TO %s", quoteIdent(schemaName), quoteIdent(roleName)),
	}
	for _, sqlStmt := range grants {
		if _, err := conn.ExecContext(ctx, sqlStmt); err != nil {
			return "", fmt.Errorf("授权 role 失败 (%s): %w", sqlStmt, err)
		}
	}

	// 4. REVOKE public schema 上一切权限，确保插件无法读 core 业务表
	revokes := []string{
		fmt.Sprintf("REVOKE ALL ON ALL TABLES IN SCHEMA public FROM %s", quoteIdent(roleName)),
		fmt.Sprintf("REVOKE ALL ON SCHEMA public FROM %s", quoteIdent(roleName)),
	}
	for _, sqlStmt := range revokes {
		if _, err := conn.ExecContext(ctx, sqlStmt); err != nil {
			// REVOKE 在某些 PG 版本对从未 GRANT 过的对象会 noop，不应该 fail
			slog.Debug("REVOKE 警告（通常可忽略）", "sql", sqlStmt, "error", err)
		}
	}

	// 5. 拼 DSN：保留 host/port/dbname，替换 user/password，注入 search_path
	dsn := p.buildPluginDSN(roleName, password, schemaName)
	return dsn, nil
}

// buildPluginDSN 从 adminFields 复制 host/port/dbname，替换为插件 role + password +
// 注入 search_path/options 让插件 SQL 默认查自己的 schema。
func (p *pluginDSNProvisioner) buildPluginDSN(role, password, schema string) string {
	out := make(dsnFields, len(p.adminFields)+3)
	for k, v := range p.adminFields {
		out[k] = v
	}
	out["user"] = role
	out["password"] = password
	// search_path 让 CREATE TABLE foo / SELECT * FROM foo 默认走 plugin schema
	// 注意 lib/pq 不直接支持 search_path 参数，需要通过 options=-c search_path=...
	out["options"] = "-c search_path=" + schema

	parts := make([]string, 0, len(out))
	for k, v := range out {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, " ")
}

// loadOrCreatePassword 从 settings 表读 role 密码；不存在则生成 24 字节 hex 写入。
//
// 用 settings 表持久化是为了让插件重启时拿到同一个密码（不需要每次 ALTER ROLE）。
// 密码本身不加密——它是 plugin role 的密码，泄漏只能访问那个插件自己的 schema。
func (p *pluginDSNProvisioner) loadOrCreatePassword(ctx context.Context, pluginID string) (string, error) {
	if p.db == nil {
		return "", fmt.Errorf("ent client 未注入，无法持久化插件 role 密码")
	}
	settingKey := pluginRolePasswordSettingPrefix + pluginID
	row, err := p.db.Setting.Query().
		Where(settingent.KeyEQ(settingKey), settingent.GroupEQ(pluginRolePasswordSettingGroup)).
		Only(ctx)
	if err == nil && row.Value != "" {
		return row.Value, nil
	}
	if err != nil && !ent.IsNotFound(err) {
		return "", fmt.Errorf("读取 role 密码 setting 失败: %w", err)
	}

	// 生成新密码：24 字节随机 → 48 字符 hex
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成随机密码失败: %w", err)
	}
	password := hex.EncodeToString(buf)

	// upsert
	if row != nil {
		_, err = p.db.Setting.UpdateOne(row).SetValue(password).Save(ctx)
	} else {
		_, err = p.db.Setting.Create().
			SetKey(settingKey).
			SetValue(password).
			SetGroup(pluginRolePasswordSettingGroup).
			Save(ctx)
	}
	if err != nil {
		return "", fmt.Errorf("写入 role 密码 setting 失败: %w", err)
	}
	return password, nil
}

// ============================================================================
// 工具函数
// ============================================================================

// isValidPluginID 校验插件 ID 只含字母数字下划线连字符且长度合理。
//
// 严格校验是为了防止 SQL 注入（quoteIdent 也会防御，但宁可双保险），
// 同时把 schema/role 名字限制在可读范围。连字符在 PG 标识符里需要引号，
// quoteIdent 会自动加；这里允许是为了与 plugin_id 命名习惯（如 airgate-health）一致。
func isValidPluginID(id string) bool {
	if id == "" || len(id) > 48 {
		return false
	}
	for _, r := range id {
		ok := (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '_' || r == '-'
		if !ok {
			return false
		}
	}
	return true
}

// quoteIdent 把字符串包成 PostgreSQL 引用标识符（双引号 + 内部引号转义）。
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// quoteString 把字符串包成 PostgreSQL 字符串字面量（单引号 + 内部引号转义）。
func quoteString(s string) string {
	return `'` + strings.ReplaceAll(s, `'`, `''`) + `'`
}
