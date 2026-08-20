# CI/CD、Proto 治理、JUnit/Webhook 与迁移版本化设计

> 本文档是这三块工程化能力的完整设计记录：为什么这么做、方案取舍、实现位置、使用方式与后续约定。

## 目录

1. 目标与非目标
2. 迁移版本化
3. JUnit XML 报告
4. Webhook 与 CI 集成流程
5. Proto 治理自动化
6. GitHub Actions CI/CD
7. 后续开发约定

---

## 1. 目标与非目标

### 目标

- Scheduler schema 具备版本化迁移记录，存量库可安全升级、新库可从头构建；
- 任何 CI 都能触发一次计划运行、拉取 JUnit XML 报告、按测试结果决定流水线成功/失败；
- `run_finished` webhook 提供 JUnit 报告地址，CI 可以事件驱动而非轮询；
- proto 作为单一契约有 lint、breaking-change 与生成产物复现性检查；
- 仓库有可执行的 CI（测试/构建）与 CD（tag 触发镜像发布）骨架。

### 非目标

- 不引入 golang-migrate 等外部迁移运行时（见 §2 取舍）；
- 不在本期实现 API Token / CLI 二进制（仍为另议项，但迁移已预建 `api_tokens` 表）；
- 不改动现有 REST/前端业务语义；
- 不把 buf 生成代码接入构建（继续用 protoc，buf 只做 lint/breaking）。

---

## 2. 迁移版本化

### 2.1 设计

采用**嵌入式 Go 迁移注册表 + 基线版本**：

- `schema_migrations(version, name, applied_at)` 为迁移记录表；
- `version=1` 定义为当前 `model.AllModels()` AutoMigrate 产物基线；
- 后续迁移从 `version=2` 开始，在 `scheduler/internal/migrate/migrate.go` 注册；
- 每个迁移按 dialect 提供 SQLite / PostgreSQL 两版 SQL；
- Scheduler 启动时先跑迁移，再执行 AutoMigrate 作为模型级兜底。

### 2.2 存量/新库兼容

| 场景 | 行为 |
|---|---|
| 新 SQLite/PG 库（无 `tenants` 表） | AutoMigrate 全部模型 → 写 v1 基线 → 顺序应用 v2… |
| 存量库（有业务表、无 `schema_migrations`） | 不重跑 DDL，只写 v1 基线 → 顺序应用缺失迁移 |
| 已有 `schema_migrations` | 按版本差补跑，已应用版本跳过（幂等） |

`db.Open()` 的实现顺序：

1. 打开连接并配置连接池；
2. `migrate.Run(d, model.AllModels())`；
3. `d.AutoMigrate(model.AllModels()...)`（GORM 模型级兜底，不删列）；
4. seed 默认租户/admin。

### 2.3 首个真实迁移

- **v2 `create_api_tokens`**：落地 data-model 中长期预留的 `api_tokens` 表
  （id/tenant_id/user_id/name/token_hash/scopes/expires_at/last_used_at + 索引）。
- SQLite 使用 `INTEGER/TEXT/DATETIME`，PG 使用 `BIGINT/JSONB/TIMESTAMPTZ` + FK。
- 表结构只由迁移维护，暂不加入 GORM model，避免 AutoMigrate 抢跑。

### 2.4 后续约定

新增字段/表时：

1. 在 `migrate.List()` 追加下一个版本号，SQL 必须**幂等**（`IF NOT EXISTS` /
   先 `HasColumn` 判断），因为 AutoMigrate 可能已做出等价 DDL；
2. 同步更新 `model`、`docs/data-model.md`、`docs/sql/postgresql.sql`、
   `docs/sql/mysql.sql`；
3. 在 `migrate_test.go` 补“新库执行”和“存量库补跑”两个用例。

### 2.5 测试

- `scheduler/internal/migrate/migrate_test.go`：新库、存量库、无模型、幂等、时间戳。

---

## 3. JUnit XML 报告

### 3.1 API

```
GET /api/v1/runs/:id/junit
Authorization: Bearer <JWT>
```

- viewer 即可访问，按 tenant_id 隔离；
- 响应 `Content-Type: application/xml`，`Content-Disposition: attachment`。

### 3.2 映射关系

| TestPilot | JUnit |
|---|---|
| TestRun | `<testsuite name="testpilot run {id} (plan {plan_id})">` |
| TestCaseResult | `<testcase classname="TestPilot.case.{case_id}" name="{case name}">` |
| CASE_STATUS_PASSED | 无子节点 |
| CASE_STATUS_FAILED | `<failure>`，body 含 `case_result.error` + 失败步骤 path/logs 摘要 |
| CASE_STATUS_SKIPPED | `<skipped/>` |
| RUNNING / 未知 | `<error>`（不会把未完成用例当通过） |
| duration_ms | `time` 属性（秒，3 位小数） |

