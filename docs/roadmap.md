# TestPilot 实施方案（Roadmap）

> 📚 文档导航：[设计](design.md) · [数据模型](data-model.md) · [路线图](roadmap.md) · [使用指南](usage.md) · [部署](deployment.md) · [API 参考](api.md) · [错误码](error-codes.md) · [v2 特性](v2-features.md)

## 目录

1. 进度总览
2. 实施原则
3. 工作流与依赖
4. 阶段划分（Phase 0–9）
5. MVP 定义
6. 风险登记与 Spike 清单
7. 里程碑
8. 工程规范
9. 与设计的可追溯
10. v2 范围

---


> 基于 `docs/design.md`、`proto/`、`docs/data-model.md` 制定的分阶段落地计划。
> 原则：**契约先行、端到端骨架优先、垂直切片、风险驱动（Spike 前置）、多租户/认证内建于 day 1**。
> 每阶段交付**可运行、可演示、可回滚**的增量。

---

## 进度总览（2026-08-17 更新）

**Phase 0–9 与 v2 第一批/第二批/第三批全部完成并回归通过**（M1–M6 里程碑达成）。当前无进行中的功能批次；后续工作集中在文档对账、生产安全加固与「明确不做/另议」项排期决策。

v2 第三批（已完成）：Postman 导入导出、gRPC 接口测试（proto reflection）、低代码 Page 模型、压测 behavior_case。

其后完成的工程化加固：

