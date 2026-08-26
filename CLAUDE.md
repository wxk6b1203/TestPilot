# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目概览

TestPilot 是 LLM 增强的集成测试平台（接口管理 / 声明式步骤树与 Python 低代码用例 / Playwright E2E / 分布式压测 / AI Copilot），四组件 monorepo：

| 组件 | 技术栈 | 端口 | 目录 |
|---|---|---|---|
| Scheduler | Go 1.25 · fiber v3 · GORM · gRPC | `:8080` REST/前端托管，`:9090` gRPC | `scheduler/` |
| Worker | Python 3.13 · asyncio · httpx · grpcio | 主动连 `:9090` | `worker/` |
| Copilot | Python 3.13 · FastAPI · pydantic-ai | `:8100` | `copilot/` |
| Web | React 19 · TS · Vite · Ant Design 6 | `:5173`（dev） | `web/` |

**拓扑铁律**：Worker 与 Copilot 都是 Scheduler 的直接下游、彼此不通信；**数据库只属于 Scheduler**——下发给 Worker 的任务里所有实体（API 快照、二进制、生成的 `tp_api_wrappers.py` 源码）都已内联解析，Worker 零 DB 访问。前端经 Scheduler 反代访问 Copilot（`/copilot-api/*`）。SSE 事件走 Scheduler 进程内 Broker——**单实例架构**，多实例需引入 Redis/NATS（且 `recover_interrupted` 须关、`snowflake_node` 须唯一）。

文档与代码注释用简体中文；commit 遵循 Conventional Commits，中文 subject 必须追加 `English: <summary>` footer（`.github/git-commit-instructions.md`）。

## 常用命令

### 本地全栈

```bash
scripts/dev.sh start    # 一键起 echo/grpc-echo/scheduler/worker/copilot/vite（隐式先 stop）
scripts/dev.sh status   # 健康检查；停止用 stop
worker/venv/bin/python scripts/e2e.py   # 主 E2E（需全栈运行中）
```

前提：`cd worker && uv sync --extra playwright`、`cd copilot && uv sync` 并在 `copilot/.env` 填 `TP_COPILOT_API_KEY`、`cd web && pnpm install`。运行态（DB/日志/pid/jwt.secret/worker.token）都在 gitignore 的 `.data/`。Python 环境在 `venv/`（不是 `.venv/`，`UV_PROJECT_ENVIRONMENT=venv`）。

### 测试（按组件）

```bash
# Scheduler
cd scheduler && go test ./...
go test ./internal/httpserver/ -run TestXxx -count=1          # 单个测试

# Worker（本地 pytest 需 PYTHONPATH=src；CI 用 pip install -e）
cd worker && PYTHONPATH=src venv/bin/python -m pytest -q
PYTHONPATH=src venv/bin/python -m pytest tests/test_engine.py::test_xxx -q   # 单个测试

# Copilot（测试全离线，不需要 LLM key）
cd copilot && PYTHONPATH=src venv/bin/python -m pytest -q

# Web：无测试框架，只有 lint（oxlint）+ build（tsc 类型检查在 build 里）
cd web && pnpm lint && pnpm build
```

### proto 契约（改 `.proto` 后必须执行）

```bash
scripts/proto-gen.sh    # 重新生成 Go + Worker/Copilot Python + grounding
scripts/proto-check.sh  # buf lint / buf breaking + 生成零漂移校验（CI 强制）
```

生成产物**随仓库提交**，改 proto 后必须同步提交以下 6 处：`scheduler/gen/`、`worker/src/testpilot/`、`copilot/src/testpilot/`（故意不含 worker.proto，勿给 Copilot 加 worker 导入）、`scheduler/internal/grpcserver/schema.json`、`copilot/src/testpilot_copilot/grounding/`、`buf.yaml`。禁止手改生成代码；protoc / grpcio-tools 版本已钉死，两侧 venv 工具版本要一致，否则产生漂移噪音。

## 架构

### 一次测试运行的数据流

触发（手动 / cron / CI token）→ Scheduler 建 `TestRun` 并把 TestPlan 展开为 TestCases → 按 capability / 租户 / 负载选 Worker，经 bidi 流下发 `TaskAssignment` → Worker 执行（声明式步骤树或沙箱低代码），边执行边回推 `step_progress` / `log_batch` / 制品引用 → `TaskResult` → 汇总落库、通知、JUnit 导出（`GET /runs/:id/junit`）。取消/超时经 gRPC cancel 命令传播。变量作用域：step > case > plan > env > project。

