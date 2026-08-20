# TestPilot

TestPilot 是一个 LLM 增强的集成测试平台：统一管理 HTTP / gRPC 接口，用声明式步骤树或
Python 低代码编写测试，支持 Playwright 页面 E2E、分布式压测、AI Copilot 生成与分析，
并面向团队提供多租户、RBAC、定时调度、通知和 CI 集成。

## 功能

### 接口管理

- HTTP 接口：方法 / URI / params / headers / cookies / body / TLS / 重定向 / JSONC / 二进制引用；
  单接口调试工作区（即时请求与响应面板）。
- gRPC 接口：proto 文件资产 + server reflection 动态调用，无需预编译桩。
- 目录树、项目 / 环境 / 变量 / 证书管理；变量支持 `{{expr}}` 模板与敏感标记。
- 导入导出：OpenAPI 3、curl 命令、Postman Collection v2.1；
  Copilot 可对 OpenAPI 做增量 diff（`apply_openapi_diff`）。

### 测试

- **声明式用例**：递归步骤树，支持 API_CALL / GRPC_CALL / ASSERTION / SET_VAR /
  IF / LOOP / RETRY / DELAY / CODE_BLOCK / UI_ACTION，前后置脚本与步骤级参数覆盖。
- **低代码用例**：`testpilot-sdk` Python SDK + `assert_that` 链式断言，按接口 ID 调用
  （`ctx.http_api(id)` / `ctx.grpc_api(id)`），派发时自动生成 `Api<ID>` 封装类；
  脚本在受限沙箱中执行，HTTP/变量/UI 副作用经能力桥由 Worker 代执行，沙箱内零凭据。
- **Playwright E2E**：`ctx.page` 页面模型与 13 种 UI 动作，截图 / trace / HAR 产物，
  trace 可用 Playwright Trace Viewer 回放。
- **套件 / 计划 / 脚本资产**：套件有序展开、脚本资产复用（`script_ref`）、
  计划条目参数覆盖，支持单用例直接运行。

### 报告与 CI

- 三级结果模型：TestRun → TestCaseResult → TestStepResult，含请求/响应快照、断言明细与产物。
- **实时进度推送**：SSE 通道（run/project/stress/workers）替代高频轮询，
  运行列表、详情抽屉、压测报告与 Worker 状态实时刷新，保留 30s 兜底对账。
- 运行记录与运行详情均支持**导出 JUnit XML**（`GET /runs/:id/junit`）。
- `run_finished` webhook 带 `junit_url`，可事件驱动接入 Jenkins / GitLab 等 CI。
- **API Token**：管理台可颁发 / 撤销 `tp_` 机器凭证，适用于 CI / CLI；数据库仅存哈希，
  权限随颁发者的租户成员角色动态生效。
- 运行取消、租户配额、数据保留策略与制品生命周期管理。

### 压测

- 接口压测：Locust 子进程发压，多 Worker 拆分负载，RPS / P95 / 错误率时序图。
- 行为压测：低代码用例作为负载模型，沙箱常驻循环执行，按并发与 ramp 门控。

### Copilot

- 自然语言生成接口 / 用例 / 计划，分析失败根因，做覆盖率与目录问答。
- 写/触发操作默认 HITL 审批并落审计；支持当前页面项目/环境上下文。
- 支持 DeepSeek 与任意 OpenAI 兼容端点；前端经 Vercel AI SSE 流式交互。

### 平台能力

- 多租户隔离、雪花 ID、JWT + OIDC / OAuth2 登录、owner / admin / member / viewer RBAC。
- 定时调度、Webhook / 钉钉 / 飞书通知、配额、租户配置开关、审计日志。
- 版本化 schema 迁移（`schema_migrations`）、SQLite / PostgreSQL 双存储、
  S3 兼容制品后端、Prometheus 指标 + OpenTelemetry 链路。
- Docker Compose 生产部署模板（Scheduler / Worker / Copilot / PG / Jaeger / Prometheus）。

## 组件

| 组件 | 技术栈 | 地址 | 职责 |
|---|---|---|---|
| Scheduler | Go · fiber v3 · GORM · gRPC | `:8080` REST/前端，`:9090` gRPC | 领域 CRUD、认证/RBAC、调度、结果落库、通知、Copilot 工具面 |
| Worker | Python · asyncio · httpx · grpcio | 连接 `:9090` | 声明式执行引擎、低代码沙箱、Playwright、压测 |
| Copilot | Python · FastAPI · pydantic-ai | `:8100` | AI Agent：经 Scheduler gRPC 工具读写，HITL 审批 |
| Frontend | React · TypeScript · Vite · Ant Design | `:5173`（dev） | IDE 式控制台：接口、用例、计划、报告、管理台、Copilot 对话 |
| echo | Python stdlib | `:18080` | 本地联调回显服务 |

## 快速开始

前置：Go 1.25+、Python 3.13、Node 22+ 与 pnpm 11+。

```bash
# 1. Worker 依赖（Playwright 可选）
cd worker && uv sync --extra playwright \
  && venv/bin/python -m playwright install chromium && cd ..

# 2. Copilot 依赖 + LLM 密钥
cd copilot && uv sync && cp .env.example .env && cd ..
# 编辑 copilot/.env，填入 TP_COPILOT_API_KEY

# 3. 前端依赖
cd web && pnpm install && pnpm build && cd ..

# 4. 一键启动全栈（scheduler + worker + copilot + echo + vite）
scripts/dev.sh start

# 5. 端到端验证
worker/venv/bin/python scripts/e2e.py
```

