# TestPilot 设计文档

> 本文档基于 `docs/init.md` 审阅与澄清结果细化而成。原始 `init.md` 存在事实性错误（H2 与 GORM 不兼容）、架构矛盾（Worker 语言、Copilot 协议、Playwright 构建开关）、核心概念缺失（测试用例/计划/套件、压测、报告、断言、多租户、认证）。本文档给出统一、可落地的设计方案。
>
> 已确认决策：
> 1. **Worker 运行时**：纯 Python Worker（grpcio 与 Scheduler 通信；低代码与 Playwright 原生集成）。
> 2. **用例模型**：声明式测试计划 + Python 低代码双轨，共享执行引擎与结果模型。
> 3. **Copilot 定位**：Agentic，经 Scheduler gRPC API 读写，不直连 DB；前端经 HTTP/SSE 消费其流式输出。
> 4. **压力测试**：专用分布式压测引擎，Locust 库模式发压，VictoriaMetrics 存时序指标，压测期间 Worker 独占。
> 5. **前端**：React + TypeScript + Vite + Ant Design + Vercel AI SDK（`@ai-sdk/react`），嵌入 Scheduler 托管。
> 6. **多租户**：共享 schema + `tenant_id`（long/number）隔离。
> 7. **Worker 租户模型**：混合模式（默认共享，大租户可独占绑定）。
> 8. **数据隔离**：仅应用层过滤（GORM 统一注入 tenant_id），不启用 DB 层 RLS。
> 9. **认证**：本地账号 + OIDC/OAuth2 双轨，外部身份源可插拔。
> 10. **部署**：本地单二进制/compose 调试，生产仅 docker-compose，提供 deployment 模板。
> 11. **低代码沙箱**：分层隔离后端 + 能力桥；v1 默认 subprocess 加固基线，可升级 gVisor/独占 Worker。
>
> 标注 `（提案，待确认）` 的条目为合理默认，可在评审后调整。所有 `（待澄清）` 项均已确认（见第 15 章），当前无挂起的架构分叉。
>
> **落地状态（2026-08-13）**：Phase 0–9 已全部交付，本文档的组件/模型/流程描述与实现一致，以下三处为实施期的务实替代（语义等价、接口预留，生产可平滑替换）：
> - 压测时序指标：VictoriaMetrics → **`stress_metric_points` 表**（单库即可查询/清理，报告页直接消费；换 VictoriaMetrics 只动存储适配层）。
> - 敏感变量保管：Vault → **应用层 redaction**（`variables` 表 sensitive 标记 + secret_ref 引用，读出即落 `secret_read` 审计；Vault 对接挂起到 v2 另议）。
> - 制品存储：对象存储 → **本地文件系统**（`TP_ARTIFACT_DIR` 共享目录，Scheduler/Worker 约定一致；对象存储后端在 v2 第二批）。
>
> 另：运行库 schema 由 GORM AutoMigrate 管理（非 golang-migrate）；`docs/sql/*.sql` 为生产 DDL 参考脚本。

---

## 目录

