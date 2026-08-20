# TestPilot 部署指南

> 📚 文档导航：[设计](design.md) · [数据模型](data-model.md) · [路线图](roadmap.md) · [使用指南](usage.md) · [部署](deployment.md) · [API 参考](api.md) · [错误码](error-codes.md) · [工程化](ci-migration-plan.md) · [低代码 ID 调用](lowcode-api-invocation.md) · [v2 特性存档](v2-features.md)

## 目录

1. 启动配置（YAML / env / flag）
2. 单机开发（scripts/dev.sh）
3. 生产：docker compose
4. 数据库：SQLite → PostgreSQL
5. 制品存储：本地目录 → S3
6. 可观测性
7. 安全清单

---


两种本地调试形态 + 生产 compose 模板（design 13.4；首期不引入 k8s）。

## 启动配置（YAML / env / flag）

三服务统一：**显式 CLI flag > 环境变量 > YAML > 内置默认**（Copilot 在 env 与 YAML 之间多一层
`.env`；`api_key` 不提供 CLI 入口，避免进程列表泄漏）。YAML 发现顺序：
`--config` > `TP_CONFIG` / `TP_WORKER_CONFIG` / `TP_COPILOT_CONFIG` > 当前目录 `<service>.yaml`。
键名 = flag 的 snake_case（`--http-addr` ↔ `http_addr` ↔ `TP_HTTP_ADDR`）。

逐键注释模板：`deploy/scheduler.yaml.example`、`deploy/worker.yaml.example`、
`deploy/copilot.yaml.example`（含 DB 连接池、JWT 有效期、HTTP 超时/BodyLimit、保留间隔、
沙箱限额、egress、OTel 等细化参数）。compose 形态仍以环境变量注入（见 `.env.example`），
YAML 适合裸机/虚拟机部署。

## 单机开发（scripts/dev.sh）

```bash
scripts/dev.sh start    # scheduler + worker + copilot + echo + vite
scripts/dev.sh status / stop
```

- Scheduler 构建到稳定路径 `.data/bin/scheduler` 并**回环绑定**（127.0.0.1:8080/9090），
  避免 macOS 应用防火墙对每次新构建的未签名二进制弹"接受传入连接"审批。
- 数据库默认 SQLite（`.data/testpilot.db`）；产物目录 `.data/artifacts`（Scheduler 与 Worker 共享）。

## 生产：docker compose

```bash
cp deploy/.env.example deploy/.env   # 填 PG_PASSWORD / TP_JWT_SECRET / TP_COPILOT_API_KEY
docker compose -f deploy/docker-compose.prod.yml --env-file deploy/.env up -d --build
```

服务清单：

| 服务 | 镜像/构建 | 端口 | 说明 |
|---|---|---|---|
| postgres | postgres:17-alpine | 127.0.0.1:5432（仅回环，可删） | 主库（`TP_DB_DSN` 指向） |
| scheduler | deploy/scheduler.Dockerfile | :8080 REST+前端、:9090 gRPC | 内嵌 web/dist 托管；`/copilot-api/*` 反代到 copilot（`TP_COPILOT_URL`），SSE 生产入口收敛到 :8080 |
| worker | deploy/worker.Dockerfile × `${WORKER_REPLICAS}` | — | functional/lowcode/playwright |
| worker-stress | 同上 × `${STRESS_WORKER_REPLICAS}` | — | 压测独占编排 |
| copilot | deploy/copilot.Dockerfile | 127.0.0.1:8100（仅调试，可删） | LLM 密钥只走 .env；正常流量走 scheduler 反代 |
| jaeger | jaegertracing/jaeger | :16686 UI、:4317 OTLP | 链路（`TP_OTEL_EXPORTER=otlp`） |
| prometheus | prom/prometheus | :9091 | 抓取 scheduler:8080/metrics |

Worker 扩缩：`docker compose ... up -d --scale worker=4`（或改 `.env` 的 `WORKER_REPLICAS`）。
Worker 与 Scheduler 经命名卷 `artifacts` 共享产物目录；生产可换对象存储（改 `TP_ARTIFACT_DIR` 语义层）。

### 构建备注

- 三个 Dockerfile 的构建上下文都是**仓库根**（compose 已配置 `context: ..`）。
- 依赖镜像源与宿主开发机一致（GOPROXY=goproxy.cn、npm=npmmirror、pip=tuna + pypi 回退），
  均可用 `--build-arg GOPROXY=... / NPM_REGISTRY=... / PIP_INDEX_URL=...` 覆盖。