三条通信链路：
- **Worker ↔ Scheduler**：Worker 主动发起 `WorkerService.Connect` bidi 流（首帧 register，之后心跳/事件；Scheduler 推 task/cancel/config）。鉴权用 `x-worker-token` 元数据。
- **Copilot ↔ Scheduler**：Copilot 是 gRPC 客户端调 `CopilotToolService`（12 只读工具免审批；8 写 + 2 触发工具全部 `requires_approval=True` HITL + 落审计）。请求第一字段必须是 `ctx`（RequestContext）——Scheduler 鉴权拦截器按字段名 `Ctx` 反射读取并与 JWT 交叉校验。
- **前端 ↔ Copilot**：Vercel AI Data Stream Protocol v7（SSE），版本两端锁死。

### Scheduler（Go）

- 入口 `cmd/scheduler/main.go`；boot 顺序：config → snowflake/zap/OTel → `db.Open`（内含迁移 + seed）→ recover 中断运行 → dispatch/runner/cron → reapers/retention → gRPC `:9090` → fiber `:8080` → 优雅关停（**必须先 `w.Shutdown()` 关 Worker 流再 `gs.GracefulStop()`**，否则挂死）。
- **没有 service/repo 层**：REST handler 直接查 GORM（fat handler）；横切编排独立成包（`runner` / `dispatch` / `cronsched` / `quota` / `impexp`），同样直接拿 `*gorm.DB`。模型集中在 `internal/model`。
- 全部路由在 `internal/httpserver/server.go` 的 `App()`，经 `h(method, path, minRole, fn)` 注册；RBAC 数字越小越强（owner=1/admin=2/member=3/viewer=4）。错误统一 `{"error":{code,message}}`，新错误码须同时注册 `internal/apperr` 并同步 `docs/error-codes.md`。
- 配置三级（CLI kebab-case flag > `TP_*` env > snake_case YAML > 默认）由 struct tag 反射自动派生——**加配置项只需加结构体成员和默认值**。模板见 `deploy/scheduler.yaml.example`。
- 迁移在 `internal/migrate/migrate.go`（版本化，v1 = AutoMigrate 基线）：新增迁移注册进 `List()`，提供 SQLite/PG 双份幂等 SQL + 存量库与新库测试；新 GORM 模型加进 `model.AllModels()`（例外：`ApiToken` 故意只在版本化迁移里）。`docs/sql/*.sql` 仅是参考导出，需手动同步。
- 安全默认 fail-closed：弱 JWT secret 拒绝启动、空 `worker_token` 拒绝所有 Worker、CORS 仅放行 loopback、通知 webhook 屏蔽私网（dev 由 `TP_NOTIFY_ALLOW_PRIVATE=1` 放行）。
- 雪花 ID 在 JSON 里序列化为**字符串**（`id`/`*_id`/`tenant_id`），前端必须当字符串处理。
- fiber v3 坑：`SendStream` 的 body 归 fasthttp 所有（勿 `defer Close`）；`Next()` 之后才取 Route/Params。

### Worker（Python）

- 包：`src/testpilot_worker/`（服务）、`src/testpilot_sdk/`（沙箱内 SDK）、`src/testpilot/`（生成 proto）。入口 `main.py`；gRPC 客户端 `client.py`（心跳 10s、有界 outbox 4096 丢日志兜底、断线重连并清空旧会话事件、`asyncio.shield` 交付结果）。
- 引擎 `engine.py`：`_run_step` 按 `step.WhichOneof("params")` 分派步骤类型（api_call/assertion/code_block/ui_action/set_var/if/loop/retry/delay/grpc_call），步骤路径形如 `2.loop.1.1`。**只接受已解析的任务**——裸 `api_id`、`script_ref`、未解析 `binary_ref` 直接判失败（"must be resolved by scheduler"）；新增引用类型必须先在 Scheduler 侧解析。
- 沙箱：子进程跑 `testpilot_sdk.entry`，经 JSON-Lines 能力桥由 Worker 代执行 HTTP/变量/UI/按 API ID 调用（沙箱内零凭据）；启动即**抹掉全部 `*TOKEN*/*KEY*/*SECRET*/*PASSWORD*` 环境变量**；macOS `sandbox-exec` / Linux `bwrap` 尽力禁网，`TP_SANDBOX_REQUIRE_ISOLATION=1` 才强制。并行 loop 上限 16 并发/1000 总数、桥 64 并发/2000 日志行是防 DoS 的，勿随意放宽。
- `{{expr}}` 模板用 AST 白名单解释器（无函数调用/dunder，深度长度乘积护栏）；断言带 ReDoS 防护。
- 压测：Locust 必须留在独立子进程 `stress_runner.py`（gevent `monkey.patch_all` 与 asyncio 不得混用）；行为压测是常驻沙箱循环按 ramp 门控。

### Copilot（Python）

