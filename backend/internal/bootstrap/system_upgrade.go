package bootstrap

import (
	"context"
	"crypto/sha256"
	stdsql "database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"time"

	entsql "entgo.io/ent/dialect/sql"

	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

const (
	systemUpgradeQualifiedTable        = "public.system_upgrade"
	systemUpgradeAdvisoryLockKey int64 = 2026052809517
)

//go:embed migrations/*.sql
var systemUpgradeFiles embed.FS

type systemUpgrade struct {
	ID          string
	Description string
	Checksum    string
	SQL         string
}

func runSystemUpgrades(drv *entsql.Driver) {
	if drv == nil {
		return
	}
	upgrades := loadSystemUpgrades()
	if len(upgrades) == 0 {
		return
	}

	ctx := context.Background()
	conn, err := drv.DB().Conn(ctx)
	if err != nil {
		panicSystemUpgrade("open system upgrade connection", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, systemUpgradeAdvisoryLockKey); err != nil {
		panicSystemUpgrade("lock system upgrades", err)
	}
	defer func() {
		if _, err := conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, systemUpgradeAdvisoryLockKey); err != nil {
			slog.Warn("system_upgrade_unlock_failed", sdk.LogFieldError, err)
		}
	}()

	if err := prepareSystemUpgradeTable(ctx, conn); err != nil {
		panicSystemUpgrade("prepare system_upgrade table", err)
	}

	for _, upgrade := range upgrades {
		var appliedChecksum stdsql.NullString
		const appliedSQL = `SELECT checksum FROM public.system_upgrade WHERE id = $1`
		err := conn.QueryRowContext(ctx, appliedSQL, upgrade.ID).Scan(&appliedChecksum)
		if err == nil {
			if appliedChecksum.Valid && appliedChecksum.String != "" && appliedChecksum.String != upgrade.Checksum {
				panicSystemUpgrade("verify system upgrade checksum "+upgrade.ID, fmt.Errorf("recorded=%s current=%s", appliedChecksum.String, upgrade.Checksum))
			}
			if !appliedChecksum.Valid || appliedChecksum.String == "" {
				const updateSQL = `UPDATE public.system_upgrade
					SET checksum = $2, description = $3
					WHERE id = $1 AND checksum = ''`
				if _, err := conn.ExecContext(ctx, updateSQL, upgrade.ID, upgrade.Checksum, upgrade.Description); err != nil {
					panicSystemUpgrade("backfill system upgrade checksum "+upgrade.ID, err)
				}
			}
			continue
		}
		if err != stdsql.ErrNoRows {
			panicSystemUpgrade("check system upgrade "+upgrade.ID, err)
		}

		start := time.Now()
		slog.Info("system_upgrade_start", "id", upgrade.ID)
		if err := executeSystemUpgradeSQL(ctx, conn, upgrade); err != nil {
			panicSystemUpgrade("run system upgrade "+upgrade.ID, err)
		}
		duration := time.Since(start).Milliseconds()
		const insertSQL = `INSERT INTO public.system_upgrade (id, description, checksum, duration_ms)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (id) DO NOTHING`
		if _, err := conn.ExecContext(ctx, insertSQL, upgrade.ID, upgrade.Description, upgrade.Checksum, duration); err != nil {
			panicSystemUpgrade("record system upgrade "+upgrade.ID, err)
		}
		slog.Info("system_upgrade_done", "id", upgrade.ID, "duration_ms", duration)
	}
}

func prepareSystemUpgradeTable(ctx context.Context, conn *stdsql.Conn) error {
	const createTableSQL = `CREATE TABLE IF NOT EXISTS public.system_upgrade (
		id text PRIMARY KEY,
		description text NOT NULL DEFAULT '',
		checksum text NOT NULL DEFAULT '',
		applied_at timestamptz NOT NULL DEFAULT now(),
		duration_ms bigint NOT NULL DEFAULT 0
	)`
	if _, err := conn.ExecContext(ctx, createTableSQL); err != nil {
		return fmt.Errorf("create %s table: %w", systemUpgradeQualifiedTable, err)
	}
	if err := ensureSystemUpgradeColumns(ctx, conn, systemUpgradeQualifiedTable); err != nil {
		return err
	}
	if err := normalizeSystemUpgradePrimaryKey(ctx, conn); err != nil {
		return err
	}

	return nil
}

func ensureSystemUpgradeColumns(ctx context.Context, conn *stdsql.Conn, table string) error {
	statements := []string{
		"ALTER TABLE " + table + " ADD COLUMN IF NOT EXISTS id text",
		"ALTER TABLE " + table + " ADD COLUMN IF NOT EXISTS description text",
		"ALTER TABLE " + table + " ADD COLUMN IF NOT EXISTS checksum text",
		"ALTER TABLE " + table + " ADD COLUMN IF NOT EXISTS applied_at timestamptz",
		"ALTER TABLE " + table + " ADD COLUMN IF NOT EXISTS duration_ms bigint",
		"UPDATE " + table + " SET description = '' WHERE description IS NULL",
		"UPDATE " + table + " SET checksum = '' WHERE checksum IS NULL",
		"UPDATE " + table + " SET applied_at = now() WHERE applied_at IS NULL",
		"UPDATE " + table + " SET duration_ms = 0 WHERE duration_ms IS NULL",
		"ALTER TABLE " + table + " ALTER COLUMN description SET DEFAULT ''",
		"ALTER TABLE " + table + " ALTER COLUMN description SET NOT NULL",
		"ALTER TABLE " + table + " ALTER COLUMN checksum SET DEFAULT ''",
		"ALTER TABLE " + table + " ALTER COLUMN checksum SET NOT NULL",
		"ALTER TABLE " + table + " ALTER COLUMN applied_at SET DEFAULT now()",
		"ALTER TABLE " + table + " ALTER COLUMN applied_at SET NOT NULL",
		"ALTER TABLE " + table + " ALTER COLUMN duration_ms SET DEFAULT 0",
		"ALTER TABLE " + table + " ALTER COLUMN duration_ms SET NOT NULL",
	}
	for _, statement := range statements {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("ensure %s columns: %w", table, err)
		}
	}
	return nil
}

