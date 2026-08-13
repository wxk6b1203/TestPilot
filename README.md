# TestPilot

LLM 驱动的自动化集成测试平台 —— Phase 0–9（Copilot、压测、多租户企业化、可观测性与 compose 部署）。

## 组件

| 组件 | 技术 | 端口 | 说明 |
|---|---|---|---|
| Scheduler | Go 1.25 · fiber v3 · GORM/SQLite · gRPC | :8080 (REST+前端) / :9090 (gRPC) | 领域模型、认证(JWT)、调度派发、结果落库、CopilotToolService |
| Worker | Python 3.13 · grpcio · asyncio · httpx | 连接 :9090 | 声明式执行引擎 + 表达式语言 + 低代码沙箱 + Playwright + Locust 压测 |
| Copilot | Python 3.13 · FastAPI · pydantic-ai 2.28 | :8100 | AI 生成/分析：工具经 Scheduler gRPC 执行，写操作 HITL 审批 + 审计 |
| 前端 | React 19 · TS · Vite · AntD | :5173 (dev) | 控制台 + Copilot 对话页 + 压测报告 |
| echo 服务 | Python stdlib | :18080 | 联调用本地回显服务 |

设计文档：`docs/design.md`（架构）· `docs/data-model.md`（31 表 ER）· `docs/roadmap.md`（实施路线）。
使用/部署/API：`docs/usage.md` · `docs/deployment.md` · `docs/api.md`。
契约：`proto/testpilot/{common,worker,copilot}/v1/*.proto`。

## 快速开始

前置：Go 1.25+ · Python 3.13 + uv · Node 24 + pnpm。

```bash
# 1. Worker 依赖（editable 安装）
cd worker && uv sync && cd ..

# 1b. （可选）UI 测试变体：Playwright + Chromium
cd worker && uv pip install --python .venv/bin/python playwright \
  && .venv/bin/python -m playwright install chromium && cd ..

# 1c. Copilot 依赖 + 密钥（DeepSeek 等 OpenAI 兼容端点）
#     （pydantic-ai-extensions 未发布公共 PyPI，经 tool.uv.sources 解析 copilot/vendor/ 内 wheel）
cd copilot && uv sync && cp .env.example .env  # 编辑填入 TP_COPILOT_API_KEY（勿提交） && cd ..

# 2. 前端依赖 + 构建（构建后 Scheduler 可直接托管于 :8080）
cd web && pnpm install && pnpm build && cd ..

# 3. 一键起全栈（scheduler + worker + copilot + echo + vite dev）
scripts/dev.sh start

# 4. 端到端闭环验证（声明式 + 低代码沙箱 + UI 用例 + 压测）
worker/.venv/bin/python scripts/e2e.py
# 4b. Copilot E2E（真实 LLM 调用：HITL 生成接口+用例、审计校验）
worker/.venv/bin/python scripts/e2e_copilot.py
# 4c. Phase 8 E2E（RBAC / 跨租户隔离 / 配额 429 / OIDC 登录 / 定时+通知）
worker/.venv/bin/python scripts/e2e_phase8.py
# 4d. Phase 9 E2E（Prometheus 指标 / 审计留痕）
worker/.venv/bin/python scripts/e2e_phase9.py
```

打开 http://localhost:5173 （dev 热更新）或 http://localhost:8080 （托管构建产物），
账号 `admin` / `admin123`。Copilot 对话页在左侧菜单「Copilot」。

停止：`scripts/dev.sh stop`；状态：`scripts/dev.sh status`。

生产形态（PG + 多 Worker + Jaeger + Prometheus）：
`cp deploy/.env.example deploy/.env && docker compose -f deploy/docker-compose.prod.yml --env-file deploy/.env up -d --build`，
详见 `docs/deployment.md`。

## MVP 能力

- **声明式用例**：递归步骤树 — API_CALL / ASSERTION / SET_VAR / IF / LOOP / RETRY / DELAY /
  CODE_BLOCK（沙箱）/ UI_ACTION（Playwright）
