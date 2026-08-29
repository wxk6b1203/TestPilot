# TestPilot：接口目录、低代码沙箱与 AI Copilot —— 一个集成测试平台的设计与实现

> 技术博文 · 面向测试工程师 / 平台工程师
> 关键词：API 测试、gRPC、低代码、Python SDK、沙箱、LLM Agent、HITL、分布式压测、CI

**摘要**：TestPilot 是一个 LLM 增强的集成测试平台，覆盖从接口管理、用例编排、
执行调度、报告制品到 AI 协作的完整链路。本文面向第一次接触 TestPilot 的读者，
从它要解决的四个测试工程问题讲起，依次介绍整体架构、接口目录、变量体系、
两种用例形态（声明式步骤树与 Python 低代码）、沙箱与凭据边界、调度执行、
报告与制品、UI 测试与分布式压测，最后落到 Copilot Agent 的工具调用与人工
审批设计，以及端到端的工作流闭环。

---

## 1. TestPilot 是什么

TestPilot 是一个开源（Apache-2.0）的集成测试平台，一句话定位：

> **以接口目录为唯一真相源，用声明式步骤树和 Python 低代码编写用例，
> 由 Go 调度器派发到分布式 Worker 执行，并用带审批机制的 AI Copilot
> 参与编写、导入、触发与失败分析的全过程。**

它要解决的是测试资产达到工程规模后的四个经典问题：

1. **定义漂移**——脚本如果直接复制接口的 URL / header / TLS 配置，接口一变
   脚本就悄悄过期，结果往往不是运行失败，而是「静默打到了旧路径」。
2. **资产难以复用与编排**——单个请求容易写，成百上千个用例的组织、依赖声明、
   参数传递、定时触发和 CI 集成才是真正的工程量。
3. **凭据边界模糊**——测试脚本天然要接触 token、cookie、内部域名，如果脚本
   与凭据同处一个运行时，安全就只剩「规范约定」，无法审计。
4. **AI 只能给建议、不能干活**——通用 AI 助手看不到项目的真实接口、变量和
   历史运行，生成的代码要人工搬运和核对，更不被允许执行任何写操作。

TestPilot 对这四个问题的回答分别是：**按 ID 引用接口、派发时注入快照**；
**用例 / 套件 / 计划三层编排 + cron / CI 触发**；**低代码沙箱零凭据 + 能力桥
代执行副作用**；**Copilot 通过 gRPC 工具集读写真实项目数据，写操作全部经过
人工审批（HITL）并落审计**。

下面从架构开始逐一展开。

---

## 2. 整体架构

TestPilot 由四个组件组成：

```text
Frontend (React)
   │  REST 控制面 / SSE 实时事件        │  Vercel AI SSE（经 Scheduler 反代）
   ▼                                   ▼
Scheduler (Go)  ◄───── gRPC ──────►  Copilot (Python + pydantic-ai)
   │  接口目录 · 用例/计划 · 调度 · 落库      （每个工具调用 = 一次 Scheduler gRPC）
   │  gRPC 任务下发 / 事件回传
   ▼
Worker (Python) × N
   ├── 声明式执行引擎（步骤树）
   ├── 低代码沙箱（testpilot-sdk + 能力桥）
   ├── Playwright UI 执行器
   └── 压测执行器（Locust / 行为压测）
```

- **Scheduler**（Go，REST `:8080` + gRPC `:9090`）：唯一的数据库持有者。接口
  目录、用例、计划、运行结果、审计都落在这里；同时负责把计划展开成任务、
  按 capability / 租户 / 负载挑选 Worker 下发，以及向前端推送 SSE 实时事件。
- **Worker**（Python）：不监听端口，主动向 Scheduler 发起双向 gRPC 流注册并
  领取任务；执行声明式步骤树或低代码沙箱，边执行边回推进度、日志与制品。
- **Copilot**（Python）：LLM Agent 服务。它不直连数据库，每一个工具调用都是
  一次 Scheduler gRPC 请求，复用平台既有的租户、RBAC 与审计边界。
- **Frontend**（React）：控制台 + Copilot 对话页，实时事件走 SSE。

