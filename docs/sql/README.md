# DDL 脚本

按 `docs/data-model.md`（33 表：GORM AutoMigrate 32 张 + api_tokens 1 张）手工维护的生产级 DDL，用于 MySQL / PostgreSQL 部署。Scheduler 运行时迁移见 `docs/ci-migration-plan.md`（api_tokens 由 v2 迁移创建）。

| 文件 | 目标库 | 说明 |
|---|---|---|
| `postgresql.sql` | PostgreSQL 13+ | JSONB / TIMESTAMPTZ / 部分索引（软删行不进索引） |
| `mysql.sql` | MySQL 8.0+ InnoDB | JSON / DATETIME(3) / utf8mb4；无部分索引 |

## 使用

```bash
# PostgreSQL
psql -d testpilot -f docs/sql/postgresql.sql

# MySQL
mysql -D testpilot < docs/sql/mysql.sql
```

## 约定

- **主键**：应用层 snowflake BIGINT（`scheduler/internal/model.NextID()`），DDL 不含自增/序列。
- **文档列**（JSONB/JSON）：内容为 `proto/testpilot/common/v1/types.proto` 的 JSON 表示，proto 为单一事实源。
- **软删**：`deleted_at IS NULL` 为存活；PG 用部分索引排除已删行，MySQL 用普通索引。
- **SQLite（本地开发默认）**：由 GORM AutoMigrate 建基线（`scheduler/internal/db`），后续增量走 `scheduler/internal/migrate`。
- **同步规则**：改 `data-model.md` 表结构 → 同步更新两个 .sql + GORM model + 新增一条版本化迁移（约定见 `docs/ci-migration-plan.md`）。