- Worker 镜像含 Playwright + Chromium（`--with-deps`），体积较大属预期。

## 数据库：SQLite → PostgreSQL

- 默认 SQLite（`TP_DB_PATH`）；设 `TP_DB_DSN=postgres://user:pass@host:5432/dbname?sslmode=disable` 即切 PG。
- 启动时 AutoMigrate + 种子（默认租户 + admin/admin123）；**PG 全量 e2e 回归通过**（Phase 9 验证）。
- 生产 DDL 参考：`docs/sql/postgresql.sql` / `docs/sql/mysql.sql`（与 GORM 模型同步）。
- 迁移回归：开发期改动模型后至少跑一次 `scripts/e2e.py`（SQLite）+ 一次 PG 冒烟
  （`TP_DB_DSN=... .data/bin/scheduler` 后跑 `scripts/e2e_phase8.py`）。

## 制品存储：本地目录 → S3

默认 `artifact_backend=local`（`TP_ARTIFACT_DIR` 共享目录，compose 用命名卷）。切 S3：

```bash
TP_ARTIFACT_BACKEND=s3
TP_S3_ENDPOINT=https://s3.oss-cn-shanghai.aliyuncs.com   # OSS S3 网关（或 AWS/MinIO 端点）
TP_S3_ACCESS_KEY=... TP_S3_SECRET_KEY=...                 # 仅 env/YAML（不走 CLI flag）
TP_S3_BUCKET=bee-all TP_S3_REGION=cn-shanghai
TP_S3_PREFIX=testpilot/                                  # 可选；键 = {prefix}{tenant_id}/{uri}
# TP_S3_PATH_STYLE=true                                  # 私有 MinIO 需要；AWS/OSS 默认 virtual-hosted
```

- 写入：Worker 仍写共享产物目录（暂存），Scheduler 收到 TaskResult 时上传并删本地文件；
  上传失败仅告警（产物行保留，读取时 404 便于发现）。
- 读取：`GET /artifacts/{id}/content` 经后端；retention 清理经后端删除对象。
- OSS S3 网关端点形如 `s3.oss-{region}.aliyuncs.com`（注意不是 `s3.{region}.aliyuncs.com`）；
  要求 virtual-hosted 寻址（默认即可）。AK/SK 建议环境变量注入。

## 可观测性

- **指标**：`GET /metrics`（Prometheus 格式，公开端点——生产仅对内网/Prom 可达）。
  关键指标：`testpilot_http_requests_total`、`testpilot_runs_total{status,trigger}`、
  `testpilot_run_duration_seconds`、`testpilot_workers_online`、`testpilot_worker_load_sum`、
  `testpilot_dispatch_total`、`testpilot_quota_rejections_total{metric}`、
  `testpilot_notifications_total{type,result}`、`testpilot_stress_runs_total`。
- **链路**：OTel。`TP_OTEL_EXPORTER`：`""`关（默认）/ `stdout` 调试 / `otlp`（+`TP_OTEL_ENDPOINT`，
  默认 127.0.0.1:4317）。Scheduler REST/gRPC 自动 span；派发到 Worker 经
  `TaskAssignment.traceparent` 续链；Copilot 经 gRPC metadata 注入。compose 内 Jaeger 一站式查看。
- **日志**：`TP_LOG_FORMAT=json` 生产格式；Worker/Copilot 日志行带 `[trace_id]`，
  Scheduler 关键路径日志带 `trace_id` 字段，三进程可按 trace_id 串联。

## 安全清单

- `TP_JWT_SECRET` 生产必改；`PG_PASSWORD`、`TP_COPILOT_API_KEY` 只放 `.env`（不入库）。
- 多 Scheduler 实例必须为每个实例配置不同的 `TP_SNOWFLAKE_NODE`（0-1023），否则主键冲突。
- Worker 出口控制：`.env` 默认 `TP_EGRESS_BLOCK_PRIVATE=1`（拦截私网/环回）；有内网测试目标时
  显式用 `TP_EGRESS_ALLOW` 白名单，不要整体关私网阻断。
- 通知 webhook 默认拒绝私网目标；仅当确有内网 webhook 需求时设置 `TP_NOTIFY_ALLOW_PRIVATE=1`。
- `/metrics` 与 OIDC 回调为公开端点；对外暴露时由反向代理收敛。
- 数据保留：`TP_RETENTION_RUN_DAYS`（如 90）开启每小时级联清理（含产物文件）。