三条架构铁律贯穿全项目：

1. **数据库只属于 Scheduler**。Worker 与 Copilot 都是 Scheduler 的下游，彼此
   互不通信；下发给 Worker 的任务里，所有实体（接口快照、脚本源码、生成的
   封装类）都已内联解析完毕，Worker 执行期间零数据库访问。
2. **proto 是单一契约**。接口、用例、步骤、运行结果等领域对象统一定义在
   `proto/testpilot` 下，Go 与 Python 两侧的代码由同一份契约生成，跨语言
   不存在第二份结构定义。
3. **租户隔离在入口收敛**。所有读写都带 `tenant_id` 上下文，RBAC（owner /
   admin / member / viewer 四级）在 REST 与 gRPC 入口统一校验，而不是散落在
   业务代码里。

---

## 3. 接口目录：唯一真相源

接口目录是整个平台的基石，支持两类接口：

- **HTTP 接口**：method、URL、headers、cookies、query/body 模板、TLS 校验
  策略、客户端证书、认证 header 等，全部结构化存储；
- **gRPC 接口**：service / method 全名、元数据、超时，请求体在执行时通过
  **server reflection** 动态构造——不需要为被测服务编译 proto 桩，只要服务
  开了反射即可直接调用。

目录之上还有两组工程化能力：

- **OpenAPI 导入与变更同步**：可以把 OpenAPI 描述导入为接口目录；后端发布
  新版本后再次导入，平台按 added / changed / breaking 分类给出 diff，可选择
  自动回写受影响的用例。
- **变量引用检查**：接口定义里大量使用 `{{vars.xxx}}` 模板，平台提供一键
  检查「哪些接口引用了未定义的变量」，避免运行期才发现拼写错误。

---

## 4. 环境与变量体系

接口和脚本都不应该写死环境相关的值。TestPilot 用两级容器 + 五层作用域解决：

- **项目（Project）**与**环境（Environment）**是两个顶层容器：接口、用例、
  计划属于项目；每个项目可定义多个环境（dev / staging / prod…），环境携带
  自己的变量集。
- 变量在运行时的解析优先级为 **step > case > plan > env > project**——步骤里
  刚设置的值立即生效，环境变量兜底，项目变量再兜底。
- 脚本与接口定义里用 `{{vars.path.to.value}}` 引用变量。模板表达式由一个
  AST 白名单解释器执行：**只有属性访问与索引，没有函数调用**，配合深度与
  长度护栏，模板引擎本身不成为注入面。
- 敏感变量（token、secret 类命名）在展示层统一掩码，且**不会注入低代码脚本
  上下文**（见第 6 节）。

---

## 5. 用例的两种形态

TestPilot 的用例（TestCase）有两种类型，共享同一套接口目录与变量体系。

### 5.1 声明式步骤树

用 JSON / 表单描述步骤，适合流程固定的接口测试。步骤类型共十种：

```text
api_call     调用 HTTP 接口（按 ID 引用 + 参数覆盖）
grpc_call    调用 gRPC 接口
assertion    断言（status / header / jsonpath，等于、包含、存在、正则等）
set_var      把表达式结果写入变量
if / loop    条件分支与循环（支持并行度上限）
retry        失败重试（次数 + 间隔）
code_block   内联代码片段
delay        延时等待
ui_action    Playwright UI 动作（goto / click / fill / 断言可见 等）
```

一段真实的步骤树大致长这样：

```json
{
  "steps": [
    { "id": "1", "type": "STEP_TYPE_API_CALL", "name": "创建用户",
      "apiCall": { "apiId": "744267297657487360",
                   "override": { "body": { "name": "neo" } } } },
    { "id": "2", "type": "STEP_TYPE_ASSERTION", "name": "校验响应",
      "assertion": { "assertions": [
        { "target": "JSON", "path": "$.data.id", "op": "EXISTS" } ] } },
    { "id": "3", "type": "STEP_TYPE_LOOP", "name": "批量查询",
      "loopStep": { "over": "{{vars.user_ids}}", "parallel": 4, "steps": [ ... ] } }
  ]
}
```

