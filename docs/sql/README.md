# DDL 脚本

按 `docs/data-model.md`（28 表）手工维护的生产级 DDL，用于 MySQL / PostgreSQL 部署。

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
- **SQLite（本地开发默认）**：由 GORM AutoMigrate 自动建表（`scheduler/internal/db`），不使用本目录脚本。
- **同步规则**：改 `data-model.md` 表结构 → 同步更新两个 .sql + GORM model；CI 做三口径校验（后续）。
