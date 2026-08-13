# TestPilot v2 第二批特性说明

> v2 第二批交付说明（2026-08-14）。前三节（suite 引用展开 / ApplyOpenApiDiff / lowcode script_ref）
> 为详细设计记录，后两节为 loop parallel 与对象存储制品后端的要点速览。
> 实现位置：Scheduler `internal/{runner,impexp,httpserver,artifactstore,dispatch,retention}`，
> Worker `engine.py`，契约 `proto/testpilot/{common,copilot}/v1/*.proto`。

---

## 1. suite 引用展开（PlanItem ref_type=2）

### 1.1 概念

测试套件（TestSuite）是**有序用例列表**——与 TestPlan 的 items 结构对称但只允许 case 引用：
`test_suite_items` 每行 = `{suite_id, case_id, order}`，**不允许套件嵌套套件**，因此展开过程
无环、无需递归深度限制。计划条目 `test_plan_items.ref_type` 新增取值 `2 = suite`（1 = case）。

### 1.2 数据模型

| 表 | 关键列 | 说明 |
|---|---|---|
| `test_suites` | id, tenant_id, project_id, name, description, 软删 | 与 DDL `docs/sql/*.sql` 一致（原 v2 预留表，本批落地 GORM 模型） |
| `test_suite_items` | id, suite_id, case_id, order | 全量替换语义（见下），无 tenant_id（跟随套件） |

### 1.3 REST 接口

```
GET|POST  /suites            （viewer 列表，支持 ?project_id=；member 创建）
GET|PUT|DELETE /suites/{id}  （viewer 读；member 写）
```

- POST/PUT body：`{name, description, project_id, case_ids: ["123", "456"]}`；`case_ids` 为有序
  ID 数组，字符串/数字形态都收（雪花 ID 超 JS 安全整数，前端回传字符串）。
- **items 全量替换**：更新时删除旧 `test_suite_items` 行并按新数组重建，order = 数组下标。
- 创建/更新不校验 case 是否存在——与 plan items 一致，缺失 case 在触发时跳过并告警。

### 1.4 触发时展开（runner.Trigger）

派发循环按 `ref_type` 分派：

- `ref_type=1`：原路径，`dispatchCase(RefID)`。
- `ref_type=2`：`suiteCaseIDs(tenant, RefID)` 加载套件（租户过滤）→ 按 `order` 升序取
  case_ids → 逐个 `dispatchCase`。展开后的 case 与直接引用走**完全相同的路径**
  （配额检查、case_result 预建、TaskAssignment 派发、失败落库）。

### 1.5 语义细节与边界

- **重复执行是显式语义**：同一 case 被计划直接引用又经套件引用时，会派发多次、各落一条
  case_result。run summary 统计展开后的实际数量。
- **套件缺失/被删**：该 plan item 跳过，WARN 日志；不影响其他 item。
- **套件内 case 缺失**：与直接引用相同——跳过该 case（WARN）。
- **param_overrides 未应用**：与 v1 直接引用行为一致（该字段后续批次接入）。
- 展开发生在触发时刻（读套件最新内容），改套件后下次触发即生效。

### 1.6 测试

- 单测 `runner_test.go`：`TestTriggerSuiteExpansion`（2 case 按序派发）、
  `TestTriggerSuiteMissing`（缺失套件 → 跳过 → NO_WORKER 路径）。
- e2e `scripts/e2e.py`：建套件 → 计划含 `ref_type=2` → 运行 → 展开后的 case 全绿校验。

---

## 2. ApplyOpenApiDiff（OpenAPI 增量应用）

### 2.1 定位

Copilot 侧工具：给定**新 spec**（OpenAPI 3，JSON/YAML），按 `method + uri` 与项目内现有接口
匹配，增量应用变更并输出差异报告。与 `ImportOpenAPI` 的关系：同一解析器（`parseSpecAPIs`），
import = 幂等创建（存在即跳过）；diff = 匹配 + 分类 + 更新。

### 2.2 接口（gRPC CopilotToolService）