| 项 | 状态 | 说明 |
|---|---|---|
| #21 fiber v3 迁移 | ✅ | net/http → fiber v3（fasthttp）；全量 e2e + 链路 + 容器冒烟通过 |
| 启动配置三级化 | ✅ | YAML/env/CLI（Copilot 多一层 .env）；逐键模板 `deploy/*.yaml.example` |
| 无网单测 | ✅ | Go 15 internal 包 + Worker 123 用例 + Copilot 61 用例 |
| 依赖卫生 | ✅ | locust/pyyaml/pytest(dev) 声明入 pyproject；worker 镜像含 locust（容器压测冒烟通过） |
| v2 第一批 | ✅ | curl 导出（/export/curl）、Copilot 反代（/copilot-api/* → TP_COPILOT_URL）、OpenAPI URL 导入（gRPC openapi_url 分支 + SSRF 私网防护） |
| v2 第二批 | ✅ | suite 引用展开、ApplyOpenApiDiff、对象存储制品后端（local/S3）、loop parallel、lowcode script_ref（详述 `docs/v2-features.md`） |
| v2 第二批补完 | ✅ | api_call 步骤 api_id 派发期解析、PlanItem param_overrides 应用（原遗留边界清零） |
| 前端 IDE 化重构 | ✅ | 三栏布局 + 接口调试工作区（/apis/debug 后端支撑）+ 用例步骤树编辑器 + 全部缺失页面（gRPC/proto/套件/脚本/管理台/注册/SSO）+ 设计 token 体系 |
| 2026-08-17 补齐轮 | ✅ | 证书 CRUD / 接口 pre-post 脚本 / Copilot gRPC 工具面 / 运行取消 / 雪花节点可配置 / DNS 解析绑定 / sandbox rlimits 移除 preexec_fn / 生产 egress 默认拦私网 / 通知 dialer 防 rebinding |
| 2026-08-17 HTTP 契约补齐 | ✅ | cookies / tls_verify(optional) / comment_tolerant_json / binary_ref（artifact 解析 + base64 内联）| Copilot 新增 create_project / query_api_directory / check_variable_refs |

**v2 状态**（三批均已完成，详见本文件末尾「v2 范围」）：
- 第一批：curl 导出 / Copilot 反代 / OpenAPI URL 导入
- 第二批：suite 引用展开（ref_type=2）、ApplyOpenApiDiff、对象存储制品后端、loop parallel、lowcode script_ref
- 第三批：gRPC 接口测试（proto reflection）、低代码 Page 模型 + 压测行为脚本、Postman 导入导出

---

## 0. 实施原则

1. **契约先行**：proto + 数据模型已定（任务 A）。所有组件以此为单一事实源。
2. **端到端骨架优先**：尽早打通"建接口 → 建用例 → Worker 执行 → 看结果"最小闭环，再逐层加厚，避免长期无集成。
3. **垂直切片而非水平分层**：每阶段交付一个可用的功能增量，而非"先做完所有后端再做前端"。
4. **风险驱动**：最不确定的部分（声明式执行引擎、低代码能力桥、Locust 并发模型、流式协议、gRPC 双向流）用 **Spike 前置验证**。
5. **多租户/认证内建于 day 1**：`tenant_id` 与租户过滤中间件从 Phase 0 就存在，先用"默认租户 + 存根认证"，后续替换为真认证，**避免后期全量返工**。
6. **契约治理**：proto 变更走 buf lint + breaking-change 检查；Worker/Copilot 经版本握手校验兼容。

---

## 1. 工作流与依赖

四条工作流，契约定后可并行；执行引擎与 SDK 是关键路径。

```
契约 (proto + data-model)   [已完成 A]
   │
   ├─► Scheduler (Go)  ──┬─► Console API ──► Frontend (React)
   │                      ├─► gRPC server ──► Worker (Python)
   │                      └─► gRPC tools ───► Copilot (Python, pydantic-ai)
   │
   └─► 关键路径: 执行引擎(Worker) → 低代码SDK/沙箱 → Copilot grounding
```

- **可并行**：契约后，Scheduler CRUD、Worker 执行引擎、Frontend 页面三线并行；Copilot 依赖 Scheduler API 稳定后接入。
- **关键路径**：声明式执行引擎 → 低代码能力桥 → Copilot 低代码 grounding。

---

## 2. 阶段划分

### Phase 0 — 地基（脚手架 + 契约落地） ✅
**目标**：可构建、可运行的空骨架，契约可 codegen，组件能连通。

| 交付物 | 组件 |
|--------|------|
| 仓库结构 `scheduler/ worker/ copilot/ frontend/ proto/ deploy/ docs/` | 全仓 |
| `buf.yaml` / `buf.gen.yaml`（Go + Python codegen 固定） | proto |
| 首批 DDL（核心表：tenants/users/tenant_members/projects/environments/variables/http_apis/tree_nodes） | Scheduler |
| 配置加载、结构化日志、健康检查、GORM 连接、**租户中间件（默认租户存根）** | Scheduler |
| grpcio 连接、Register/Connect 双向流桩 | Worker |
| CI：proto lint + codegen + 单测 + 迁移校验 | 全仓 |

**DoD**：`make dev` 一键起 Scheduler+Worker（单二进制模式），Worker 成功注册到 Scheduler，健康检查通过，proto codegen 可复现。
**风险**：低。**Spike E**：gRPC 双向流（背压、重连、取消语义）在此验证。

---

### Phase 1 — 端到端骨架（walking skeleton） ✅
**目标**：打通最小闭环，验证执行引擎与表达式语言。

| 交付物 | 组件 |
|--------|------|
| 项目/环境/HTTP 接口/声明式用例/运行 的最小 CRUD + 触发运行 + 结果查询 | Scheduler |
| 声明式引擎 v1：仅 `API_CALL`/`ASSERTION`/`SET_VAR` + 表达式语言 v1（模板插值 + JSONPath 安全求值）；httpx 客户端 | Worker |
| 结果落库（test_runs/case_results/step_results） | Scheduler |
| 最小页面：接口列表、用例编辑、触发运行、结果查看 | Frontend |
| 调度 v1：单 Worker，能力路由 + 负载均衡基础 | Scheduler |
| **认证基础**：本地账号 + JWT（控制台不再裸奔） | Scheduler |

**DoD**：UI 创建一个 HTTP 接口 + 含 2 步的声明式用例，触发运行，看到逐步骤结果与断言通过/失败。
**风险**：中。**Spike A（前置）**：声明式引擎 + 表达式语言原型。**Spike D（前置）**：Locust gevent 子进程与 asyncio 共存验证（为 Phase 7 扫障，成本低、早做）。
**依赖**：Phase 0。

---

### Phase 2 — 领域管理 + 导入导出 ✅
**目标**：完整的基础业务结构管理（对标 Postman/Apifox 的接口管理）。

| 交付物 | 组件 |
|--------|------|
| 目录树（CRUD、排序、子树查询） | Scheduler + Frontend |
| 变量体系（项目/环境维度，敏感标记 + Vault/Tink 引用） | Scheduler |
| 证书管理 | Scheduler |
| gRPC 接口（proto 上传 / server reflection、GrpcApi CRUD） | Scheduler + Worker |
| 导入：OpenAPI 3.x / Postman / curl → HttpApi | Scheduler |
| 导出：OpenAPI / curl / Postman / 项目 bundle | Scheduler |
| 接口级前置/后置脚本（受限 Python） | Worker |

**DoD**：导入一份 OpenAPI 生成接口树；配置环境变量并在请求中引用；gRPC 接口可定义并被调用。
**风险**：低-中。**Spike F**：gRPC server reflection / proto 动态调用。
**依赖**：Phase 1。

---

### Phase 3 — 执行引擎深化 + 报告 ✅
**目标**：声明式引擎全能力 + 完整结果/报告 + CI。

| 交付物 | 组件 |
|--------|------|
| 全部步骤类型：IF/LOOP/RETRY/CODE_BLOCK/DELAY + 嵌套 + step_path 定址 | Worker |
| 断言体系全 op + 结果记录 | Worker |
| 并发执行（plan.concurrency）+ 隔离上下文 + overlap_policy | Worker + Scheduler |
| 实时进度（Worker→Scheduler→前端 WS/SSE） | 三方 |
| 报告：三级下钻、趋势、产物预览 | Scheduler + Frontend |
| 取消/超时传播 | Scheduler + Worker |
| CI 集成：REST 触发 + 轮询/Webhook + CLI + JUnit XML | Scheduler |

**DoD**：含控制流（IF/LOOP/RETRY）的用例并发跑通，实时进度可见，报告可下钻，CLI 触发并返回正确退出码。
**风险**：中（并发上下文隔离、嵌套步骤、实时扇出）。
**依赖**：Phase 1。

---

### Phase 4 — 低代码 SDK + 沙箱 ✅
**目标**：Python 低代码用例可编写、可安全执行。

| 交付物 | 组件 |
|--------|------|
| `testpilot-sdk`：Pydantic 模型（HttpAPI/GrpcAPI/Response/assert_that/Page 预留）+ `run()` 契约 | Worker |
| **能力桥**：沙箱内瘦客户端 + IPC 转发 Worker 执行（HTTP/gRPC/变量/密钥） | Worker |
| subprocess 后端 + 加固基线（非特权用户、setrlimit、env scrub、scratch 目录、超时） | Worker |
| `ExecutionBackend` 抽象（可升级 gVisor） | Worker |
| 模型生成管线：proto → JSON Schema → `datamodel-code-generator` → Pydantic | proto |
| 低代码用例 CRUD + Monaco 编辑器 | Scheduler + Frontend |

**DoD**：编写低代码用例（调用 2 接口 + 断言 + 数据流）在沙箱中安全执行成功；验证沙箱无网络直连、无 Worker 凭据、资源受限。
**风险**：高。**Spike B（前置）**：能力桥原型（隔离性 + IPC 性能），建议在 Phase 2-3 之间先做原型。
**依赖**：Phase 3。

---

### Phase 5 — Playwright E2E ✅
**目标**：页面 E2E 测试。

| 交付物 | 组件 |
|--------|------|
| `testpilot-worker[playwright]` 变体（含浏览器，版本随 Playwright 锁定） | Worker |
| 声明式 `UI_ACTION` 步骤（14 动作）+ 低代码 `Page` 模型 | Worker |
| 浏览器生命周期（每用例一次启动、BrowserContext 隔离） | Worker |
| 产物：截图/video/trace/har + 报告嵌入 Trace Viewer 回放 | Worker + Frontend |

**DoD**：含 UI_ACTION 的用例在 playwright Worker 上跑通，报告可看 trace 回放与截图。
**风险**：中（浏览器资源占用、Trace Viewer 嵌入）。
**依赖**：Phase 3（声明式 UI 步骤）；低代码 `Page` 依赖 Phase 4。

---

### Phase 6 — Copilot ✅
**目标**：AI 生成与分析。

| 交付物 | 组件 |
|--------|------|
| Copilot 服务（FastAPI + pydantic-ai） | Copilot |
| Grounding：构建期 `schema.json` + 数据字典 + SDK API 面 | Copilot |
| 工具集 v1：只读 + 生成类（接口/声明式用例/断言；低代码生成需 Phase 4）经 Scheduler API | Copilot |
| HITL 审批 + 审计 | Copilot + Scheduler |
| 流式适配（`pydantic_ai.ui.vercel_ai`）+ 前端对话面板 | Copilot + Frontend |

**DoD**：对话生成一个接口和一个用例（经 HITL 确认落库）；流式输出正常；审计可查。
**风险**：中。**Spike C（前置）**：pydantic-ai → Vercel AI 流式 + HITL 原型。
**依赖**：Scheduler API 稳定（Phase 2-3）；低代码生成依赖 Phase 4。

---

### Phase 7 — 压力测试 ✅
**目标**：分布式压测。

| 交付物 | 组件 |
|--------|------|
| StressTestPlan CRUD | Scheduler |
| Locust 库模式发压（**独立子进程 + gevent**） | Worker |
| Scheduler master：拆分负载、聚合 | Scheduler |
| VictoriaMetrics 写入 + 报告（时序图、SLA 达标） | Worker + Scheduler + Frontend |
| 压测独占调度 | Scheduler |

**DoD**：对一接口发起阶梯压测，多 Worker 发压，报告展示 RPS/延迟 p95/错误率时序。
**风险**：中-高（Locust gevent 集成、VictoriaMetrics 写入/查询）。
**依赖**：Phase 3（执行）、Worker 池。**Spike D 已在 Phase 1 扫障**。

---

### Phase 8 — 多租户/认证/RBAC/通知/配额强化 ✅
**目标**：生产级多租户与治理。`tenant_id` 过滤自 Phase 0 已内建，本阶段替换存根认证为真认证并补全治理。

| 交付物 | 组件 |
|--------|------|
| 真认证：本地账号（已有）+ OIDC/OAuth2（`identity_providers` 可插拔）+ JWT | Scheduler |
| RBAC 权限矩阵落地 + `tenant_member` | Scheduler |
| 配额（concurrent_runs/worker_slots/artifact_bytes/monthly_runs/ai_calls） | Scheduler |
| 定时调度（robfig/cron + overlap_policy + misfire 处理） | Scheduler |
| 通知（Webhook/邮件/钉钉/飞书） | Scheduler |
| SSRF 出口控制落实（Worker 出口代理/白名单） | 运维 + Worker |
| 数据保留清理任务（结果/产物按策略清理） | Scheduler |

**DoD**：跨租户数据不可见（隔离验证）；OIDC 登录可用；配额超限被拒；定时 + 通知可用。
**风险**：中（认证集成、权限矩阵覆盖完整性）。
**依赖**：贯穿；认证/RBAC 依赖 Phase 1 存根替换。

---

### Phase 9 — 可观测性 + 部署 + 收尾 ✅
**目标**：可运维、可交付。

| 交付物 | 组件 |
|--------|------|
| Prometheus 指标 + OpenTelemetry 链路 + 结构化日志（统一 trace_id） | 全组件 |
| 审计完善 | Scheduler |
| `deployment` 模板（`docker-compose.prod.yml` + `.env.example` + Worker 扩缩） | 运维 |
| 文档（使用 / 部署 / API） | 全组件 |
| 性能与稳定性加固、数据迁移回归 | 全组件 |

**DoD**：compose 一键起全栈；指标/链路可见；文档齐全。
**依赖**：各阶段。

---

## 3. MVP 定义（首个可交付版本）

**MVP = Phase 0 → 3 + 认证基础**，即"接口管理 + 自动化测试 + 报告"完整闭环：

- 接口管理（HTTP/gRPC）+ 目录树 + 环境变量 + 证书
- 声明式用例 + 全执行引擎（控制流/断言/并发）+ 报告（下钻/趋势）
- 导入 OpenAPI / Postman / curl；导出
- CI 集成 + CLI
- 本地账号认证（单租户或默认租户）

> MVP 即可替代"Postman + 自动化脚本"的日常用途。**低代码 / Playwright / Copilot / 压测 / 完整多租户**为后续增强版（v1.1+）。

---

## 4. 风险登记与 Spike 清单

| Spike | 验证内容 | 前置阶段 | 正式落地 | 缓解 |
|-------|---------|---------|---------|------|
| **A** | 声明式执行引擎 + 表达式语言（核心，最新颖） | Phase 1 前 | Phase 1/3 | 先做 v1 三种步骤 |
| **E** | gRPC 双向流（背压/重连/取消） | Phase 0 | Phase 0/1 | 协议简单、事件驱动 |
| **B** | 低代码能力桥（隔离性 + IPC 性能） | Phase 2-3 间 | Phase 4 | 先验证隔离与开销，失败可退化为纯 subprocess |
| **D** | Locust gevent 子进程与 asyncio 共存 | Phase 1 | Phase 7 | 低成本早做，确认独立子进程方案 |
| **C** | pydantic-ai → Vercel AI 流式 + HITL | Phase 6 前 | Phase 6 | 已有官方适配器，风险低 |
| **F** | gRPC server reflection / proto 动态调用 | Phase 2 | Phase 2 | 备选：proto 上传优先 |

> 高风险 Spike（A/B/D）务必**前置做原型**，不等到正式阶段才首次碰。

---

## 5. 里程碑（相对顺序，不定绝对日期）

| 里程碑 | 包含阶段 | 可演示成果 |
|--------|---------|-----------|
| **M1 骨架可跑** | Phase 0-1 | 最小闭环：建接口→建用例→跑→看结果 |
| **M2 MVP** | Phase 2-3 | 接口管理+自动化+报告+CI 可用 |
| **M3 低代码+E2E** | Phase 4-5 | 低代码沙箱执行 + 页面 E2E + trace 回放 |
| **M4 Copilot** | Phase 6 | AI 生成接口/用例（HITL）+ 流式对话 |
| **M5 压测** | Phase 7 | 分布式压测 + 时序报告 |
| **M6 生产就绪** | Phase 8-9 | 多租户/认证/RBAC/配额/可观测/部署模板 |

---

## 6. 工程规范

- **目录**：`scheduler/`(Go module)、`worker/`(Python)、`copilot/`(Python)、`frontend/`(Vite)、`proto/`、`deploy/`、`docs/`
- **proto 治理**：`buf lint` + `buf breaking`（防破坏变更）；codegen 入 CI，产物不入库
- **分支/提交**：trunk-based + 短生命周期 feature 分支；Conventional Commits
- **CI**：proto lint/codegen、Go 单测、Python 单测、迁移校验、前端构建（当前以本地命令验证，未启用 GitHub Actions；需要远程 CI 时另建）
- **版本协调**：Worker 注册上报 `sdk_version`，Scheduler 校验兼容范围；Copilot grounding 随发布与 Scheduler 对齐
- **多 DB**：开发期 SQLite/PG，迁移脚本以 PG 为准，CI 覆盖 PG

---

## 7. 与设计的可追溯

| 阶段 | 覆盖 design.md 章节 |
|------|---------------------|
| Phase 0 | 12（构建/版本）、2（架构） |
| Phase 1 | 4.4/4.5/4.6（步骤/断言/引擎 v1）、5（调度）、9.4（认证基础） |
| Phase 2 | 3（领域模型）、10.1/10.2（导入导出） |
| Phase 3 | 4（测试模型全量）、4.7（结果报告）、10.4（CI）、5.4（状态机） |
| Phase 4 | 6（低代码 SDK + 沙箱 + 能力桥） |
| Phase 5 | 10.3（Playwright） |
| Phase 6 | 7（Copilot） |
| Phase 7 | 8（压力测试） |
| Phase 8 | 2.4（多租户）、9.3/9.4（RBAC/认证）、9.5（SSRF）、13.1/13.2/13.3/13.5（调度/通知/配额/保留） |
| Phase 9 | 13.4（部署）、14（可观测性） |

---

## 8. v2 范围（MVP 后增强；原 Phase 2/3/5/7 中裁剪项 + 新增）

> 下列各项在原阶段计划中存在但被裁剪/降级，或属新增增强。按投入产出与依赖分批。

### 第一批（已完成 2026-08-13）
| 项 | 原属 | 落地 |
|---|---|---|
| curl 导出 | Phase 2「导出 curl」 | `GET /export/curl?project_id=`（impexp.ExportCurl） |
| Copilot 生产反代 | Phase 6/9 边界 | scheduler `/copilot-api/*` → `TP_COPILOT_URL`（SSE FlushInterval=-1） |
| OpenAPI URL 导入 | Phase 2 导入增强 | gRPC `ImportOpenApi.openapi_url` 分支 + 私网/环回 SSRF 防护 |

### 第二批（已完成 2026-08-14）
| 项 | 位置 | 落地 |
|---|---|---|
| suite 引用展开 | runner.go `RefType != 1` 跳过；DDL `test_suites/test_suite_items` 已预留 | ✅ Suite/Item GORM 模型 + `/suites` CRUD + 触发时有序展开（items 仅 case 引用，无嵌套无环） |
| ApplyOpenApiDiff | copilot_service.go `Unimplemented` | ✅ impexp.ApplyOpenAPIDiff：method+uri 匹配，added/changed/breaking/removed（removed 仅报告不删除）；auto_update_cases 回写用例 inline；Copilot 工具 HITL |
| 对象存储制品后端 | artifact_handlers.go 直读本地目录 | ✅ `internal/artifactstore` local/S3（minio-go，OSS S3 网关实测）；上报 Ingest 上传、读取/retention 走后端；AK/SK 不进 CLI |
| loop parallel | engine.py `parallel=true not supported` | ✅ 并发迭代 + vars 快照隔离 + 按迭代序合并 + 统一抛首个失败（带迭代号） |
| lowcode script_ref | engine.py `script_ref not supported` | ✅ `scripts` 资产库 + `/scripts` CRUD + 派发前按租户解析内联 source |

> 详述见 `docs/v2-features.md`（含派发期解析与 param_overrides 的补完章节 §6）。

### 第三批（大工程，需 Spike 前置；2026-08-14 开工）
| 项 | 前置 | 说明 |
|---|---|---|
| Postman 导入导出 | — | ✅ 已完成：collection v2.1 解析/生成（folder 递归、`{{base_url}}` 占位导出） |
| gRPC 接口测试 | Spike F（proto reflection / 上传） | ✅ 已完成：Spike F 通过 → GrpcApi/ProtoFile 模型 + `/grpc-apis` `/proto-files` CRUD + 引擎 GRPC_CALL（反射动态调用，任务级 grpc_apis 映射）+ Copilot create_grpc_api 工具 |
| 低代码 Page 模型 | Spike B 能力桥扩展 UI 操作 | ✅ 已完成：SDK `ctx.page` + 桥 op=ui_action → Worker UiSession（run/case 目录隔离产物；断言失败随桥错误传播） |
| 压测 behavior_case | 依赖低代码 Page 能力桥（已就绪） | ✅ 已完成：Worker 进程内 asyncio 负载环 + 沙箱常驻循环模式（迭代门控/全新 vars 快照/指标协议复用 Locust 路径，报告页零改动） |

### 本轮补齐（2026-08-17）
| 项 | 落地 |
|---|---|
| 证书管理 CRUD | `/certificates` REST + 前端证书页（pem/p12 引用）；Worker 加载 cert_ref 执行 TLS 仍另议 |
| 接口 pre/post 脚本 | Worker 按 `run(ctx)` 沙箱执行；pre 写入 `ctx.vars` 参与请求渲染，post 可读 `ctx.response`；前端调试页标记 `enabled:true` |
| Copilot gRPC 工具面 | `ListApis` 返回 `grpc_apis`；`GetApi(kind=GRPC)`/`UpdateApi(grpc)` 可用 |
| 运行取消 | `POST /runs/:id/cancel`：RUNNING→ABORTED、未决 case→SKIPPED、广播 cancel；迟到结果被状态守卫拒绝 |

### 明确不做/另议
- **OAuth2 授权码登录**：✅ 已落地（2026-08-14）——`type=oauth2` + userinfo 身份拉取 + 无 discovery 提供方显式端点接入；**client_credentials（机器凭证）**仍另议（属 api_tokens 场景）
- **api_tokens 表（CI token）**：DDL 已预留；roadmap Phase 3 的「CI 集成 + CLI」整体待议（用户已明确不依赖 GitHub CI）
- **邮件通知**：需引入 SMTP 配置/凭据与投递语义，另议；当前 webhook/钉钉/飞书可用
- **计划级 notifications 规则**：与现有「租户渠道 + events 订阅」模型冲突，暂以租户渠道为准；规则字段仅存储
- **项目 bundle 导出**：尚无导出格式/用例定义，另议；OpenAPI/curl/Postman 已可用
- **WS/SSE 实时进度推送**：前端当前 3s 轮询已满足运行可见性；推送通道与 fiber 静态托管/负载均衡方案相关，另议
- **plan.concurrency / retry_on_failure**：与 Worker 中心化并发模型（max_concurrency + 最少负载调度）冲突，当前仅存储；如需计划级并发上限需重构调度面
- **证书客户端证书执行**：cert_ref/key_ref 实际解析与 TLS 绑定依赖 Vault/制品密钥后端，另议；CRUD 已落地
- **沙箱容器/gVisor 后端**：当前为 subprocess + sandbox-exec/bwrap 尽力隔离；生产可开 `TP_SANDBOX_REQUIRE_ISOLATION=1` fail-closed，容器/gVisor 后端需部署层实现
- **gRPC mTLS**：Worker token + Copilot JWT 已启用；mTLS 可选增强另议
- **HTTP cookies / tls_verify / comment_tolerant_json / binary_ref**：✅ 已补齐执行语义（cookies 渲染发送；tls_verify 缺省 true；注释 JSON 请求体解析；binary_ref 支持 `artifact:<id>` / `base64:<data>`）
- **VictoriaMetrics**：压测时序当前落 `stress_metric_points` 表；设计文档保留 VictoriaMetrics 为大规模部署的可选替换，非排期项
- **Vault**：密钥管理走敏感变量 + secret_ref 引用（沙箱零凭据设计）；Vault 对接非排期项