步骤以**嵌套点路径**定址（如 `3.loop.1.2`），运行报告里的每一步都有独立的
结果记录：请求 / 响应快照、逐条断言的期望与实际值、日志、耗时。

### 5.2 低代码 Python

当测试逻辑超出声明式的表达力（多步编排、复杂数据加工、UI 脚本化），可以写
一段 Python 脚本，入口函数固定：

```python
async def run(ctx):
    ...
```

脚本在受限沙箱里执行（见第 6 节），通过 `ctx` 能力对象声明一切副作用：

```python
# 创建用户并断言。注意：没有 method / uri / headers，只有一个接口 ID
resp = await ctx.http_api("744267297657487360").run(
    body={"name": "neo", "email": "neo@example.com"}
)
assert_that(resp.status).eq(200)
assert_that(resp.body["data"]["id"]).exists()

# 变量读写与日志
await ctx.set_var("user_id", resp.body["data"]["id"])
ctx.log(f"created user {resp.body['data']['id']}")

# gRPC 同构：按 ID 调用，反射动态构造请求
r = await ctx.grpc_api("744267297657487999").run(request={"name": "neo"})
```

`ctx.http_api(id)` 背后的语义是：**脚本只声明「我要调哪个接口」和「本次业务
输入是什么」**。method、URL、headers、cookies、证书、TLS 策略、binary 引用
都在 Scheduler 派发任务前从接口目录解析成快照，随任务一起下发。

脚本的 `run(...)` 参数只是 override：headers / params / cookies 按键合并，
body 整体替换；而 `tls_verify` 这类安全设置以接口快照为准，**脚本不可覆盖**
——把 TLS 校验关掉的行为不可能藏在测试脚本里发生。

### 5.3 依赖声明与运行前校验

低代码用例必须显式声明它引用了哪些接口（`httpApiRefs` / `grpcApiRefs`；
脚本里的 `ctx.http_api("...")` 字面量也会在派发时被静态提取补全）。引用了
不存在或无权限的接口，**在运行开始前就明确失败**，而不是执行到一半抛出一串
难懂的堆栈。声明本身还有第二个用途：影响分析——接口变更时能精确列出受影响
的用例。

### 5.4 自动生成的稳定封装类

对偏好面向对象写法的场景，派发时 Scheduler 会为被引用接口生成
`tp_api_wrappers.py`：

```python
# auto-generated by TestPilot — do not edit
from testpilot_sdk import HttpAPI

class Api744267297657487360(HttpAPI):
    """创建用户 · POST /users"""
    api_id: str = "744267297657487360"
    method: str = "POST"
    uri: str = "/users"
    headers: dict = {"X-Client": "testpilot"}
```

脚本中即可 `Api744267297657487360(body={"name": "neo"}).run()`。这里有个刻意
的设计：**`Api<ID>` 是稳定类名**。接口在页面上改名、改 URL，`Api<ID>` 始终
有效——脚本天然免疫接口重命名。

---

## 6. 沙箱与凭据边界

低代码脚本运行在一个受限子进程中，这是平台安全模型的基石：

- **无网络**：脚本进程的直连网络被拒绝（macOS `sandbox-exec` / Linux
  bubblewrap 尽力隔离，可配置强制），`requests` / `httpx` / 裸 socket 都
  不可用；
- **无环境变量**：子进程启动即抹掉所有 `*TOKEN* / *KEY* / *SECRET* /
  *PASSWORD*` 类环境变量；
- **资源受限**：CPU / 内存 / 输出尺寸都有硬顶，循环与桥接调用有并发护栏，
  防止失控脚本拖垮 Worker；
- **副作用全部经能力桥代执行**：HTTP、gRPC、变量读写、日志、UI 操作，由
  Worker 进程代为执行后把结果回传给脚本。

那鉴权怎么办？环境里配置的 `Authorization` 等 header 变量，由 Worker 在
发起请求时自动注入，**值不需要、也不会进入脚本上下文**。脚本作者从头到尾
接触不到 token；沙箱里写什么代码都不会把凭据带出去。脚本资产（源码）由
Scheduler 存储与版本管理，派发前内联进任务，同样不经过任何共享文件系统。

---