```proto
rpc ApplyOpenApiDiff(ApplyOpenApiDiffRequest) returns (ApplyOpenApiDiffResponse);

message ApplyOpenApiDiffRequest {
  RequestContext ctx = 1;
  string project_id = 2;
  string openapi_document = 3;   // 新 spec
  bool auto_update_cases = 4;
}
message ApplyOpenApiDiffResponse {
  repeated DiffEntry diffs = 1;            // {api_id, kind, summary}
  repeated string updated_api_ids = 2;     // 新增 + 变更的接口
  repeated string updated_case_ids = 3;    // auto_update_cases=true 时被回写的用例
}
```

Copilot 工具 `apply_openapi_diff(project_id, openapi_document, auto_update_cases)`，
`requires_approval=true`（HITL 审批后执行），审计 `action=apply_openapi_diff`。

### 2.3 四类结论

| kind | 判定 | 动作 |
|---|---|---|
| `added` | 新 spec 有、项目没有 | 创建 HttpApi |
| `changed` | 两侧都有，params/headers/body 有差异，且非破坏性 | 更新 params/headers/body |
| `breaking` | 两侧都有，且：**① 已有 query/header 参数键在新 spec 中被移除；② body content_type 变化** | 同 changed + kind 升级 |
| `removed` | 项目有、新 spec 没有 | **仅报告，不删除** |

### 2.4 关键设计决策

- **removed 不自动删**：删除是破坏性操作，留给用户在控制台显式执行；diff 每轮都会重复报告
  该 removed 条目（行还在、spec 还没有——持续可见的提醒）。summary 注明 "kept"。
- **breaking 是启发式**：当前 params/headers 列只存键值（无 required 标记），故用
  「参数键被移除 / body content_type 变化」作为保守判定——这两类变更最可能破坏存量用例。
- **幂等**：同 spec 重复应用不再产生 added/changed/breaking（内容相等则跳过）；
  removed 因行保留而稳定重复。

### 2.5 auto_update_cases（用例内联快照回写）

声明式用例的 `api_call` 步骤形如 `{"api_id": "...", "override": {...}}`——运行态由 Scheduler
在派发前解析（当前 MVP 尚未实现 api_id 的派发期解析，见 2.6）。diff 检测到接口变更时，
`auto_update_cases=true` 会把**新 spec 的快照写回用例 definition**：

- 递归遍历步骤树（顶层 steps + `if_step`/`loop_step`/`retry_step` 嵌套内的
  `then_steps`/`else_steps`/`body_steps`/`body_step`），命中 `api_id == 变更接口` 的
  `api_call` 步骤 → 写入 `inline`（protojson 兼容的 HttpApi 快照：method 枚举名、uri、
  params/headers/body）。
- **保留 `api_id` 引用与 `override`**——inline 是快照缓存，不破坏引用语义与用例覆盖。
- 响应 `updated_case_ids` 列出被回写的用例；未引用变更接口的用例不被触碰。

### 2.6 已知边界

- api_id 引用的**派发期解析**（runner 内联，类似 script_ref）尚未实现：Worker 收到仅含
  api_id 的步骤仍会报错——经 diff 回写 inline 后的用例不受影响。该缺口在后续批次补。

### 2.7 测试

- 单测 `impexp_test.go`：全矩阵（added/breaking/removed + 幂等重复）、body content_type
  breaking、auto_update_cases（顶层 + 嵌套回写、override/api_id 保留、无关用例不触碰）。
- gRPC 单测 `copilot_service_test.go`：`TestApplyOpenApiDiff`（分类 + 落库 + 审计）、
  参数校验 InvalidArgument。

---

## 3. lowcode script_ref（脚本资产库）

### 3.1 概念与动机

低代码用例定义（proto `LowCodeCase`）为 oneof：`source`（内联脚本）或 `script_ref`（引用）。
v1 只支持 source；script_ref 原注释为「artifact 引用」——**错误地把可复用脚本资产当作 run
产物**（Artifact 随保留策略级联清理，脚本被清掉用例即失效）。本批落地独立的**脚本资产库**
`scripts` 表，与 Artifact 生命周期解耦。

### 3.2 数据模型

```
scripts: id, tenant_id, project_id, name, description,
         language（默认 python）, content, created_at/updated_at, deleted_at（软删）
```

租户 + 项目两级隔离；软删与其他配置类实体一致。DDL 已补入 `docs/sql/*.sql`（第 32 张表）。

### 3.3 REST 接口

```
GET|POST /scripts            （viewer 列表，?project_id=；member 创建，content 必填）
GET|PUT|DELETE /scripts/{id} （viewer 读；member 写/删）
```

