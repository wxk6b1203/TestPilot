// Package migrate 提供轻量、嵌入式、版本化的 schema 迁移。
//
// 设计目标（详见 docs/ci-migration-plan.md）：
//  1. 不引入外部迁移工具，迁移记录表由 Scheduler 自身维护；
//  2. 兼容存量部署：已有业务表但无 schema_migrations 时，写入基线版本 1
//     （当前 AutoMigrate 产物视为 v1），绝不重跑全量 DDL；
//  3. 新库由 AutoMigrate 建立 v1 基线，再按版本顺序应用后续迁移；
//  4. 后续迁移必须是幂等的（AutoMigrate 作为兜底可能已经做出等价 DDL）。
package migrate

import (
	"fmt"
	"sort"
	"time"

	"gorm.io/gorm"
)

// Migration 一次向前迁移。Version 从 2 开始（1 为 AutoMigrate 基线）。
type Migration struct {
	Version  int
	Name     string
	SQLite   string
	Postgres string
}

// List 按版本升序返回全部迁移。新增迁移时在此追加即可。
func List() []Migration {
	ms := []Migration{
		{
			Version: 2,
			Name:    "create_api_tokens",
			SQLite: `
CREATE TABLE IF NOT EXISTS api_tokens (
	id            INTEGER PRIMARY KEY,
	tenant_id     INTEGER NOT NULL,
	user_id       INTEGER NOT NULL,
	name          TEXT NOT NULL,
	token_hash    TEXT NOT NULL,
	scopes        TEXT NOT NULL DEFAULT '[]',
	expires_at    DATETIME,
	last_used_at  DATETIME
);
CREATE INDEX IF NOT EXISTS idx_api_tokens_tenant ON api_tokens (tenant_id);`,
			Postgres: `
CREATE TABLE IF NOT EXISTS api_tokens (
	id            BIGINT PRIMARY KEY,
	tenant_id     BIGINT NOT NULL REFERENCES tenants (id),
	user_id       BIGINT NOT NULL REFERENCES users (id),
	name          VARCHAR(128) NOT NULL,
	token_hash    VARCHAR(255) NOT NULL,
	scopes        JSONB NOT NULL DEFAULT '[]'::jsonb,
	expires_at    TIMESTAMPTZ,
	last_used_at  TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_api_tokens_tenant ON api_tokens (tenant_id);`,
		},
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].Version < ms[j].Version })
	return ms
}

// Run 应用所有未执行的迁移并返回已执行迁移数。
// baselineModels 仅在新库没有 tenants 表时用于建立 v1 基线。
func Run(d *gorm.DB, baselineModels []any) (int, error) {
	dialect := d.Dialector.Name()
	if err := ensureHistory(d, dialect); err != nil {
		return 0, err
	}
	if err := ensureBaseline(d, dialect, baselineModels); err != nil {
		return 0, err
	}
	applied := map[int]bool{}
	rows, err := d.Raw(`SELECT version FROM schema_migrations`).Rows()
	if err != nil {
		return 0, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return 0, fmt.Errorf("scan schema_migrations: %w", err)
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate schema_migrations: %w", err)
	}

	executed := 0
	for _, m := range List() {
		if applied[m.Version] {
			continue
		}
		sql := m.SQLite
		if dialect == "postgres" {
			sql = m.Postgres
		}
		if sql == "" {
			return executed, fmt.Errorf("migration %d %s has no SQL for dialect %s",
				m.Version, m.Name, dialect)
		}
		if err := d.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec(sql).Error; err != nil {
				return fmt.Errorf("apply migration %d %s: %w", m.Version, m.Name, err)
			}
			return recordVersion(tx, dialect, m.Version, m.Name)
		}); err != nil {
			return executed, err
		}
		executed++
	}
	return executed, nil
}

func ensureHistory(d *gorm.DB, dialect string) error {
	switch dialect {
	case "postgres":
		return d.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
			version BIGINT PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`).Error
	default: // sqlite
		return d.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`).Error
	}
}

// ensureBaseline 建立 v1 基线：存量库只记录版本；新库先 AutoMigrate 全部模型。
func ensureBaseline(d *gorm.DB, dialect string, baselineModels []any) error {
	var cnt int64
	if err := d.Table("schema_migrations").Count(&cnt).Error; err != nil {
		return fmt.Errorf("count schema_migrations: %w", err)
	}
	if cnt > 0 {
		return nil
	}
	hasTenants := d.Migrator().HasTable("tenants")
	if !hasTenants && len(baselineModels) > 0 {
		if err := d.AutoMigrate(baselineModels...); err != nil {
			return fmt.Errorf("baseline automigrate: %w", err)
		}
	}
	return recordVersion(d, dialect, 1, "baseline_automigrate")
}

// recordVersion 幂等写入迁移记录：多实例并发启动时靠 dialect 的冲突忽略语义兜底。
func recordVersion(d *gorm.DB, dialect string, version int, name string) error {
	if dialect == "postgres" {
		return d.Exec(`INSERT INTO schema_migrations (version, name, applied_at)
			VALUES (?, ?, ?) ON CONFLICT (version) DO NOTHING`,
			version, name, time.Now()).Error
	}
	return d.Exec(`INSERT OR IGNORE INTO schema_migrations (version, name, applied_at)
		VALUES (?, ?, ?)`, version, name, time.Now()).Error
}