- **低代码用例（Phase 4）**：Python SDK（pydantic 模型 + `assert_that` 链式断言）在
  **subprocess 沙箱**中运行：setrlimit 资源约束、环境变量白名单、临时工作目录、超时强杀，
  macOS `sandbox-exec` / Linux `bwrap` 强制无网络出口；HTTP/变量等副作用经**能力桥**
  （stdin/stdout JSON Lines）由 Worker 代执行 —— 沙箱内零凭据、零明文密钥
- **UI 测试（Phase 5）**：13 种 UI_ACTION（GOTO/CLICK/FILL/SELECT/CHECK/HOVER/PRESS/
  EXPECT_TEXT/EXPECT_VISIBLE/SCREENSHOT/WAIT/UPLOAD/DOWNLOAD）；每用例一次浏览器启动、
  BrowserContext 隔离、全程 tracing；产物 = 截图（含失败自动截图）+ trace.zip + network.har，
  运行详情页内联看图、trace 下载后可 `npx playwright show-trace` 回放
- **Copilot（Phase 6）**：FastAPI + pydantic-ai 2.28，Provider 注册表（deepseek /
  openai_compatible）直连 OpenAI 兼容端点；工具 = CopilotToolService gRPC（19 RPC，
  不直连 DB）；写/触发工具 `requires_approval` → 前端 HITL 批准后才执行，
  Scheduler 落审计（actor=copilot, approved_by=用户）；上下文压缩经
  pydantic-ai-extensions 的 ContextCompression capability（fraction 0.7 触发、保留最近 6 条）；
  前端手写 Vercel AI SSE 消费（文本流 + 工具调用 + 审批按钮），会话经 Scheduler REST 持久化
- **压测（Phase 7）**：Locust 以独立子进程运行（gevent，不与 asyncio 混部），
  JSON Lines stdout 协议上报采样点；Scheduler 将目标并发均衡拆分到多个 stress-capable
  Worker（Worker 独占式压测调度）；指标点落 `stress_metric_points`（生产换 VictoriaMetrics），
  报告页手绘 SVG 时序图（RPS/P95/并发/错误率）
- **表达式语言**：`{{expr}}` 模板 + AST 白名单安全求值（无函数调用/dunder），
  作用域含环境变量、`vars`、`response`（`response.json` / `.status` / `.headers` / `.elapsed_ms`）
- **统一断言**：STATUS / HEADER / BODY / JSONPATH / ELAPSED × EQ/NE/EXISTS/NOT_EXISTS/CONTAINS/MATCHES/GT/LT/GE/LE/TYPE_IS
- **多租户**：tenant_id 全表隔离（应用层过滤），雪花 ID，REST 层 ID 字符串化（JS 安全、输入字符串自动还原）
- **认证 / RBAC（Phase 8）**：本地账号（bcrypt + JWT 24h；种子 `admin/admin123` = tenant 1 owner）
  + **OIDC** 授权码流程（可插拔 identity_providers，租户级；RS256 JWKS / HS256 验签，
  外部用户首次登录自动建档并落 viewer）。角色 owner=1 / admin=2 / member=3 / viewer=4
  （小=高），路由约定：GET→viewer、领域写→member、租户治理（成员/配额/通知/身份源/审计）→admin；
  自助建租户（POST /tenants）+ switch-tenant 换签；最后一名 owner 不可降级/移除
- **配额（Phase 8）**：tenant_quota（metric 维度上限，0/缺省=不限，用量实时从事实表计算）——
  concurrent_runs / monthly_runs / artifact_bytes / ai_calls / worker_slots；超限 429 `QUOTA_EXCEEDED`
- **定时调度（Phase 8）**：robfig/cron 5 段表达式，overlap_policy（跳过/并发），
  启动时 misfire 补跑（落后 >2min）；触发记 trigger=SCHEDULED + triggered_by=schedule:<id>
- **通知（Phase 8）**：notification_channels —— Webhook（原始 JSON）/ 钉钉（markdown + HMAC-SHA256
  URL 签名）/ 飞书（text + body 签名）；事件 run_finished / stress_finished，异步发送不阻塞结果落库