1. [概述](#1-概述)
2. [总体架构](#2-总体架构)
3. [领域模型](#3-领域模型)
4. [测试模型](#4-测试模型)
5. [执行与调度](#5-执行与调度)
6. [低代码 SDK 与运行时](#6-低代码-sdk-与运行时)
7. [Copilot](#7-copilot)
8. [压力测试](#8-压力测试)
9. [数据存储与安全](#9-数据存储与安全)
10. [集成能力](#10-集成能力)
11. [前端控制台](#11-前端控制台)
12. [构建、打包与版本](#12-构建打包与版本)
13. [运行支撑](#13-运行支撑)
14. [可观测性](#14-可观测性)
15. [开放问题](#15-开放问题)
16. [附录：与 init.md 的差异清单](#16-附录与-initmd-的差异清单)

---

## 1. 概述

### 1.1 目标
构建 LLM 参与的自动化集成测试平台，覆盖：
- HTTP / gRPC API 的能力测试与压力测试
- 基于 Playwright 的页面 E2E 测试
- 接口管理、环境与变量管理、证书管理、目录树组织
- 声明式（GUI 可编排）与 Python 低代码双轨测试用例
- AI Copilot 生成接口 / 用例 / 脚本
- 测试报告、历史、CI 集成
- 多租户隔离

### 1.2 组件
| 组件 | 语言 | 职责 |
|------|------|------|
| **Scheduler** | Go | 控制台后端（前端 CRUD）、数据存储、分布式调度、与 Copilot/Worker 交互、嵌入前端静态资源 |
| **Worker** | Python | 执行功能测试、低代码脚本、Playwright E2E、压测发压 |
| **Copilot** | Python（FastAPI + pydantic-ai） | AI Agent，生成接口/用例/脚本，经 Scheduler API 写入 |
| **Frontend** | TypeScript（React + Ant Design） | 控制台 UI、Copilot 流式交互 |

### 1.3 名词约定
- **TestPlan** 测试计划 / **TestCase** 测试用例 / **TestStep** 测试步骤 / **TestSuite** 测试套件
- **声明式用例**：由步骤树组成的 GUI 可编排用例
- **低代码用例**：基于 `testpilot-sdk` 的 Python 脚本用例
- **租户 Tenant**：隔离边界，`tenant_id` 为 long/number

---

## 2. 总体架构

### 2.1 部署拓扑

```
                              ┌────────────────────┐
                              │  Frontend (React)   │
                              │ embed in Scheduler  │
                              └───┬───────────┬────┘
              REST / WS / SSE     │           │   HTTP/SSE (Vercel AI SDK)
        ┌─────────────────────────┘           └──────────────────┐
        ▼                                                         ▼
┌───────────────────────────┐                          ┌──────────────────┐
│       Scheduler (Go)       │◄──────── gRPC ──────────│  Copilot         │
│  Console API + gRPC server │   (Copilot 作客户端调工具)│ (FastAPI+        │
│  cron + embed frontend     │                          │  pydantic-ai)    │
└───┬───────────────────┬───┘                          └──────────────────┘
    │                   │
    │ SQL (GORM)        │ gRPC 双向流（任务下发/结果·指标回传；Worker 主动注册）
    │                   │
    ▼                   ▼
┌─────────┐     ┌────────────────────────────┐
│ DB (PG) │     │    Worker Pool (Python)     │
│tenant_id│     │ functional/lowcode/         │
└─────────┘     │ playwright/stress(locust)   │
                └──┬──────────┬──────────┬───┘
                   │          │          │
        密钥解析    │          │压测指标   │产物写入
        (HTTP)    │          │remote    │(screenshot/
                   │          │write     │ trace/har)
                   ▼          ▼          ▼
            ┌───────────┐┌──────────────┐┌────────────┐
            │Vault/Tink ││VictoriaMetrics││ Artifacts  │
            │ 租户路径   ││  (压测时序)    ││ (S3/local) │
            └───────────┘└──────────────┘└────────────┘
                               ▲               ▲
                Scheduler 查询压测报告    Scheduler 读产物供前端展示
                (VM query API)          (artifact serve)
```

> 要点：
> - **Worker 与 Copilot 都是 Scheduler 的直连下游**：Worker 主动注册到 Scheduler 的 gRPC server；Copilot 作为 gRPC 客户端调 Scheduler 工具。Worker 不经过 Copilot。
> - **DB 只属于 Scheduler**；Worker 不直连业务库，任务与数据经 gRPC 下发。
> - **读回路径**：压测报告由 Scheduler 查询 VictoriaMetrics 聚合；产物由 Worker 写入、Scheduler 读取后供前端展示/下载。

### 2.2 协议矩阵

| 链路 | 协议 | 用途 |
|------|------|------|
| Frontend ↔ Scheduler | REST/HTTP + WebSocket/SSE | CRUD、实时运行状态 |
| Frontend ↔ Copilot | HTTP/SSE（Vercel AI SDK Data Stream Protocol） | Copilot 流式输出，`@ai-sdk/react` 消费 |
| Copilot ↔ Scheduler | gRPC（带 tenant 上下文） | 工具调用，读写业务数据 |
| Scheduler ↔ Worker | gRPC 双向流（带 tenant 上下文） | 任务下发、结果/指标回传 |
| Scheduler ↔ DB | SQL（GORM） | 业务数据持久化 |
| Worker ↔ Vault | HTTP（Vault API） | 执行期密钥解析（租户化路径） |
| Worker ↔ VictoriaMetrics | remote write / API | 压测时序指标写入 |
| Scheduler ↔ VictoriaMetrics | query API | 压测报告聚合读取 |
| Worker ↔ Artifacts | S3 / 本地 FS | 产物写入（截图/trace/har） |
| Scheduler ↔ Artifacts | S3 / 本地 FS | 产物读取，供前端展示/下载 |

> **协议澄清**：`init.md`"FastAPI 暴露 Vercel AI 兼容接口与 Scheduler 交互"表述有误。Vercel AI SDK 流式协议是**前端↔Copilot**通道；Copilot↔Scheduler 控制面走 gRPC。

### 2.3 技术栈
- **Scheduler**：Go 1.22+、gRPC、GORM、golang-migrate、robfig/cron
- **Worker**：Python 3.13+、grpcio、asyncio、httpx、playwright、locust
- **Copilot**：Python 3.13+、FastAPI、pydantic-ai
- **Frontend**：React + TypeScript + Vite + Ant Design + Vercel AI SDK（`@ai-sdk/react`）+ React Router + 状态管理（Zustand/TanStack Query，`提案，待确认`）
- **共享契约**：Protocol Buffers（Scheduler/Worker/Copilot 共用 proto，生成 Go 与 Python 桩）
- **DB**：PostgreSQL（主）、MySQL（备）、SQLite（本地/开发）
- **时序库**：VictoriaMetrics（压测指标）

### 2.4 多租户模型
- 共享 schema + `tenant_id`（`long/number`，建议 snowflake 生成以避免分布式自增冲突）。
- **所有业务实体携带 `tenant_id`**：Project、Environment、Variable、HttpApi、GrpcApi、Certificate、TreeNode、TestCase、TestPlan、TestRun、Worker 注册（可选绑定）等。
- **数据隔离**：所有查询强制按 `tenant_id` 过滤（仅应用层过滤，GORM 中间件统一注入 tenant_id，不启用 DB 层 RLS）。
- **租户成员**：`tenant_member(user_id, tenant_id, role)`；用户可属多租户，每租户独立角色；登录后选择/切换租户上下文。
- **Worker 租户模型**：混合模式--默认共享 Worker（跨租户复用、任务级 tenant 隔离），大租户可绑定独占 Worker（注册时带 tenant_id，只跑该租户任务）。
- **Copilot**：所有工具调用经 gRPC metadata 携带 tenant 上下文，RBAC 按租户生效。
- **Vault**：密钥路径租户化 `vault://tenant/{tenant_id}/...`。
- **配额**：租户级资源限额（见 13.3）。

---

## 3. 领域模型

> 以下实体均携带 `tenant_id`（除 User/Tenant 本身），下文不再重复标注。

### 3.1 项目 Project
```
Project
  id, tenant_id, name, description
  rbac_assignments[]        # 见 9.3
  config                    # 项目级配置（默认超时、并发等）
  created_at, updated_at
```

### 3.2 环境 Environment
项目维度，一对多。
```
Environment
  id, tenant_id, project_id
  icon, name, description
  base_url                  # 前置 URL
  variables[]               # 环境变量
```

### 3.3 变量体系
支持**项目维度**与**环境维度**。
```
Variable
  id, tenant_id, project_id, environment_id?(null=项目级)
  scope: project | environment
  category: header | cookie | query | body | custom
  key, value
  sensitive: bool           # 是否敏感
  secret_ref?               # vault://tenant/{tid}/... 或 tink 密文引用
```
- HTTP 下 `category=body` 仅对 `form-data` / `x-www-form-urlencoded` 有效。
- 敏感变量值不入库明文，见 9.2。

### 3.4 接口结构

#### HTTP 接口
```
HttpApi
  id, tenant_id, project_id
  method                    # GET/POST/...
  uri                       # 可含 {{var}}
  params[]                  # Query
  body: BodySpec
  headers[]
  cookies[]                 # name, value, type, description
  pre_scripts[]             # 前置操作
  post_scripts[]            # 后置操作（含断言）
  settings:
    tls_verify, follow_redirects
    comment_tolerant_json   # 兼容带注释 JSON
    timeout_ms
  certificate_id?
```
```
BodySpec
  content_type: none | form-data | x-www-form-urlencoded | json | xml | binary | graphql
  raw?                       # json/xml/graphql 原文
  fields?                    # form-data 字段表
  binary_ref?                # 二进制 artifact 引用
```

> **URL 解析**：实际请求 URL = `Environment.base_url` + `HttpApi.uri`（uri 可含 `{{var}}` 模板）。gRPC 无 base_url 概念，目标为 `host:port`（由环境/接口的 tls_settings 决定明文或 TLS）。

#### gRPC 接口
```
GrpcApi
  id, tenant_id, project_id
  proto_ref                  # 上传文件 / server reflection
  full_service               # package.Service
  method
  request_message            # JSON 表示
  metadata[]                 # key/value
  deadline_ms
  tls_settings
  pre_scripts[], post_scripts[]
  certificate_id?
```

### 3.5 证书 Certificate
统一存储，可用于项目/接口维度。
```
Certificate
  id, tenant_id, project_id
  name, description
  type: pem | p12 | ...
  cert_ref                   # artifact 引用或密文
  key_ref
  password_secret_ref?       # 口令走 Vault/Tink
```

### 3.6 目录树
项目下接口/用例/计划以目录树组织，每层可含目录与节点。
```
TreeNode
  id, tenant_id, project_id
  parent_id                  # null=根
  node_type: folder | http_api | grpc_api | test_case | test_suite | test_plan
  ref_id                     # 指向对应实体（folder 为 null）
  name, icon, order          # 同级排序
  path                       # 物化路径（如 proj.a.b），便于子树查询
```
- 采用**邻接表 + 物化路径**，兼顾移动子树与子树查询。
- **实体挂树方式**：统一经 `TreeNode.ref_id -> 实体` 单向关联（树节点持有实体引用）；实体不反向存 `tree_node_id`，避免双向指针不一致。

### 3.7 前置/后置脚本
用于断言与变量设置：
- **断言**：见 4.5
- **变量设置**：`set_var("token", response.body.token)`
- 声明式用例中以 `CODE_BLOCK` 步骤承载；低代码用例中直接写在 Python 脚本内。

---

## 4. 测试模型

> 这是 `init.md` 完全缺失的核心部分。

### 4.1 实体关系
```
TestSuite ──< TestCase >── TestPlan
                   │
        ┌──────────┴──────────┐
   declarative            lowcode
   (steps[])         (python script)
        │
   TestStep (API_CALL | GRPC_CALL | ASSERTION | SET_VAR |
             IF | LOOP | RETRY | CODE_BLOCK | DELAY | UI_ACTION)
```

> **TestSuite vs TestPlan**：TestSuite 是**可复用的静态用例集合**（无环境/调度），用于分组与复用；TestPlan 是**可运行编排**（绑定环境、并发、定时、通知）。TestPlan 的 items[] 可引用单个用例或整个 TestSuite。

### 4.2 TestCase
```
TestCase
  id, tenant_id, project_id
  type: declarative | lowcode
  name, description
  # declarative: steps[]
  # lowcode: script_ref + entry + parameters
  tags[]
  created_by: human | copilot
```

### 4.3 TestPlan
```
TestPlan
  id, tenant_id, project_id, env_id
  name
  items[]                    # 有序引用 TestCase 或 TestSuite，支持启用/禁用、参数覆盖
  concurrency                # 用例间并发度
  retry_on_failure
  overlap_policy: skip | queue | run   # 上次未跑完时定时触发的重叠策略
  schedule_cron?             # 定时触发
  timeout_ms
  notifications?             # 见 13.2
```

### 4.4 TestStep（声明式用例的步骤）
```
TestStep
  id, type, name            # id 用于 step_path 定址（嵌套为点路径，如 3.then.1）
  # 按 type 携带 params：
  API_CALL:    api_id, overrides(method/uri/headers/body/params)
  GRPC_CALL:   grpc_api_id, overrides
  ASSERTION:   assertions[]
  SET_VAR:     key, value_expr
  IF:          condition_expr, then_steps[], else_steps[]
  LOOP:        iterator, count|range, parallel?, body_steps[]   # 默认顺序；parallel 并发
  RETRY:       body_step, max_attempts, backoff_ms              # 包装并重试单步
  CODE_BLOCK:  lang=python, source
  DELAY:       ms
  UI_ACTION:   action, target(locator), value?                  # 见下
```
- 步骤树可嵌套（IF/LOOP 内含子步骤）。执行器维护 **运行上下文**（变量、当前 Response、环境），步骤间共享。
- **表达式语言**：模板插值 `{{var}}` / `{{env.X}}` / `{{response.body.id}}`；条件与取值用受限表达式（JSONPath + 安全求值器，非任意 `eval`），可引用 `$response`（上一步响应）、`$ctx.vars`、`$loop`。
- **UI_ACTION 动作集**：`goto | click | fill | select | check | hover | press | expect_text | expect_visible | screenshot | wait | upload | download`；`target` 为 Playwright locator 表达式。

**声明式步骤示例：**
```json
[
  {"type":"API_CALL","name":"创建用户","api_id":"api_1",
   "overrides":{"body":{"name":"{{user_name}}"}}},
  {"type":"SET_VAR","key":"userId","value_expr":"$response.body.id"},
  {"type":"ASSERTION","assertions":[
     {"target":"status","op":"eq","expected":201},
     {"target":"jsonpath","path":"$.id","op":"exists"}
  ]},
  {"type":"LOOP","iterator":"i","range":[0,5],"body_steps":[
     {"type":"API_CALL","name":"查询","api_id":"api_2"}
  ]}
]
```

### 4.5 断言体系
统一断言结构，声明式与低代码共用结果记录。
```
Assertion
  target: status | header | body | jsonpath | elapsed | custom
  path?                       # header名 / jsonpath
  op: eq | ne | exists | not_exists | contains | matches | gt | lt | ge | le | type_is
  expected?
```
低代码侧提供 `assert_*(...)` 辅助函数与 Python 原生 `assert`，二者都产出 `AssertionResult`。

### 4.6 执行引擎
单一引擎同时驱动声明式与低代码：
1. 加载 TestPlan -> 展开 TestCase 序列（按 concurrency 并发）
2. 对每个 TestCase：
   - **declarative**：遍历步骤树，按 type 执行；维护运行上下文
   - **lowcode**：交由 Python 执行器（见 6）运行脚本，SDK 内部复用同一 HTTP/gRPC/Playwright 客户端
3. 收集 `TestStepResult` / `AssertionResult` / 日志 / 产物
4. 汇总为 `TestCaseResult` -> `TestRun`

### 4.7 结果与报告
```
TestRun
  id, tenant_id, plan_id, env_id, status(running|passed|failed|aborted)
  trigger: manual | scheduled | ci      # 触发方式
  triggered_by: user_id | token | scheduler
  started_at, finished_at, summary{total, passed, failed, skipped}

TestCaseResult
  run_id, case_id, status, duration_ms, error?

TestStepResult
  case_result_id, step_path, status, duration_ms
  request?  response?          # 序列化的请求/响应快照
  assertions[]                 # AssertionResult[]
  logs[], artifacts[]          # artifact: screenshot/trace/har
```
- **报告视图**：单次运行详情、用例/步骤/断言三级下钻、趋势图（历史通过率/耗时）。
- **CI 集成**：REST `POST /runs` 触发 + 轮询/Webhook 回调；CLI `testpilot run <plan>`。

### 4.8 运行上下文与变量作用域
- 作用域优先级（高→低）：步骤局部 > 用例局部 > 计划级 > 环境级 > 项目级；同名就近覆盖
- `set_var` 默认写入用例局部作用域
- 并发执行的用例拥有**隔离运行上下文**；计划级变量在计划启动时快照、用例间只读共享，避免并发写冲突

---

## 5. 执行与调度

### 5.1 Worker 生命周期
1. Worker 启动 -> 通过 gRPC 向 Scheduler 注册
2. 上报**能力声明**：
   ```
   capabilities: [functional, lowcode, playwright, stress]
   python_version, sdk_version
   tags[]                      # 如 region=cn, env=prod
   max_concurrency
   tenant_id?                  # 独占模式绑定租户；null=共享
   ```
3. 建立双向流，进入空闲等待
4. 心跳保活；超时标记 offline

### 5.2 调度策略
- **租户隔离**：任务携带 tenant_id；独占 Worker（带 tenant_id）只跑该租户任务，共享 Worker 跨租户但任务级隔离
- **能力路由**：按任务所需能力筛选 Worker（Playwright 任务只分给带 `playwright` 的 Worker）
- **负载均衡**：在合格 Worker 中按当前并发度选最闲
- **亲和性**：可选标签匹配（region/env）
- **配额校验**：调度前校验租户配额（见 13.3）
- **压测独占**：压测任务按需占用 N 个 Worker，期间这些 Worker 不接受功能任务（已确认）

### 5.3 任务下发与回传
- Scheduler 经 gRPC 双向流下发 `Task`（含用例/环境/变量/密钥引用，**不含密钥明文**）
- Worker 执行并流式回传：步骤进度、日志、结果、产物引用
- 产物写入对象存储（local/S3），DB 只存引用
- 压测指标流式写入 VictoriaMetrics

### 5.4 运行状态机
```
Triggered -> Queued -> Dispatched -> Running -> Collecting -> Completed
                                       │
                                       └── Failed / Aborted / Timeout
```
- 前端经 WebSocket/SSE 订阅实时进度。
- **取消/超时传播**：取消经 gRPC 下发 Worker，Worker 终止对应执行子进程；超时由 Scheduler 计时触发，同样下发取消。

---

## 6. 低代码 SDK 与运行时

### 6.1 testpilot-sdk
独立 Python 包，Pydantic 模型由共享 proto 生成，与 Scheduler 领域模型一致。
```python
class Response(BaseModel):
    status: int
    headers: dict[str, str]
    body: Any
    elapsed_ms: int

class HttpAPI(BaseModel, ABC):
    method: str
    uri: str
    headers: dict[str, str] = {}
    # ...其余字段
    @abstractmethod
    async def run(self) -> Response: ...

class GrpcAPI(BaseModel, ABC):
    service: str
    method: str
    request: dict
    @abstractmethod
    async def run(self) -> Response: ...
```
- Pydantic + ABC 实现"字段验证 + 方法契约"双重约束。
- 内置 `CreateUser(HttpAPI)` 等可由 Copilot 生成或由接口定义自动派生。

### 6.2 用户用例形态
```python
from testpilot_sdk import HttpAPI, Response, assert_that

class CreateUser(HttpAPI):
    """对应 CreateUser 接口"""
    method = "POST"
    uri = "/users"

async def run(ctx):
    req = CreateUser(body={"name": ctx.vars["user_name"]})
    result: Response = await req.run()
    assert_that(result.status).eq(201)
    ctx.set_var("userId", result.body["id"])
```
- 入口 `run(ctx)` 由 Worker 调用；`ctx` 注入环境、变量、断言工具、租户上下文。

### 6.3 运行时与隔离（沙箱）
采用**分层隔离后端 + 能力桥**模型：隔离强度可按租户/阶段升级，而无需改动 SDK 与执行引擎。

**威胁模型**：低代码脚本是租户成员编写的**半可信**代码，需防——Worker 崩溃/卡死、资源耗尽、跨租户数据泄露、SSRF（见 9.5）、密钥泄漏、读取 Worker 凭据/文件。

**杠杆一：可插拔执行后端**（`ExecutionBackend` 统一接口，后端可切换）：
| 后端 | 隔离强度 | 开销 | 适用 |
|------|---------|------|------|
| `subprocess`（默认） | 进程级 | 低 | 单租户/可信团队、v1 |
| `container`（gVisor/Kata） | 容器级（独立内核视图/FS/网络） | 中 | 多租户共享 Worker |
| `dedicated`（独占 Worker） | 主机级 | 高 | 高隔离租户（已有能力） |

**杠杆二：能力桥（capability bridge，推荐）**。沙箱内只跑用户脚本的**编排逻辑**，SDK 在沙箱内是**瘦客户端**，把每个副作用操作（HTTP/gRPC 调用、Playwright 动作、取变量/密钥）经受控 IPC 转发给 Worker 执行。由此沙箱进程**默认无网络、无文件系统、无密钥明文**，所有出口与密钥替换由 Worker 统一管控——即便用最弱的 subprocess 后端也能获得较强的效果隔离。代价是每次操作多一次 IPC 往返（操作粗粒度，开销可接受）。

**subprocess 后端加固基线（v1）**：
- 以独立**非特权 OS 用户**运行（非 Worker 用户），`preexec` setuid/setgid
- `resource.setrlimit`：CPU 秒、内存（RLIMIT_AS）、进程数（RLIMIT_NPROC 防 fork 炸弹）、文件描述符、文件大小
- 超时强杀；崩溃不波及 Worker
- **环境 scrub**：子进程不继承 Worker 环境变量 / gRPC 凭据 / Scheduler token；变量与密钥经 SDK 通道显式注入
- 独立 scratch 工作目录（tmpfs），不可见 Worker 文件与其他运行产物
- 可选 seccomp-bpf 收敛系统调用

**升级路径**：共享 Worker 的多租户隔离需求增强时，后端切到 `container`（gVisor）；需最强隔离的租户直接用**独占 Worker**（通常比引入 Firecracker microVM 更简单、且能力已具备）。**不建议**语言级沙箱（RestrictedPython 等）——Python 无法可靠地在进程内沙箱化。

### 6.4 模型同步
- 共享 proto 为单一事实源；CI 生成 Go 结构与 Python Pydantic 模型
- SDK 版本化；Worker 注册时上报 `sdk_version`，Scheduler 版本不匹配时告警

### 6.5 可行性分析
- **Pydantic + ABC 组合**：可行。Pydantic v2 的 `BaseModel` 可与 `ABC` 共存，`@abstractmethod` 在未实现前阻止实例化，实现"字段验证 + 方法契约"双重约束。
- **asyncio**：SDK 全链路 async（httpx / grpc / playwright 均原生 async），Worker 以单事件循环驱动，契合 3.13+ asyncio。
- **子进程隔离**：`asyncio.create_subprocess_exec` 拉起独立 Python 进程执行用户脚本；POSIX 下用 `resource.setrlimit` 限 CPU/内存，超时强杀。崩溃不波及 Worker 主进程。
- **模型生成（注意点）**：protoc 直接产出的是 protobuf message 类，**不是** Pydantic 模型。推荐路径：proto → JSON Schema → `datamodel-code-generator` → Pydantic；或将 SDK 领域模型以 Python 为单一事实源、再生成 proto。可执行的 `HttpAPI/GrpcAPI/Page` 封装为 SDK 手写代码，纯数据模型自动生成。

### 6.6 能力范围
低代码可表达：
- 任意流程编排：顺序、条件、循环、重试、并行（`asyncio.gather`）
- 调用任意 HttpAPI / GrpcAPI，全参数控制（含动态生成参数）
- 变量读写、环境访问、跨步骤数据流
- 断言（`assert_that` 辅助 + 原生 `assert`）
- **API + UI 混合**：同一脚本内既调接口又用 Playwright 驱动页面（SDK `Page` 模型）
- 数据驱动：生成/循环数据集（faker、边界值、等价类）
- 任意 Python 逻辑：计算、转换、调用第三方库
- 生命周期钩子：case 级 setup / teardown
- 复用：自定义可复用 API 类与辅助函数

### 6.7 边界（不做什么）
- 不承担压测发压（由 Locust 负责）；但低代码脚本可作为压测的"用户行为"目标
- 非通用计算沙箱，受 6.3 资源与安全边界约束
- 纯确定性、GUI 可编排的流程优先用声明式；低代码用于复杂逻辑 / 混合场景

---

## 7. Copilot

### 7.1 架构
- pydantic-ai Agent，工具调用经 Scheduler gRPC API 读写（工具以 `FunctionToolset` 组织）
- 前端经 HTTP/SSE（Vercel AI SDK Data Stream Protocol）消费流式输出，`@ai-sdk/react` 渲染
- **不直连 DB**，所有写入经 Scheduler API，可审计
- 每次会话绑定 tenant 上下文 + 用户身份，工具调用受 RBAC 约束
- **流式协议适配**：pydantic-ai 提供官方 Vercel AI 适配（`pydantic_ai.ui.vercel_ai`，将 `run_stream_events` 事件流转为 Data Stream Protocol），前端用 `@ai-sdk/react` 直接消费，无需手写协议映射，降低集成风险

### 7.2 能力范围判定
Copilot 不止生成接口与脚本，其能力覆盖**生成、分析、调试、运维、知识、编排**六大类（标注读写权限与落地阶段）：

| 类别 | 能力 | 说明 | 读写 | 阶段 |
|------|------|------|------|------|
| 生成 | 接口生成/改造 | 从 OpenAPI/curl/自然语言生成 HTTP/gRPC 接口 | 写 | v1 |
| 生成 | 声明式用例 | 生成步骤树（API_CALL/ASSERTION/控制流） | 写 | v1 |
| 生成 | 低代码脚本 | 生成/改造 testpilot-sdk Python 脚本 | 写 | v1 |
| 生成 | Playwright 步骤/脚本 | 生成 UI_ACTION 步骤或低代码 Page 脚本 | 写 | v1 |
| 生成 | 测试计划/套件 | 组装、修改 TestPlan/TestSuite | 写 | v1 |
| 生成 | 断言规则 | 由接口/响应样例生成断言 | 写 | v1 |
| 生成 | 测试数据 | faker/边界值/等价类数据集 | 写 | v1 |
| 生成 | 环境/变量配置 | 生成环境、变量、密钥引用 | 写 | v1 |
| 生成 | 批量生成 | 从 OpenAPI 一键生成整套接口+用例 | 写 | v1 |
| 分析 | 失败根因分析 | 读 TestRun/步骤结果/日志/产物，定位失败原因 | 读 | v1 |
| 分析 | 覆盖率分析 | 接口 vs 用例覆盖缺口（OpenAPI 比对） | 读 | v1 |
| 分析 | 结果摘要 | 生成自然语言运行摘要（报告/通知用） | 读 | v1 |
| 分析 | 回归对比 | 跨版本运行结果 diff，识别回归 | 读 | v2 |
| 分析 | 压测报告解读 | 解读 VictoriaMetrics 时序，定位瓶颈 | 读 | v2 |
| 分析 | flaky 检测 | 从历史运行识别不稳定用例 | 读 | v2 |
| 调试 | 对话式调试 | 粘贴错误/日志，解释并给修复建议 | 读 | v1 |
| 调试 | 断言建议 | 给定响应样例，建议断言 | 读 | v1 |
| 调试 | 单接口试跑 | 触发单接口运行并解释响应 | 写(触发) | v2 |
| 调试 | 变量引用检查 | 检测未定义/未使用变量 | 读 | v2 |
| 运维 | OpenAPI diff | 导入新 spec 比对现有接口，标记 breaking change 并建议同步用例 | 读+写 | v2 |
| 运维 | 环境配置诊断 | 检测缺失变量/证书/错误 base_url | 读 | v2 |
| 运维 | 调度建议 | 按变更频率推荐 schedule_cron | 读 | v2 |
| 知识 | 接口目录问答 | 自然语言查询接口/用例 | 读 | v2 |
| 知识 | 语义搜索 | 跨用例语义检索 | 读 | v2 |
| 编排 | 数据流分析 | 跨用例变量依赖分析、依赖排序 | 读 | v2 |

> v1 = 首版落地；v2 = 后续迭代。读类能力免审批；写/触发类默认 HITL 审批（见 7.5）。

### 7.3 工具集
工具以 `FunctionToolset` 分组注册，按读写拆分以支持审批策略：
- **只读工具**（免审批）：`list_projects` / `list_apis` / `list_environments` / `get_api` / `get_test_case` / `query_schema` / `list_runs` / `get_run` / `query_coverage` 等
- **写工具**（默认 HITL 审批）：`create_api` / `update_api` / `create_test_case` / `create_test_plan` / `import_openapi` / `apply_openapi_diff` 等
- **触发工具**（默认 HITL 审批）：`trigger_run` / `trigger_stress` 等

### 7.4 Grounding（构建期 DDL 的澄清）
`init.md`"构建时把 DDL 写入工作目录"重新定义为**领域 schema grounding**：
- **构建期**：从 Go 结构/proto 生成 `schema.json` + 数据字典，打入 Copilot 产物，作为静态 grounding
- **运行期**：经 `query_schema` 等工具查询活态 schema 与业务数据，应对 schema 演进
- 二者结合：静态 grounding 保证模型一致性，运行时工具保证数据时效性
- **低代码 grounding**：除领域 schema 外，还需注入 testpilot-sdk 的 Python API 面（`HttpAPI`/`GrpcAPI`/`Page`/`assert_that` 等的签名与示例），保证生成脚本可运行

### 7.5 安全与人机协同
- **Human-in-the-loop**：pydantic-ai 支持工具审批。所有**写操作**与**触发运行**默认需用户在前端确认后才执行；只读操作免审批。兼顾 Agentic 能力与可审计性。
- **审计**：所有写工具调用记录（发起=Copilot，审批=用户，tenant 上下文）。
- **权限**：工具调用以用户身份 + 租户上下文经 Scheduler 执行，受 RBAC 约束。
- **MCP**（`提案，待确认` v2）：pydantic-ai 支持 MCP。Copilot 可将工具集暴露为 MCP server（供 IDE/其他 Agent 调用），或消费外部 MCP server 扩展能力。

### 7.6 产物形态与运行
- FastAPI 服务，暴露 `/chat`（SSE 流）与 `/tools` 元数据
- 鉴权复用前端会话 token，工具调用带 tenant 上下文以应用 RBAC
- **会话持久化**：对话按用户/租户落库，支持历史回放与上下文续接
- **模型配置**：LLM Provider/模型可配置（pydantic-ai 模型无关）；API Key 经 Vault 管理；按租户配置 AI 调用配额（见 13.3）

---

## 8. 压力测试

### 8.1 模型
```
StressTestPlan
  id, tenant_id, project_id, env_id
  target: api_id | test_plan_id      # 压测目标
  load_profile:
    ramp: [(at_seconds, rps_or_users)]   # 阶梯加压
    duration_seconds
    concurrency_per_worker
  worker_count                       # 发压 Worker 数
  metrics_interval_ms                # 采样间隔
```

> **压测目标范围**：限定为**单接口或简单用户流**（Locust user behavior）。不直接对含复杂控制流的 TestPlan 发压（并发负载下控制流语义复杂）；若需压流程，将其转为一个低代码"用户行为"脚本作为压测目标。

### 8.2 执行（Locust 库模式）
- **Locust 以 headless 库模式集成**：每个压测 Worker 独立调用 Locust 程序化接口承担 `concurrency_per_worker` 负载，**不启用 Locust 自带 master-web**，避免双调度层。
- **运行时注意**：Locust 基于 **gevent（greenlet）**，与 Worker 的 asyncio 事件循环不同。压测引擎须在**独立子进程**（自带 gevent loop）中运行，不可混入 asyncio 主循环，避免并发模型冲突。
- Scheduler 拆分总负载到 N 个 Worker（`worker_count`），每个 Worker 等量或按权重分担。
- Worker 流式回传时序指标（RPS、延迟 p50/p95/p99、错误率、并发数）至 VictoriaMetrics。
- **压测独占**（已确认）：被选中的 Worker 在压测期间标记独占，不接受功能任务。

### 8.3 指标存储与报告
- 时序存储：**VictoriaMetrics**（已确认），Worker 经 remote write 或 API 写入
- 报告：RPS/延迟/错误率时间序列图、SLA 达标判定；与功能测试结果分离存储但共享目标定义
- 报告查询经 Scheduler 聚合 VictoriaMetrics 数据呈现

---

## 9. 数据存储与安全

### 9.1 数据库
- **PostgreSQL**（生产主选）、**MySQL**（备选）、**SQLite**（本地/开发）
- 经 GORM 访问；迁移用 golang-migrate 管理版本化 SQL（生产），开发期可用 AutoMigrate
- **移除 H2**（与 GORM/Go 不兼容）
- 所有业务表含 `tenant_id` 字段并建立联合索引 `(tenant_id, ...)`

### 9.2 密钥管理（Vault / Tink）
- **HashiCorp Vault**（外部密钥源）：敏感变量引用 `vault://tenant/{tenant_id}/path`
- **Google Tink**（本地信封加密）：无外部 Vault 时，敏感字段以 Tink 加密入库
- 解析时机：Worker 执行期解析，明文仅在内存；DB 不存明文
- 证书私钥、接口口令同等处理；密钥路径租户隔离

### 9.3 RBAC
租户级角色 + 系统级角色：
| 角色 | 权限范围 |
|------|----------|
| Owner | 租户内全部，含人员管理 |
| Maintainer | 接口/用例/计划/环境 CRUD、运行 |
| Tester | 用例/计划编辑、运行、查看 |
| Viewer | 只读 |
| Admin（系统） | 跨租户管理、Worker 管理、系统配置 |

- 权限矩阵覆盖：项目/接口/用例/计划/环境/运行/Copilot/密钥读取
- 敏感变量读取独立权限（Tester 默认不可见明文）
- 角色通过 `tenant_member` 绑定，用户在 不同租户可有不同角色

### 9.4 认证与用户管理
- 用户体系：User 独立于租户，经 `tenant_member` 关联多租户
- **认证双轨**：
  - 本地账号：用户名/密码（bcrypt），自管用户体系，用于初始/运维
  - 外部身份源：OIDC / OAuth2 Provider（Keycloak、Google、GitHub、Auth0 等），可插拔多 Provider
- **身份提供方抽象**：`identity_provider` 表记录 Provider 类型与配置（issuer/client_id/scopes/字段映射等），登录入口按 Provider 路由
- 会话：JWT（access + refresh）；前端嵌入 Scheduler 复用其会话
- API Token：为 CI/CLI 颁发租户级 token（独立于登录会话）
- 外部用户映射：外部身份登录后按邮箱/用户名映射或创建本地 User，再由管理员分配租户与角色

### 9.5 网络出口控制（SSRF 防护）
- Worker 会发起用户指定的任意 HTTP/gRPC 请求并执行低代码 Python，多租户下存在 **SSRF 风险**（Worker 可能访问内网/云元数据等）。
- 措施：Worker 部署在受限网段，配置**出口白名单/代理**；阻断 link-local/云元数据地址（169.254.169.254 等）与内网保留段；共享 Worker 强制走出口代理。
- 该边界与低代码沙箱（15.1）共同构成执行安全模型。

---

## 10. 集成能力

### 10.1 导入
- OpenAPI 3.x -> HttpApi 批量生成
- Postman Collection v2.1、Apifox 导出
- 单个 curl 命令 -> HttpApi
- gRPC：proto 文件上传 / server reflection -> GrpcApi

### 10.2 导出
- 接口 -> OpenAPI 3.x / curl / Postman Collection
- 项目/环境 -> JSON bundle（可再导入）
- 测试计划 -> JSON
- 测试报告 -> HTML / JUnit XML（CI 友好）

### 10.3 Playwright（E2E）

#### 交互逻辑
- **Locator + 自动等待**：经 `page.locator()` 定位；动作前自动等待元素可操作（可见/稳定/可用），降低 flakiness
- **Web-first 断言**：`expect(locator).to_be_visible()` 等带自动重试
- **动作集**：导航、点击、填表、选择、勾选、悬停、键盘、拖拽、上传/下载
- **上下文隔离**：每次运行在独立 `BrowserContext`（cookies/storage 隔离，类隐身窗口）
- **网络拦截**：`page.route()` mock/阻断/打桩请求

#### 能力范围
- 跨浏览器：Chromium / Firefox / WebKit
- 设备模拟：视口、移动设备、地理位置、locale、时区、权限、配色
- 会话：storageState 保存/加载（复用登录态）、cookies、localStorage
- 网络：HAR 录制、请求拦截/mock、离线模式、HTTP 认证、客户端证书、代理
- 产物：截图、video、trace（DOM 快照+动作+网络）、下载文件
- 多页签、iframe、shadow DOM

#### 运行模式
- **Worker 服务端**：headless；浏览器进程**每用例启动一次**（非每步骤），用例内步骤复用同一 Browser，每用例独立 BrowserContext 隔离，采集 trace/video/screenshot 为产物
- **调试回放**：失败运行导出 trace.zip，前端嵌入 Trace Viewer 回放（DOM 快照+网络+动作时间线）
- **录制 bootstrap**（`提案`）：本地用 Playwright `codegen` 录制交互，导入为 UI_ACTION 步骤或低代码脚本，降低 E2E 起步成本
- headed 模式仅本地调试，不用于服务端 Worker

#### 表达形式
- 声明式 `UI_ACTION` 步骤：动作子集暴露为数据 `{action, target(locator), value?}`
- 低代码 SDK `Page` 模型：`async def run()` 内自由编排
- **slim 变体**：`testpilot-worker` 不含 Playwright；`testpilot-worker[playwright]` 含浏览器与依赖（见 12）

### 10.4 CI 集成
- REST：`POST /api/v1/runs`（plan_id, env_id）触发，返回 run_id
- 状态：轮询 `GET /runs/{id}` 或 Webhook 回调
- CLI：`testpilot run <plan> --env <env> --wait`，退出码反映通过状态
- 报告：JUnit XML 输出供 CI 平台展示

---

## 11. 前端控制台

### 11.1 技术栈
- React + TypeScript + Vite + Ant Design
- React Router（路由）
- 状态管理：Zustand + TanStack Query（服务端状态）（`提案，待确认`）
- Vercel AI SDK（`@ai-sdk/react`）：消费 Copilot SSE 流，渲染流式消息与工具调用过程

### 11.2 核心模块
- 租户切换 / 项目 / 环境 / 变量管理
- 接口管理（HTTP/gRPC）+ 目录树
- 测试用例编辑器（声明式步骤编排器 + 低代码代码编辑器 Monaco）
- 测试计划与运行
- 运行报告（三级下钻、趋势、产物预览）
- 压测报告（VictoriaMetrics 时序图表）
- Copilot 对话面板（侧栏，可引用当前上下文生成）

### 11.3 托管
- 前端构建产物经 Go `embed` 嵌入 Scheduler 二进制，由 Scheduler 统一托管（已确认）；也可独立部署（可选）

---

## 12. 构建、打包与版本

### 12.1 产物
| 产物 | 形态 | 说明 |
|------|------|------|
| Scheduler | Go 二进制 + 迁移 SQL + embed 前端 | 单服务，含控制台 API、gRPC server、cron、前端静态资源 |
| Worker | Python 包 | `testpilot-worker`（functional+lowcode+stress）与 `testpilot-worker[playwright]`（含 E2E） |
| Copilot | Python 包 | FastAPI 服务，含构建期生成的 schema grounding |
| 共享 proto | 仓库内 | 生成 Go/Python 桩 |

### 12.2 slim 机制（澄清）
`init.md` 的"构建开关 slim"重新定义为 **Python 打包 extras**：
- 基础镜像：`testpilot-worker`（不含 Playwright 与浏览器二进制，体积小）
- 完整镜像：`testpilot-worker[playwright]`（含 E2E 能力）
- Worker 注册时上报能力，Scheduler 据此路由 E2E 任务

### 12.3 版本协调
- 共享 proto 单一事实源，CI 生成各语言桩
- Worker 注册上报 `sdk_version`，Scheduler 校验兼容范围
- Copilot grounding schema 随发布打包，版本号与 Scheduler 对齐

---

## 13. 运行支撑

### 13.1 定时调度
- Scheduler 内置 cron 调度器（robfig/cron），按 TestPlan.schedule_cron 触发运行
- 调度任务持久化，Scheduler 重启后恢复
- 触发产生 TestRun，trigger=scheduled

### 13.2 通知
- 运行完成/失败通知：Webhook、邮件、IM（钉钉/飞书/Slack）（`提案，待确认` 渠道范围）
- 按 TestPlan 配置通知规则与接收人；通知内容含运行摘要与报告链接

### 13.3 配额（租户级）
- 并发运行数、Worker 占用数、压测 Worker 数、产物存储量、每月运行次数（`提案，待确认` 粒度）
- 超额时调度拒绝并提示；可按租户配置

### 13.4 部署
- **本地调试**：支持两种
  - 单机单二进制：Scheduler（含嵌入前端）+ SQLite + 本地内嵌/子进程拉起 Worker 与 Copilot，零依赖快速启动
  - docker-compose：Scheduler + Worker + Copilot + PG + VictoriaMetrics + Vault dev，接近生产形态
- **生产**：仅 docker-compose，提供 `deployment` 模板（`docker-compose.prod.yml` + `.env.example` + Worker 横向扩缩配置）
- 不引入 k8s（首期）；模板支持多 Worker 副本与压测 Worker 独立编排

### 13.5 数据保留与清理
- 运行结果（含请求/响应快照）与产物（截图/trace/har）体积随时间增长，需保留策略。
- 默认：运行结果保留 N 天（如 90）、产物保留 M 天（如 30），超限异步清理；可按租户/项目配置。
- 大响应体可截断存储（保留头部 + 大小），完整体存产物。

---

## 14. 可观测性

- **日志**：结构化日志（JSON），统一 trace_id 贯穿 Scheduler->Worker->Copilot
- **指标**：Prometheus（任务队列、Worker 利用率、运行通过率、压测指标）
- **链路**：OpenTelemetry，跨进程 trace 传递
- **审计**：Copilot 写操作、敏感变量读取、人工变更、租户切换记录审计表
- trace 与日志均带 tenant_id 便于按租户筛查

---

## 15. 开放问题

### 15.1 低代码沙箱强度（已确认）
采用**分层隔离后端 + 能力桥**（见 6.3）：v1 默认 **`subprocess` 加固基线 + 能力桥**；隔离需求增强时切 `container`（gVisor）；最强隔离走独占 Worker。执行后端以 `ExecutionBackend` 抽象，可插拔升级。

### 15.2 已提案项（默认采纳，可调整）
- 状态管理：Zustand + TanStack Query
- 通知渠道：Webhook + 邮件 + 钉钉/飞书
- ID 策略：tenant_id 与主键用 snowflake（long）；实体内部可保留字符串业务 ID
- Locust 库模式（无 master-web）
- 外部身份源：OIDC/OAuth2 可插拔 Provider
- Copilot 写/触发操作默认 HITL 审批（见 7.5）
- Copilot MCP 暴露/消费（v2，见 7.5）
- Playwright codegen 录制 bootstrap（v2，见 10.3）
- 压测目标限定单接口/简单用户流（见 8.1）

### 15.3 可选范围裁剪（建议，可调）
- **MySQL**：可作为可选支持延后；首版聚焦 PostgreSQL + SQLite，减少多库测试/迁移负担
- **BodySpec 的 XML/GraphQL/Binary**：可延后到 v2，首版聚焦 JSON / form-data / urlencoded

---

## 16. 附录：与 init.md 的差异清单

| init.md 条目 | 问题 | 本文档处理 |
|------|------|------|
| 数据库支持 H2 | H2 与 GORM/Go 不兼容 | 移除 H2，保留 PG/MySQL/SQLite |
| Worker 由 Python 或 TS 编写 | 与低代码 Python-on-Worker、Playwright slim 构建标签矛盾 | 定为纯 Python Worker，slim 改为打包 extras |
| Copilot 暴露 Vercel AI 接口与 Scheduler 交互 | 与"gRPC 交互"矛盾，Vercel AI 协议是前端流式通道 | 拆分：前端↔Copilot 走 HTTP/SSE（@ai-sdk/react），Copilot↔Scheduler 走 gRPC |
| Playwright slim 构建开关 | Go 概念，不适用 Python Worker | 改为 Python extras 变体 |
| 测试用例/计划/套件 | 完全缺失 | 新增第 4 章测试模型 |
| 压力测试架构 | 缺失 | 新增第 8 章：Locust 库模式 + VictoriaMetrics + 压测独占 |
| 测试报告/结果/历史/CI | 缺失 | 新增 4.7 与 10.4 |
| 断言体系 | 缺失 | 新增 4.5 |
| Copilot 能力边界与 DDL | 模糊 | 定为 Agentic 经 API 写入，grounding 澄清为 schema 快照+运行时工具 |
| 低代码运行时与沙箱 | 缺失 | 新增第 6 章 |
| RBAC / Vault / 目录树 / 导入导出 | 欠细化 | 第 3/9/10 章细化 |
| 多租户 | 缺失 | 新增 2.4：tenant_id 贯穿全实体，混合 Worker 模型，应用层过滤隔离 |
| 认证 | 缺失 | 新增 9.4：本地 + OIDC/OAuth2 双轨可插拔 |
| 定时调度 / 通知 / 配额 / 部署 | 缺失 | 新增第 13 章；部署为本地单二进制/compose + 生产仅 compose |
| 前端技术栈 | 未指定 | React + Ant Design + Vite + Vercel AI SDK，嵌入 Scheduler |