func normalizeSystemUpgradePrimaryKey(ctx context.Context, conn *stdsql.Conn) error {
	const ensurePrimaryKeySQL = `DO $$
	BEGIN
		IF NOT EXISTS (
			SELECT 1
			FROM pg_constraint c
			JOIN pg_class t ON t.oid = c.conrelid
			JOIN pg_namespace n ON n.oid = t.relnamespace
			WHERE n.nspname = 'public'
				AND t.relname = 'system_upgrade'
				AND c.contype = 'p'
		) THEN
			ALTER TABLE public.system_upgrade ADD CONSTRAINT system_upgrade_pkey PRIMARY KEY (id);
		END IF;
	END $$`
	if _, err := conn.ExecContext(ctx, ensurePrimaryKeySQL); err != nil {
		return fmt.Errorf("ensure %s primary key: %w", systemUpgradeQualifiedTable, err)
	}
	return nil
}

func loadSystemUpgrades() []systemUpgrade {
	entries, err := fs.ReadDir(systemUpgradeFiles, "migrations")
	if err != nil {
		panicSystemUpgrade("read system upgrade files", err)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	upgrades := make([]systemUpgrade, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		if err := validateSystemUpgradeFilename(entry.Name()); err != nil {
			panicSystemUpgrade("validate system upgrade "+entry.Name(), err)
		}
		path := filepath.ToSlash(filepath.Join("migrations", entry.Name()))
		data, err := systemUpgradeFiles.ReadFile(path)
		if err != nil {
			panicSystemUpgrade("read system upgrade "+entry.Name(), err)
		}
		hash := sha256.Sum256(data)
		id := strings.TrimSuffix(entry.Name(), ".sql")
		sql := string(data)
		upgrades = append(upgrades, systemUpgrade{
			ID:          id,
			Description: systemUpgradeDescription(sql, id),
			Checksum:    hex.EncodeToString(hash[:]),
			SQL:         sql,
		})
	}
	return upgrades
}

func validateSystemUpgradeFilename(name string) error {
	id := strings.TrimSuffix(name, ".sql")
	if len(id) < len("20060102150405_")+1 || id[14] != '_' {
		return fmt.Errorf("must use YYYYMMDDHHMMSS_description.sql")
	}
	if _, err := time.Parse("20060102150405", id[:14]); err != nil {
		return fmt.Errorf("invalid timestamp prefix: %w", err)
	}
	if strings.TrimSpace(id[15:]) == "" {
		return fmt.Errorf("missing description after timestamp")
	}
	return nil
}

func systemUpgradeDescription(sql, fallback string) string {
	for _, line := range strings.Split(sql, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "-- description:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "-- description:"))
		}
	}
	return fallback
}