- `agent.py:build_agent()` 组装 pydantic-ai Agent：`output_type=[str, DeferredToolRequests]`——**漏掉 `DeferredToolRequests` 会让所有 HITL 写工具失效**。上下文压缩用 vendored 的 `ContextCompression`（阈值 0.7，summarizer 是第二个廉价 Agent；`pydantic_ai_extensions` 来自 `copilot/vendor/*.whl`，不在 PyPI，uv 从本地解析）。
- 工具（`tools.py`）是 Scheduler gRPC 薄封装，proto→dict 一律走线程池 `to_dict_async`（`MessageToDict` CPU 密集，别阻塞 SSE 循环）；`update_*` 工具是 get-then-merge 局部更新。
- `/api/chat`（`main.py`）：Bearer 鉴权 → Scheduler `/me` 解析身份 → 建/续会话 → `VercelAIAdapter.dispatch_request`。**鉴权窗口坑**：返回的是惰性 StreamingResponse，agent 在 body 迭代期才运行、异步生成器不继承 contextvar——`attach_auth_stream` 必须在流迭代窗口内 `auth_token.set(token)`，在 handler 里设置对工具不可见。
- 会话持久化走 Scheduler REST（role 1=user/2=assistant/3=tool）：行必须**串行**提交（雪花 ID 顺序即回放顺序）；tool call/result 按 `tool_call_id` 配对（**绝不用行 ID**，重复的 tool_call_id 会让 DeepSeek 下一条消息 400）；审批回执会重发全量历史，用户消息按内容去重。
- gRPC 无 channel 级 deadline，`_DeadlineProxy` 注入 30s 兜底；敏感 header 已脱敏才喂给 LLM。

### Web（React）

- 扁平 feature 结构，**无状态库/无 react-query**：`src/api.ts` 一个文件承载全部 REST 层（fetch 封装 + 实体类型 + localStorage `tp_token`/`tp_project`/`tp_env`）；SSE 用 `hooks/useEventStream`（fetch + ReadableStream，因需 Authorization 头）。
- 路由在 `App.tsx` 用 `createHashRouter`（hash 路由 → Scheduler 静态托管无需 history fallback）；必须是 data router（`useLeaveGuard` 依赖 `useBlocker`）。
- Copilot 对话页 `pages/Copilot.tsx`：Vercel AI SDK v7 `useChat`；版本与 Python 后端协议锁死，升级 `ai`/`@ai-sdk/react` 需同步 `pnpm-workspace.yaml` 的 release-age 排除并复查 `sanitizeMessages`（后端请求 schema `extra=forbid`，多发字段会被拒）。
- lint 是 **oxlint**（非 ESLint，仅 2 条 react 规则）；类型检查在 `pnpm build`；`verbatimModuleSyntax` 要求类型导入必须 `import type`；无路径别名，全部相对导入。
- antd v6：禁止静态 `message.xxx`，统一走 `src/messageBridge.ts`；颜色取 `theme.ts` 派生的 PALETTE，不手写色值。
- 发往后端的行不得携带额外字段（protojson 拒绝未知字段）。
- **改动未经明确要求不 commit，先迭代验收**（用户既定策略）。

### 配置与部署

三组件统一三级配置：CLI flag > `TP_*` env > YAML（`--config` / `TP_*_CONFIG` / CWD 默认）> 默认；逐键模板 `deploy/*.yaml.example`。生产用 `deploy/docker-compose.prod.yml`（PG + scheduler + worker×N + 独立 stress worker 池 + copilot + jaeger + prometheus）。开发机网络不稳时**先重试、勿换源**（Dockerfile 内 ARG 已配 npmmirror/goproxy.cn/tuna，与宿主镜像一致）。

## 工作流约定

- Commit：`<type>(<scope>): <subject>`；中文 subject 必须加 `English: <summary>` footer。
- 改 proto → `scripts/proto-gen.sh` → 生成产物随改动一起提交（CI 零漂移校验会拦）。
- 改数据模型 → `model.AllModels()` + 版本化迁移（SQLite/PG 双份幂等 SQL）+ 存量/新库测试，并手动同步 `docs/sql/*.sql`。
- CI（`.github/workflows/ci.yml`）= proto 治理 + scheduler（`go vet`/`go test`/`go build`）+ worker/copilot pytest + web lint/build。CD（`cd.yml`）在 `v*` tag 构建三个镜像推 ghcr.io。
- E2E 套件（均需 dev.sh 栈运行，用 `worker/venv/bin/python` 执行）：`scripts/e2e.py`（主流程）、`e2e_phase8.py`（RBAC/租户隔离/配额/OIDC/通知，自起 mock IdP）、`e2e_phase9.py`（metrics/审计）、`e2e_copilot.py`（真实 DeepSeek 调用，需 key）、`e2e_lowcode_api.py`（按 API ID 调用，另需 grpc-echo `:19090`）。
