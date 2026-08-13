# TestPilot 使用指南

> 📚 文档导航：[设计](design.md) · [数据模型](data-model.md) · [路线图](roadmap.md) · [使用指南](usage.md) · [部署](deployment.md) · [API 参考](api.md) · [错误码](error-codes.md) · [v2 特性](v2-features.md)

## 目录

1. 概念速览
2. 声明式用例
3. 低代码用例（Python SDK）
4. UI 用例（Playwright）
5. 定时调度
6. 通知
7. 配额
8. OIDC 登录
9. Copilot
10. 压测
11. 审计

---


面向测试/开发用户的操作手册。部署见 `docs/deployment.md`，REST 参考见 `docs/api.md`。

## 概念速览

```
项目 Project ──┬── 环境 Environment（base_url + 变量解析）
               ├── 变量 Variable（项目级 environment_id=0 / 环境级；sensitive 落审计）
               ├── 接口 HttpApi（OpenAPI/curl 导入或手建）
               ├── 用例 TestCase（declarative=步骤树 / lowcode=Python SDK / ui=Playwright）
               ├── 计划 TestPlan（有序 items，引用用例）── 触发 → TestRun
               └── 压测计划 StressTestPlan ── 触发 → StressRun
```

- **多租户**：所有数据按租户隔离；`POST /tenants` 自助建租户，`POST /auth/switch-tenant` 切换。
- **角色**：owner=1 / admin=2 / member=3 / viewer=4（小=高）。GET 只需 viewer；领域写要 member；
  租户治理（成员/配额/通知/身份源/审计）要 admin。

## 声明式用例

用例 = 递归步骤树（`definition.steps`）。步骤类型：

| type | 名称 | 关键字段 |
|---|---|---|
| 1 | API_CALL | `api_call`（`api_id` 引用或 `inline` 内联 method/uri/...） |
| 2 | ASSERTION | `assertion`（target/operator/expected） |
| 3 | SET_VAR | `set_var`（key/value，value 可引用 `{{expr}}`） |
| 4 | IF | `condition` + `children` |
| 5 | LOOP | `loop`（count 或 over 变量）+ `children` |
| 6 | RETRY | `retry`（times/interval）+ `children` |
| 7 | DELAY | `delay_ms` |
| 8 | CODE_BLOCK | `code`（低代码沙箱 Python） |
| 9 | UI_ACTION | `ui_action`（13 种浏览器动作） |
| 11 | GRPC_CALL | `grpc_call`（`grpc_api_id` 引用 `/grpc-apis`，server reflection 动态调用） |

表达式：`{{vars.x}}` / `{{response.json.user.name}}` / `{{response.status}}` / `{{env.BASE_URL}}`。
断言：STATUS / HEADER / BODY / JSONPATH / ELAPSED × EQ/NE/EXISTS/CONTAINS/MATCHES/GT/LT/GE/LE/TYPE_IS。

完整可运行示例见 `scripts/e2e.py`（建项目 → 环境 → 用例（含 script_ref/套件）→ 计划 → 运行 → 断言结果）。

**LOOP `parallel: true`**：迭代并发执行，每迭代取 vars 快照 + iterator 变量，迭代内 SET_VAR
互不可见；结果按迭代序合并，任一迭代失败则该 LOOP 步骤失败（错误带迭代号）。

**套件与脚本资产（v2）**：`POST /suites` 建有序用例集合，计划 item `ref_type=2` 引用后触发时
展开；低代码脚本可存 `POST /scripts` 资产库，用例 definition `{"script_ref": "<id>"}` 引用，
派发前内联执行。

**gRPC 用例（v2 第三批）**：建接口 `POST /grpc-apis`（full_service/method/request_message），
步骤 `{"grpc_call": {"grpc_api_id": "<id>", "request_override": {...}}}`；目标地址取环境
base_url（host[:port]），响应经 `$.字段` JSONPATH 断言。