## 7. 计划、调度与执行

单条用例之上是计划（TestPlan）：把用例 / 套件按顺序组装，绑定环境，附加
计划级变量，就构成一次可重复执行的测试资产。

**触发方式**有四种：

1. 控制台手动触发；
2. **cron 定时**：计划绑定 cron 表达式，Scheduler 内置调度器按时触发，对
   落后超过阈值的 misfire 自动补跑一次；
3. **CI token 触发**：流水线里用长令牌调用 REST 接口发起运行，适合部署后
   冒烟；
4. webhook / 事件联动。

触发后 Scheduler 把计划展开成任务，按 **capability 标签、租户归属与当前
负载**挑选合适的 Worker，经双向 gRPC 流下发；执行期间的取消与超时也沿同一条
流传播，不会出现「页面点了取消、Worker 还在跑」的分裂状态。

---

## 8. 报告、制品与通知

一次运行（TestRun）落库后得到：

- **运行级汇总**：总数 / 通过 / 失败 / 耗时，前端 SSE 实时刷新进度；
- **步骤树明细**：每个步骤（含循环内每一轮）的请求与响应快照、逐条断言的
  期望值与实际值、日志、耗时，可精确到 `3.loop.1.2` 这样的路径；
- **JUnit XML 导出**：`GET /runs/:id/junit`，Jenkins / GitLab CI 直接渲染；
- **制品（Artifacts）**：九类——截图、视频、Trace、HAR、下载文件、日志、
  Proto、证书、用户上传。UI 测试自动归档截图与视频；二进制请求体支持
  `artifact:<id>` 引用，派发时内联下发；
- **通知**：运行结束可回调 webhook，附带运行摘要，方便接入 IM 机器人或
  内部告警平台。

---

## 9. UI 测试与分布式压测

**UI 测试**基于 Playwright，与接口测试共用一套资产模型：

- 声明式形态：步骤树里的 `ui_action` 步骤（goto / click / fill / select /
  断言可见 / 截图等），与接口步骤自由混排——「调接口造数 → 打开页面 → 断言
  元素」在同一个用例里完成；
- 低代码形态：`ctx.page` 暴露受控的 Playwright 页面对象，适合脚本化的复杂
  UI 流程；每步自动截图，失败时归档现场。

**压测**分两种形态，共用同一套用例资产：

- **接口压测**：直接选接口（或用例）配并发与时长，由 Locust 执行——Locust
  被隔离在独立子进程里，避免其 gevent 与平台 asyncio 互相污染；
- **行为压测**：以低代码用例为虚拟用户行为脚本，按 ramp 曲线（如
  `0s→2, 5s→10, 10s→20`）逐步加压，压测执行器可以独立横向扩容成专门的
  Worker 池。

压测运行实时回传 RPS、延迟分位、错误率等指标，前端绘制时序曲线。

---

## 10. Copilot：有工具、有审批、有审计的 Agent

TestPilot 的 Copilot 基于 `pydantic-ai`，前端经 Vercel AI SDK 流式交互。它
不是「套壳聊天框」：**每一轮回答都是对真实项目数据的函数调用序列**。模型
支持 DeepSeek 与任意 OpenAI 兼容端点（温度 / top_p 等采样参数可按部署配置）。

### 10.1 工具集：只读 + 受审批的写操作

- **只读（免审批）**：项目 / 环境 / 接口 / 用例 / 计划的查询，`get_run`
  （含步骤明细）、覆盖率查询、接口目录视图、变量引用检查、当前上下文，
  以及 UI 探测的快照与关闭；
- **写 / 触发（必须人工审批）**：创建 / 修改项目、HTTP 与 gRPC 接口、用例
  （含 UI 用例）、计划，OpenAPI 导入与 diff 应用，触发运行与触发压测，以及
  UI 探测的打开 / 操作 / 求值 / 执行脚本。

所有工具都经 Scheduler 的 gRPC 接口执行，请求带租户与用户上下文——**租户
隔离、RBAC 和审计不是 Copilot 自己实现的，而是复用平台既有边界**。Copilot
没有数据库连接，也没有任何旁路。

### 10.2 Grounding：让模型知道「本项目的数据长什么样」