打开 <http://localhost:5173>（开发热更新）或 <http://localhost:8080>（Scheduler 托管构建产物）。
默认账号：`admin / admin123`（生产环境必须修改 `TP_ADMIN_PASSWORD` 与 `TP_JWT_SECRET`）。

停止：`scripts/dev.sh stop`；状态：`scripts/dev.sh status`。

## 使用

### CI 接入示例

```bash
# 1. 在管理台「API Token」创建机器凭证（原始 token 只显示一次）
TOKEN="tp_..."

# 2. 触发计划运行
curl -s -X POST http://localhost:8080/api/v1/plans/<plan_id>/run \
  -H "Authorization: Bearer $TOKEN"

# 3. 运行结束后下载 JUnit 报告
curl -s -o junit.xml http://localhost:8080/api/v1/runs/<run_id>/junit \
  -H "Authorization: Bearer $TOKEN"
```

也可以配置通知渠道订阅 `run_finished`，webhook payload 中包含 `status` / `summary` /
`junit_url`，CI 无需轮询即可按事件拉取报告。

### 配置

三个服务统一三级配置：CLI flag > 环境变量 > YAML 文件；逐键注释模板见
`deploy/scheduler.yaml.example` / `deploy/worker.yaml.example` / `deploy/copilot.yaml.example`。
生产环境变量与安全清单见 `docs/deployment.md`。

### 完整文档

| 文档 | 内容 |
|---|---|
| `docs/design.md` | 总体架构与设计决策 |
| `docs/data-model.md` | 数据模型与 33 张表 |
| `docs/usage.md` | 用例、调度、通知、Copilot、压测、CI 操作手册 |
| `docs/api.md` | REST API 参考 |
| `docs/deployment.md` | 部署、数据库、制品存储、可观测性与安全清单 |
| `docs/ci-migration-plan.md` | 迁移版本化、JUnit/Webhook、API Token、proto 治理与 CI/CD |
| `docs/lowcode-api-invocation.md` | 低代码按接口 ID 调用与自动封装设计 |
| `docs/blog-lowcode-copilot.md` | 技术博文：低代码与 Copilot 相比传统 API 工具的设计优势 |
| `docs/roadmap.md` | 阶段路线图、风险登记与另议项 |
| `docs/error-codes.md` | 错误码注册表 |

## 开发

### 目录结构

```text
scheduler/   Go：领域模型、REST/gRPC、调度、迁移、通知、制品
worker/      Python：执行引擎、低代码 SDK、沙箱、Playwright、压测
copilot/     Python：FastAPI + pydantic-ai Agent 与工具集
web/         React：控制台前端
proto/       protobuf 单一契约（common / worker / copilot）
deploy/      Dockerfile、compose 与 YAML 模板
docs/        设计、数据模型、使用、部署、API 文档
scripts/     dev、e2e、proto 生成/校验等脚本
```

### 测试与构建

```bash
# Scheduler
cd scheduler && go test ./...

# Worker
cd worker && venv/bin/python -m pytest -q -W error::pytest.PytestUnraisableExceptionWarning

# Copilot
cd copilot && venv/bin/python -m pytest -q

# 前端
cd web && pnpm lint && pnpm build
```

### proto 契约治理

- proto 位于 `proto/`，是 Go / Python / Copilot 三方的单一事实源。
- 生成入口：`scripts/proto-gen.sh`（Go gRPC + Python gRPC + Copilot grounding）。
- 校验入口：`scripts/proto-check.sh`（`buf lint` / `buf breaking` + 生成零漂移检查）。
- 生成产物随仓库提交以支持离线构建，修改 proto 后必须同步提交生成结果。

### 数据库迁移

- 迁移记录表 `schema_migrations`；`v1` = 当前 GORM AutoMigrate 基线。
- 新增迁移：在 `scheduler/internal/migrate/migrate.go` 注册下一版本，同时提供
  SQLite / PostgreSQL 两版幂等 SQL，并补充存量库与新库测试。
- 详细约定见 `docs/ci-migration-plan.md`。

### CI / CD

- `.github/workflows/ci.yml`：proto 治理 + Scheduler / Worker / Copilot 测试 + 前端构建。
- `.github/workflows/cd.yml`：`v*` tag 触发，构建并推送 Scheduler / Worker / Copilot 镜像到 `ghcr.io`。

## License

TestPilot 采用 [Apache License 2.0](LICENSE)（`SPDX: Apache-2.0`）：

- **开源使用**：允许商用、修改、闭源衍生与再分发，仅需保留版权与署名声明（Apache 2.0 第 4 条）。
- **商标**：TestPilot 名称与 logo 的使用受[商标使用指南](TRADEMARK_GUIDELINES.md)约束。
- **商业授权**：需要白标（免署名）或特殊保证的，见
  [COMMERCIAL_LICENSE.md](COMMERCIAL_LICENSE.md)（中文）与
  [COMMERCIAL_LICENSE_EN.md](COMMERCIAL_LICENSE_EN.md)（英文）。