func executeSystemUpgradeSQL(ctx context.Context, conn *stdsql.Conn, upgrade systemUpgrade) error {
	for _, stmt := range splitSQLStatements(upgrade.SQL) {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("execute statement in %s: %w", upgrade.ID, err)
		}
	}
	return nil
}

func splitSQLStatements(sql string) []string {
	var statements []string
	start := 0
	inSingleQuote := false
	inDoubleQuote := false
	inLineComment := false
	inBlockComment := false
	dollarTag := ""

	for i := 0; i < len(sql); i++ {
		ch := sql[i]
		next := byte(0)
		if i+1 < len(sql) {
			next = sql[i+1]
		}

		switch {
		case inLineComment:
			if ch == '\n' {
				inLineComment = false
			}
			continue
		case inBlockComment:
			if ch == '*' && next == '/' {
				inBlockComment = false
				i++
			}
			continue
		case dollarTag != "":
			if strings.HasPrefix(sql[i:], dollarTag) {
				i += len(dollarTag) - 1
				dollarTag = ""
			}
			continue
		case inSingleQuote:
			if ch == '\'' {
				if next == '\'' {
					i++
				} else {
					inSingleQuote = false
				}
			}
			continue
		case inDoubleQuote:
			if ch == '"' {
				if next == '"' {
					i++
				} else {
					inDoubleQuote = false
				}
			}
			continue
		}

		if ch == '-' && next == '-' {
			inLineComment = true
			i++
			continue
		}
		if ch == '/' && next == '*' {
			inBlockComment = true
			i++
			continue
		}
		if ch == '\'' {
			inSingleQuote = true
			continue
		}
		if ch == '"' {
			inDoubleQuote = true
			continue
		}
		if ch == '$' {
			if tag, ok := sqlDollarTag(sql[i:]); ok {
				dollarTag = tag
				i += len(tag) - 1
				continue
			}
		}
		if ch == ';' {
			stmt := strings.TrimSpace(sql[start:i])
			if stmt != "" {
				statements = append(statements, stmt)
			}
			start = i + 1
		}
	}

	tail := strings.TrimSpace(sql[start:])
	if tail != "" {
		statements = append(statements, tail)
	}
	return statements
}

func sqlDollarTag(sql string) (string, bool) {
	if sql == "" || sql[0] != '$' {
		return "", false
	}
	for i := 1; i < len(sql); i++ {
		ch := sql[i]
		if ch == '$' {
			return sql[:i+1], true
		}
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_') {
			return "", false
		}
	}
	return "", false
}

func panicSystemUpgrade(action string, err error) {
	if err != nil {
		err = fmt.Errorf("%s: %w", action, err)
	} else {
		err = fmt.Errorf("%s", action)
	}
	slog.Error("system_upgrade_failed", sdk.LogFieldError, err)
	panic(err)
}