通用 LLM 最大的问题不是「不会写代码」，而是「不知道你的 schema 和 SDK 签名」。
TestPilot 在构建期生成两份 grounding 注入 system prompt：

1. **领域数据字典**（`domain-schema.json`）：所有实体的字段、类型、约束，
   模型生成用例 JSON 时按真实 camelCase 结构输出，而不是凭记忆编造；
2. **低代码 SDK 文档**（`sdk-api.md`）：`ctx.http_api`、`assert_that`、
   封装类的真实签名与限制。

因此 Copilot 生成低代码用例时会被显式引导：优先 `ctx.http_api(id)` 或
`Api<ID>` 封装类，**不要手抄 method / URL**，并在 `httpApiRefs` 中声明依赖。
生成结果从第一行起就符合平台契约。

### 10.3 页面上下文：AI 知道你当前在看哪个项目

Copilot 页沿用控制台的当前项目 / 环境选择，前端通过 `X-TP-Project-Id` /
`X-TP-Env-Id` 头随每次请求传给 Copilot，服务端校验租户归属后加载权威详情。
于是这样的对话成立：

> 用户：**把当前项目里变量引用没定义的接口列出来，并生成一个冒烟计划。**
>
> Copilot 实际动作：
> 1. `get_current_context()` —— 确认当前项目 / 环境，不臆造 ID；
> 2. `check_variable_refs()` —— 找出 `{{var}}` 缺失定义的接口；
> 3. `query_api_directory()` —— 浏览接口目录挑选接口；
> 4. `create_test_case()` × N —— 生成低代码用例（触发审批）；
> 5. `create_test_plan()` —— 组装计划（触发审批）。

上下文是请求的一部分，工具是上下文的眼睛，审批是动作的闸门。

### 10.4 HITL：写操作必须经过人

写工具执行前会挂起，把审批卡片推给前端：

```text
[工具调用] create_test_case
  name: 创建用户-冒烟
  project_id: 744267297657487360
  definition: { case_type: "lowcode", source: "..." }

[拒绝] [批准]
```

- 批准后工具才真正执行；拒绝后 Agent 被告知结果并继续对话；
- 审批卡片显示明确的**目标项目 / 环境**，避免「AI 操作错环境」；
- 全部调用落审计，actor 标记为 `copilot`，谁在什么时间批准了什么可追溯。

敏感信息有工程化防护：接口定义里的 `Authorization`、`Cookie`、`X-Api-Key`
等 header 值在进入 LLM 上下文前统一掩码；已掩码的值回写时会被丢弃而不是
覆盖真实凭据。

### 10.5 UI 探测：让 AI 先「看」页面再写用例

写 UI 用例最难的是选择器与页面真实结构对不上。Copilot 内置 **UI 探测**
能力：在受控会话里打开目标页面，抓取 ARIA 可访问性快照，执行点击 / 填充 /
取值等探测动作，甚至运行一段受沙箱约束的 Playwright 脚本——探测会话有
数量、时长与输出尺寸上限，快照作为文本反馈给模型，用于生成可靠的
`ui_action` 步骤或 `ctx.page` 脚本。探测中的写类动作同样走审批。

### 10.6 长对话与失败分析的闭环

Copilot 内置上下文压缩（历史到达窗口阈值后由轻量 summarizer 模型压缩），
支撑「触发运行 → 跟踪进度 → 拿到结果 → 分析失败」的完整长会话；对话历史
持久化存储，支持会话切换与回收站恢复（30 天自动清理）。

结合 `get_run(include_steps=true)`，失败分析是**事实驱动**的：步骤路径、
请求 / 响应快照、断言明细、日志都来自数据库，而不是用户口述。Copilot 可以
给出「第 3 步 `$.data.id` 断言失败，实际响应为 …，根因是环境变量覆盖顺序」
级别的结论。

---

## 11. 端到端闭环：一次 OpenAPI 变更之后

把前面的能力串成一个真实场景：