- **SSRF 出口控制（Phase 8，Worker）**：`TP_EGRESS_ALLOW` host 白名单（精确/`.后缀`）+
  `TP_EGRESS_BLOCK_PRIVATE=1` 解析后拦截私网/环回；声明式引擎与沙箱能力桥共用
- **数据保留（Phase 8）**：`TP_RETENTION_RUN_DAYS`（0=永久），按 run 级联清理
  step/case/artifact（含磁盘文件）+ 压测时序点，每小时一轮、单轮 500 run 上限
- **可观测性（Phase 9）**：`GET /metrics`（Prometheus：HTTP/run/dispatch/配额/通知/worker 池）；
  OTel 链路三进程贯通（Scheduler REST/gRPC span → `TaskAssignment.traceparent` → Worker，
  Copilot 经 gRPC metadata 注入），`TP_OTEL_EXPORTER=stdout|otlp`；日志带统一 trace_id
- **审计完善（Phase 9）**：人工变更中间件（非 GET 成功即落）、敏感变量读取（secret_read）、
  租户切换（落目标租户）；Copilot 写操作审计自 Phase 6
- **部署（Phase 9）**：`deploy/` —— 三组件 Dockerfile + `docker-compose.prod.yml`
  （PG + scheduler + worker 副本 + stress worker + copilot + Jaeger + Prometheus）+ `.env.example`；
  `TP_DB_DSN` 一键 SQLite→PostgreSQL（PG 全量 e2e 回归通过）
- **调度**：Worker 能力路由（functional/lowcode/playwright/stress）+ 最少负载优先 + 共享/独占租户模型
- **导入导出**：OpenAPI 3（JSON/YAML，幂等跳过）、curl 命令行、导出 OpenAPI 3
- **结果模型**：TestRun → TestCaseResult → TestStepResult（点路径定址，含请求/响应快照与断言明细）+ Artifact（截图/trace/har）
- **错误码体系**：REST 统一 `{error:{code,message}}`，注册表见 `docs/error-codes.md`
- **DDL**：`docs/sql/postgresql.sql` / `docs/sql/mysql.sql`（30 表，与 GORM 模型一致）

## 代码生成

```bash
# Go（scheduler/gen/）
protoc -I proto --go_out=scheduler/gen --go_opt=module=github.com/testpilot/testpilot/gen \
  --go-grpc_out=scheduler/gen --go-grpc_opt=module=github.com/testpilot/testpilot/gen \
  proto/testpilot/common/v1/types.proto proto/testpilot/worker/v1/worker.proto \
  proto/testpilot/copilot/v1/copilot.proto

# Python（worker/src/）
cd worker && .venv/bin/python -m grpc_tools.protoc -I ../proto \
  --python_out=src --pyi_out=src --grpc_python_out=src \
  ../proto/testpilot/common/v1/types.proto ../proto/testpilot/worker/v1/worker.proto \
  ../proto/testpilot/copilot/v1/copilot.proto
```

## 环境变量（Scheduler）

| 变量 | 默认 | 说明 |
|---|---|---|
| `TP_HTTP_ADDR` | `:8080` | REST + 前端托管 |
| `TP_GRPC_ADDR` | `:9090` | gRPC（Worker/Copilot） |
| `TP_DB_PATH` | `testpilot.db` | SQLite 文件 |
| `TP_DB_DSN` | 空 | 非空切 PostgreSQL（`postgres://user:pass@host:5432/db?sslmode=disable`），优先于 TP_DB_PATH |
| `TP_JWT_SECRET` | `dev-secret-change-me` | JWT 签名密钥（生产必改） |
| `TP_STATIC_DIR` | 空 | 前端 dist 目录（空则不托管） |
| `TP_ARTIFACT_DIR` | `.data/artifacts` | 产物根目录（Worker 与 Scheduler 须一致；生产换对象存储） |
| `TP_RETENTION_RUN_DAYS` | `0` | 运行数据保留天数（0=永久；>0 时每小时级联清理） |
| `TP_OTEL_EXPORTER` | 空 | 空=关 / `stdout` 调试 / `otlp`（配 `TP_OTEL_ENDPOINT`，默认 127.0.0.1:4317） |
| `TP_LOG_FORMAT` | 空 | `json` = 生产格式日志 |