### 3.3 实现位置

- 纯渲染函数：`scheduler/internal/httpserver/junit.go`（`renderJUnit`）
- handler：`scheduler/internal/httpserver/junit.go`（`runJUnit`）
- 路由：`scheduler/internal/httpserver/server.go`

---

## 4. Webhook 与 CI 集成流程

### 4.1 现状补齐

`notify.RunFinished` 的通用 webhook payload 增加：

```json
{
  "event": "run_finished",
  "run_id": "…",
  "plan_id": "…",
  "status": 2,
  "summary": {"total": 1, "passed": 1, "failed": 0, "skipped": 0},
  "triggered_by": "ci",
  "finished_at": "…",
  "junit_url": "/api/v1/runs/<run_id>/junit"
}
```

### 4.2 CI 推荐流程（无需等 API Token 也能用）

1. CI 用成员 JWT 调 `POST /api/v1/plans/:id/run`（`env_id` 可选）；
2. 配置租户通知渠道订阅 `run_finished`，webhook 指向 CI 的 Job Trigger
   （Jenkins Generic Webhook / GitLab Pipeline Trigger Token / 自定义网关）；
3. CI 收到 payload 后按 `status` 判断，需要报告时用同 JWT 拉 `junit_url`；
4. CI 把 XML 交给 JUnit 插件渲染（Jenkins/GitLab 原生支持）。

> API Token / CLI 落地后，步骤 1/3 可改为机器凭证，无需人类账号 JWT。

---

## 5. Proto 治理自动化

### 5.1 配置

- `buf.yaml`：module `proto`，lint `STANDARD`；
  仅排除两条 `RPC_REQUEST_STANDARD_NAME` / `RPC_RESPONSE_STANDARD_NAME`
  （Worker `Connect(stream WorkerEvent) returns (stream SchedulerCommand)` 是刻意命名）；
  breaking 使用 `FILE` 规则。
- `scripts/proto-gen.sh`：统一 Go + Python + grounding 生成入口。
- `scripts/proto-check.sh`：buf lint/breaking + 重新生成 + `git diff --exit-code`。

### 5.2 版本锁定

| 工具 | 版本 |
|---|---|
| buf | 1.55.0 |
| protoc | 35.0 |
| protoc-gen-go | v1.28.1 |
| protoc-gen-go-grpc | v1.2.0 |
| grpcio-tools | 1.83.0 |
| protobuf | 7.35.1 |

### 5.3 生成产物检查范围

`scheduler/gen/`、`worker/src/testpilot/`、`scheduler/internal/grpcserver/schema.json`、
`copilot/src/testpilot_copilot/grounding/`。

---

## 6. GitHub Actions CI/CD

### 6.1 CI（.github/workflows/ci.yml）

| Job | 内容 |
|---|---|
| proto | buf lint；PR 时 buf breaking（against base）；protoc+插件全量重生成并检查 git diff |
| scheduler | `go vet ./...` + `go test ./...` + `go build ./cmd/scheduler` |
| worker | pip install editable + pytest（unraisable warning = error） |
| copilot | vendor wheel + editable install + pytest |
| web | pnpm frozen install + oxlint + tsc/vite build |

### 6.2 CD（.github/workflows/cd.yml）

- 触发：`v*` tag 或手动 workflow_dispatch；
- 构建三个镜像（scheduler/worker/copilot）并推送到 `ghcr.io`；
- 使用 buildx + GitHub Actions cache。

---

## 7. 后续开发约定

- **schema 变更必须走迁移**：不得只改 GORM tag 后依赖 AutoMigrate 静默变更；
  AutoMigrate 只作为新库基线与模型级兜底。
- **proto 变更必须过 `scripts/proto-check.sh`**，提交生成产物；破坏性变更要在 PR
  描述里说明并确认 breaking 规则。
- **CI 失败即阻断合并**；本地完整回归命令：

```bash
cd scheduler && GOCACHE=/tmp/testpilot-gocache go test ./...
cd worker && venv/bin/python -m pytest -q -W error::pytest.PytestUnraisableExceptionWarning
cd copilot && venv/bin/python -m pytest -q
cd web && pnpm lint && pnpm build
BUF=… scripts/proto-check.sh
```