**api_id 引用与参数覆盖（v2 补完）**：步骤 `api_call` 可只写 `api_id`（含嵌套步骤），派发前
自动解析为接口快照；计划条目 `param_overrides` 深合并进低代码 `parameters`（`ctx.parameters`），
并追加为 `{{key}}` 模板变量（优先级最高）。详见 `docs/v2-features.md`。

## 低代码用例（Python SDK）

```python
def case(sdk):
    r = sdk.http.get("/json")
    sdk.assert_that(r.status).eq(200)
    sdk.assert_that(r.json()["user"]["name"]).eq("neo")
```

沙箱内运行：setrlimit 限额、环境白名单、无网络出口（macOS sandbox-exec / Linux bwrap），
HTTP/变量等副作用经能力桥由 Worker 代执行。

**Page 模型（v2 第三批）**：`ctx.page` 在沙箱内驱动浏览器（经能力桥转发 Playwright）：

```python
async def run(ctx: Context):
    page = ctx.page
    await page.goto("/form")
    await page.fill("#username", "neo")
    await page.click("#login-btn")
    await page.expect_text("#result", "Welcome, neo!")   # 不匹配 → 用例失败
    await page.screenshot()                               # 截图/trace/har 挂到步骤结果
```

## UI 用例（Playwright）

UI_ACTION 支持 GOTO/CLICK/FILL/SELECT/CHECK/HOVER/PRESS/EXPECT_TEXT/EXPECT_VISIBLE/
SCREENSHOT/WAIT/UPLOAD/DOWNLOAD。产物：截图（失败自动）+ trace.zip + network.har，
运行详情页内联查看，trace 可 `npx playwright show-trace` 回放。

## 定时调度

`POST /schedules`：`{plan_id, env_id, cron_expr: "* * * * *", overlap_policy: 1|2, enabled}`。
标准 5 段 cron（分 时 日 月 周）；overlap_policy=1 时上次未结束则跳过；进程重启后
对落后 >2min 的 schedule 补跑一次（misfire）。触发的 run `trigger=2 (SCHEDULED)`。

## 通知

`POST /notifications`：type 1=webhook（原始 JSON）/ 2=钉钉（markdown + 加签）/ 3=飞书（text + 加签）。
`events` 逗号分隔：`run_finished,stress_finished`。异步发送，失败仅记日志与指标。

## 配额

`PUT /tenant/quotas/{metric}`（admin）：metric ∈
`concurrent_runs`（并发运行）/ `monthly_runs`（月度运行）/ `artifact_bytes`（产物字节）/
`ai_calls`（月度 Copilot 调用）/ `worker_slots`（租户专属 Worker 数）。
0 或不存在 = 不限；超限 → 429 `QUOTA_EXCEEDED`。用量实时从事实表计算（`GET /tenant/quotas` 可见）。

## OIDC 登录

admin 配置 `POST /identity-providers`（issuer/client_id/client_secret）。用户访问
`GET /auth/oidc/providers` 选择身份源 → `GET /auth/oidc/{id}/login` 302 到 IdP →
回调签发本地 JWT。外部用户首次登录自动建档，默认 viewer。支持 RS256（JWKS）与 HS256。

## Copilot

对话页（`/copilot`）自然语言生成接口/用例/压测计划、触发运行、查询结果。
写操作需 HITL 审批（前端按钮）；全部工具调用经 Scheduler gRPC 落审计（actor=copilot）。
API：`POST :8100/api/chat`（Vercel AI SSE），需 Scheduler JWT。

## 压测

`POST /stress-plans`（target=接口，load_profile：concurrency/duration/ramp）。
Scheduler 把目标并发均衡拆到所有 stress-capable Worker（压测期间独占）。
报告页有时序图（RPS/P95/并发/错误率）；压测结束触发 `stress_finished` 通知。

## 审计

`GET /audit-logs`（admin）：Copilot 写操作（actor=2）、人工变更（actor=1，自动中间件）、
敏感变量读取（secret_read）、租户切换（switch_tenant，落在目标租户）。