Worker：`testpilot-worker --scheduler 127.0.0.1:9090 --capabilities functional,lowcode,playwright --tags region=local --max-concurrency 4 --tenant-id 0`

Worker 出口控制（SSRF）：`TP_EGRESS_ALLOW`（逗号分隔 host 白名单，支持 `.后缀`；空=不限）、
`TP_EGRESS_BLOCK_PRIVATE=1`（解析目标后拦截私网/环回/链路本地地址）。

沙箱限额（Worker 环境变量）：`TP_SANDBOX_CPU`(30s) `TP_SANDBOX_MEM_MB`(1024)
`TP_SANDBOX_NPROC`(128) `TP_SANDBOX_NOFILE`(128) `TP_SANDBOX_FSIZE_MB`(32)
`TP_SANDBOX_NET`(deny/allow)

## REST 一览（`/api/v1`，除 login 外均需 `Authorization: Bearer`）

```
POST /auth/login            GET  /me
POST /auth/switch-tenant    POST /tenants          # 自助建租户（创建者为 owner）
GET|POST /projects          GET|PUT|DELETE /projects/{id}
GET|POST /environments      PUT|DELETE /environments/{id}
GET|POST /variables         PUT|DELETE /variables/{id}
GET|POST /apis              GET|PUT|DELETE /apis/{id}
GET|POST /cases             GET|PUT|DELETE /cases/{id}
GET|POST /plans             GET|PUT|DELETE /plans/{id}
POST /plans/{id}/run        GET  /runs  GET /runs/{id}
POST /import/openapi        POST /import/curl   GET /export/openapi?project_id=
GET  /workers               GET  /artifacts/{id}/content
GET|POST /stress-plans      GET|PUT|DELETE /stress-plans/{id}
POST /stress-plans/{id}/run GET  /stress-runs  GET /stress-runs/{id}
GET  /copilot/sessions      POST /copilot/sessions
GET|POST /copilot/sessions/{id}/messages      GET /audit-logs
# 租户治理（admin+）
GET|POST /tenant/members    PUT|DELETE /tenant/members/{userID}
GET  /tenant/quotas         PUT  /tenant/quotas/{metric}
GET|POST /schedules         PUT|DELETE /schedules/{id}
GET|POST /notifications     PUT|DELETE /notifications/{id}
GET|POST /identity-providers  PUT|DELETE /identity-providers/{id}
# OIDC 登录链路（公开，无需 token）
GET  /auth/oidc/providers   GET  /auth/oidc/{id}/login  GET /auth/oidc/{id}/callback
```

Copilot 服务自身（:8100）：`POST /api/chat`（Vercel AI SSE，需 `Authorization: Bearer <scheduler token>`，
可选 `X-Session-Id` 续会话）+ `GET /api/healthz`。

## 环境变量（Copilot）

| 变量 | 默认 | 说明 |
|---|---|---|
| `TP_COPILOT_PROVIDER` | `deepseek` | Provider 注册表 key：`deepseek` / `openai_compatible` |
| `TP_COPILOT_API_KEY` | 无 | LLM 密钥（只放 `copilot/.env` 或环境变量，勿提交） |
| `TP_COPILOT_BASE_URL` | 空 | 空 = Provider 默认端点（deepseek → api.deepseek.com） |
| `TP_COPILOT_MODEL` | `deepseek-v4-flash` | 主模型 |
| `TP_COPILOT_SUMMARIZER_MODEL` | 同主模型 | 上下文压缩摘要器 |
| `TP_COPILOT_CONTEXT_WINDOW` | `64000` | 压缩阈值基准（fraction 0.7 触发） |

## MVP 边界（未含）

- 低代码 Page 模型（沙箱内驱动浏览器，需能力桥扩展 UI 操作）、gRPC 调用步骤
- OAuth2（非 OIDC）登录、对象存储制品后端
- suite 引用展开（PlanItem ref_type=2）
- Copilot：ApplyOpenApiDiff 未实现（proto 占位）；生产部署时 :8100 需反向代理（dev 走 vite proxy）