用例侧不变：`POST /cases` 的 definition 传 `{"script_ref": "<script id>", "entry": "run"}`
（`parameters` 可选）。schema 语义见 `copilot/grounding/domain-schema.json`（已同步）。

### 3.4 派发时内联（runner.materializeCase）

Worker 无 DB、引擎只接受 `source`，因此**解析发生在 Scheduler 派发前**：

1. `ToProtoCase` 还原用例（definition JSON → proto）。
2. 若 `lowcode.script_ref != ""`：按 `id + tenant_id` 加载 Script →
   `lowcode.script = source{content}`，`script_ref` 清空。
3. 失败即**不派发**：脚本不存在 / 跨租户 / script_ref 非数字 ID → 该 case_result 直接置
   FAILED（error 说明原因），其余 case 不受影响。
4. Worker 引擎收到裸 `script_ref`（绕过 Scheduler 的契约破坏）→ 直接 FAILED 并提示
   "must be resolved by scheduler"。

### 3.5 语义

- **更新即时生效**：改脚本内容，下次触发即用新内容（派发时解析，无缓存）。
- **一次写、处处引用**：同一流程被多个用例引用；控制台展示的 definition 仍是 ref 形态
  （改写回写需借助 ApplyOpenApiDiff 的 auto_update_cases 同款机制，未实现）。
- 沙箱执行路径与 source 完全一致（subprocess 沙箱 + 能力桥，零凭据）。

### 3.6 测试

- 单测 `runner_test.go::TestMaterializeScriptRef`：内联成功 / entry 保留 / 缺失脚本报错 /
  跨租户拒绝 / 非低代码不受影响。
- e2e：脚本资产 → script_ref 用例 → 真实沙箱执行全绿（`e2e-script-ref`）。
- PG 冒烟：脚本表迁移 + script_ref 用例触发（无 worker 时错误为 NO_WORKER，证明已内联）。

---

## 4. LOOP parallel（要点）

- `LoopStep.parallel=true`：迭代**并发**执行（`asyncio.gather`，无并发上限字段，量级由
  count/range 决定）。
- **变量隔离**：每个迭代在独立 CaseRunner 上执行——进入 loop 时的 vars 快照 + iterator
  变量；迭代内 SET_VAR 不互相可见、不回写父作用域。
- **结果合并**：按迭代顺序合并到父 step_results（path 形如 `{path}.loop.{n}.{idx}`）。
- **失败语义**：不 fail-fast 取消——全部迭代跑完后统一抛第一个失败，错误信息带迭代号；
  已完成的迭代结果仍保留。非 StepFailure 异常原样上抛。
- 顺序模式语义不变（vars 共享、iterator 残留在父作用域）。

## 5. 对象存储制品后端（要点）

- `artifact_backend=local（默认）| s3`；S3 经 minio-go（SigV4），对象键 =
  `{s3_prefix}{tenant_id}/{uri}`，**租户在键内隔离**。
- **写入路径**：Worker 仍写共享产物目录 → Scheduler 收到 TaskResult 时 `Ingest` 上传
  （上传成功后删本地暂存文件；失败仅告警，产物行保留）。
- **读路径**：`GET /artifacts/{id}/content` 经后端；S3 先 Stat 再 Get（避免惰性 404 导致
  流中断）；本地后端沿用 `Clean("/"+uri)` 锚定 + 根前缀校验的穿越防护。
- **retention**：级联清理经后端 `Delete`（local 删文件 / s3 删对象）。
- **寻址风格**：AWS/阿里云 OSS S3 网关要求 **virtual-hosted**（默认）；私有 MinIO 设
  `s3_path_style=true`。阿里云实测端点格式：`https://s3.oss-{region}.aliyuncs.com`
  （如 `s3.oss-cn-shanghai.aliyuncs.com`）。
- **凭据安全**：`s3_access_key`/`s3_secret_key` 仅 YAML/环境变量（`tpflag:"-"` 不注册
  CLI flag，避免进程列表泄漏），勿入代码库；建议环境变量注入。
- 配置项：`artifact_backend` `s3_endpoint` `s3_access_key` `s3_secret_key` `s3_bucket`
  `s3_region` `s3_prefix` `s3_use_ssl` `s3_path_style`（详见 `deploy/scheduler.yaml.example`）。