```text
后端发布新版本，OpenAPI 描述变更
        │
        ▼
用户：把这次变更同步到项目
Copilot：import_openapi / apply_openapi_diff（HITL）
        ├─ added / changed / breaking 分类列出
        └─ 可选 auto_update_cases 回写受影响用例
        │
        ▼
用户：为新增接口生成低代码冒烟用例
Copilot：query_schema + get_api + 按 ID 生成用例（HITL）
        │
        ▼
用户：跑一遍
Copilot：trigger_run（HITL）→ get_run 跟踪 → 失败根因分析
        │
        ▼
CI：run_finished webhook → 拉取 JUnit XML → Jenkins/GitLab 渲染
```

接口、用例、运行结果在同一个数据模型里，Copilot 通过工具读写它们，人只在
写操作处按下审批键。

---

## 12. 平台化能力

作为多团队共用的平台，TestPilot 内置了组织级的基础能力：

- **多租户隔离**：所有数据带租户边界，入口统一校验；
- **RBAC**：owner / admin / member / viewer 四级角色，数字越小权限越强；
- **OIDC SSO**：对接企业身份提供方登录；
- **配额**：按租户限制运行并发与资产规模；
- **审计日志**：谁在什么时间对什么实体做了什么，含 Copilot 的全部工具调用
  与人工审批记录；
- **可观测**：Scheduler 暴露 metrics，链路追踪接 OpenTelemetry，部署样例
  附带 Prometheus + Jaeger。

---

## 13. 部署与上手

- **生产**：`deploy/docker-compose.prod.yml` 一键拉起 PostgreSQL + Scheduler
  + Worker 集群 + 独立压测 Worker 池 + Copilot + Jaeger + Prometheus；三个
  服务均为「CLI 参数 > 环境变量 > YAML 配置文件 > 内置默认值」的四级配置，
  仓库内有逐键注释的配置模板。
- **本地开发**：`scripts/dev.sh start` 一条命令拉起全部组件（含 mock 回显
  服务），附带主流程与各专项的端到端测试脚本。
- **安全默认**：弱 JWT 密钥拒绝启动、空 Worker 令牌拒绝所有 Worker 注册、
  通知 webhook 默认屏蔽私网地址——安全项全部 fail-closed。

作为 Apache-2.0 许可的开源项目，完整设计文档见仓库内 `README.md`、
`docs/design.md` 与 `docs/usage.md`。

---

## 14. 诚实的边界

技术博客应当交代边界，而不是只写优势：

- **AI 质量取决于模型**：grounding 与 HITL 能大幅降低 schema 错误与越权
  风险，但生成质量仍受所选模型能力影响；平台不绑定模型供应商，团队可按
  数据合规要求自行选型。
- **沙箱是分层基线**：当前低代码沙箱为子进程加固基线（无网络、无环境变量、
  资源受限、副作用桥接）；对强隔离诉求的生产环境，可以替换为 gVisor 或
  独立的 Worker 池。
- **Copilot 的写覆盖仍在扩展**：当前覆盖创建 / 修改 / 导入 / 触发，更远的
  自动修复闭环、覆盖缺口分析、压测报告的 AI 解读在路线图上。
- **平台的价值在整体**：接口目录 + 用例 + 计划 + 报告 + CI 作为整体使用时
  价值才完全成立；如果诉求只是单次手工调试，一个轻量 HTTP 客户端就够了。

---

## 15. 结语

TestPilot 把 API 测试从「一份份孤立的脚本」组织成「一个有目录、有编排、有
调度、有报告、有 AI 协作的工程系统」：

- 低代码**按 ID 调用**让脚本消费接口定义而非复制接口定义，定义漂移失去土壤；
- 声明式步骤树与 Python 沙箱覆盖从简单断言到复杂编排的全部表达力区间；
- 沙箱与能力桥让凭据有了可审计的边界，脚本作者从头到尾接触不到秘密；
- 计划、cron、CI 令牌与分布式 Worker 让测试资产真正跑在流水线里；
- Copilot 以工具调用读写真实项目数据，以 HITL 审批约束每一次写操作——
  AI 终于从「给建议的旁观者」变成了「被授权的同事」。

欢迎试用与共建：克隆仓库、`dev.sh start` 拉起本地环境，从一个接口、一个
用例、一次对话开始。
