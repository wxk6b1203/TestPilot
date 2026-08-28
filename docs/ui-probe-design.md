# UI 探测会话（UI Probe）设计文档 —— v1 / v2

> 📚 文档导航：[设计](design.md) · [数据模型](data-model.md) · [路线图](roadmap.md) · [使用指南](usage.md) · [部署](deployment.md) · [API 参考](api.md) · [错误码](error-codes.md) · [工程化](ci-migration-plan.md) · [低代码 ID 调用](lowcode-api-invocation.md) · [技术博文](blog-lowcode-copilot.md) · [v2 特性存档](v2-features.md) · **UI 探测**（本文）
>
> 状态：设计评审稿（未实施）。v1 为本期范围，v2 为设计预留。
> 修订记录：2025-xx-xx 初稿。

---

## 目录

1. 背景与目标
2. 总体架构
3. 核心概念：ProbeSession 生命周期
4. V1 详细设计（proto / Scheduler / Worker / Copilot / 审批 / 提示词 / 配置 / 限额 / 错误码 / 测试）
5. V1 顺手项：TestStepResult 补 error 字段
6. V2 详细设计：run_py 常驻沙箱
7. 实施顺序与发布策略
8. 风险与开放问题

---

## 1. 背景与目标

### 1.1 问题

用户对 UI 流程的描述天然是模糊的（"打开页面，找到登录按钮，点击，输入账号密码，登录"）。当前 Copilot 只有 `create_ui_test_case`（一次性盲写 locator）与 `trigger_run`（整计划运行）两个粒度，中间缺少**感知页面**与**小步试探**的手段：

- locator 全靠猜，猜错的唯一反馈是超时错误串（且 `TestStepResult` 无独立 error 字段，报错原文回传不完整）；
- 探测一轮 = 建用例 → 建计划 → HITL 审批 → 派发 → Worker 冷启动浏览器 → 整跑，秒级~十秒级一轮；
- 每次 run 都是新浏览器实例，无法增量探测（登录态、导航位置不保留）；
- 部分 DOM 交互（枚举候选元素、多策略 locator 尝试、穿 shadow DOM）用固定原子动作表达不了，需要可编程逃生舱。

### 1.2 目标

- Copilot 能**打开页面并读到结构化快照**（ARIA snapshot 文本），据此选稳定 locator，而非猜测；
- Copilot 能在**常驻会话**上小步执行动作并每步获得新快照（一步一反馈，Claude Code 式循环）；
- 提供受限的**可编程逃生舱**：v1 为 `eval_js`（页面上下文 JS 表达式），v2 为 `run_py`（lowcode 同款沙箱内的常驻 Python）；
- 全程遵守拓扑铁律：Copilot 只连 Scheduler；Worker 无 DB；数据库只属于 Scheduler；
- 探测不计入 TestRun/配额体系（不是 run，不占并发/月度配额），但有自己的资源护栏。

### 1.3 非目标（本期不做）

- ❌ 视觉/截图回传给 LLM（多数模型纯文本；ARIA 快照已覆盖感知需求，截图仍作为 artifact 落盘供人查看）；
- ❌ 录制回放（playwright codegen 式的"人操作→生成脚本"）；
- ❌ 多实例 Scheduler 下的会话路由（单实例架构，进程内 map 即可；与 SSE Broker 同一约束）；
- ❌ 移动端/多标签页管理（v1 单 page 单标签；`page` 外的 target 不支持）。

### 1.4 设计原则（与既有架构对齐）

| 原则 | 在本设计中的体现 |
|---|---|
| 拓扑铁律 | 新链路 Copilot → Scheduler（gRPC）→ Worker（既有 bidi 命令流），Worker 零 DB |
| 防护栏宁紧勿松 | 会话数/TTL/快照体积/脚本体积/执行超时全部设上限；Scheduler 侧权威 + Worker 侧硬顶双份 |
| proto 契约先行 | 全部新增走 proto，`scripts/proto-gen.sh` 生成，CI 零漂移校验 |
| 审批 + 审计 | 副作用类探测工具走 HITL；Scheduler 对写类 RPC 落审计 |
| 未知命令前向兼容 | Worker 对未识别的 SchedulerCommand 静默忽略（现状即是），新旧版本可混跑 |

---

## 2. 总体架构

### 2.1 组件与链路

```
┌─────────┐  SSE(Vercel AI v7)   ┌──────────┐   gRPC CopilotToolService   ┌───────────┐
│ Web 前端 │ ───────────────────▶ │  Copilot │ ──────────────────────────▶ │ Scheduler │
└─────────┘   (HITL 审批回执)      └──────────┘   ui_probe_* 5 个新 RPC        └─────┬─────┘
                                                                              │ SchedulerCommand.probe
                                                              （既有 Worker bidi 流，          ▼
                                                                w.Send chan 复用）      ┌───────────┐
                                                                        ┌─────────────│   Worker   │
                                                                        │  ProbeReply │ ProbeHub   │
                                                                        └────────────▶│ └─ProbeSession│
                                                                                      │    └─UiSession │
                                                                                      │      └─Playwright
```

- **Copilot**：新增 `probe` 工具集（3 读 + 2 写，见 §4.4），会话句柄由 deps 持有。
- **Scheduler**：新增 `internal/probe` 包——会话注册表（粘性路由）、请求-回执配对（仿 `Dispatcher.RegisterWaiter` 的 pending map）、TTL reaper、审计。
- **Worker**：`client.py` 命令循环新增 `probe` 分支；新增 `probes.py`（ProbeHub：会话表 + 命令执行 + 回执），浏览器层完全复用 `ui.UiSession`。

### 2.2 一次探测的时序（v1，`ui_probe_click` 为例）

```
LLM            Copilot(tools.py)     前端(HITL)        Scheduler(copilot_service)   Scheduler(probe)      Worker(probes.py)
 │ act(click,…)?    │                   │                     │                         │                      │
 │──call──────────▶ │ requires_approval │                     │                         │                      │
 │                  │──DeferredTool───▶ │ 审批弹窗             │                         │                      │
 │                  │◀─approved──────── │                     │                         │                      │
 │                  │──ActProbe RPC─────────────────────────▶ │ 鉴权(JWT↔ctx) + 审计     │                      │
 │                  │                                         │──route(session)────────▶ │ 粘性查 worker         │
 │                  │                                         │                        │──SchedulerCommand────▶│
 │                  │                                         │                        │   {probe:{req_id,act}} │ pending[req_id]=chan
 │                  │                                         │                        │   …等待回执(超时 60s)   │ UiSession.execute()
 │                  │                                         │                        │◀─WorkerEvent────────── │ aria_snapshot()
 │                  │                                         │                        │   {probe_reply:{…}}    │
 │                  │◀───ActProbeResponse {ok,snapshot} ──────│◀───────────────────────│◀──────────────────────│
 │◀─tool result──── │ （快照截断后作为工具结果返回）              │                        │                      │
```

要点：

1. **命令下行复用既有 `Worker.Send` chan**（`internal/grpcserver/worker_service.go:49` 创建，容量 32），不新建流。
2. **回执上行复用既有 `WorkerEvent` oneof**，新增 `probe_reply` 成员；Worker 的 `client.py` 收流循环已有 `WhichOneof("event")` 分发，追加分支即可。
3. **请求-回执配对**：Scheduler 侧 `pending[request_id] = chan`（带超时），仿 `Dispatcher.RegisterWaiter/TakeWaiter` 模式；Worker 无需感知等待，纯异步回执。
4. **粘性路由**：`session_id → worker_id` 进程内映射；会话创建时按 `CAPABILITY_PLAYWRIGHT` + 低负载选 Worker，此后所有命令固定发往该 Worker；Worker 断连则会话置 DEAD（见 §3）。

---

## 3. 核心概念：ProbeSession 生命周期

```
                 ui_probe_open(url)                    Worker 断连 / TTL 到期 / 生命周期上限
   ┌──────┐     ──────────────────▶   ┌──────────┐     ────────────────────────────▶ ┌──────────┐
   │ (无)  │                           │  ACTIVE  │                                    │   DEAD   │
   └──────┘   ◀──────────────────    └────┬─────┘                                    └──────────┘
              ui_probe_close()            │ 空闲超时（无任何命令）
                                          ▼
                                     ┌──────────┐   再收到任何命令 → 自动重建（新 worker、新浏览器）
                                     │  IDLE    │
                                     └──────────┘
```

| 状态 | 含义 | 资源占用 | 迁移 |
|---|---|---|---|
| ACTIVE | 有在途命令或刚执行完 | Worker 浏览器进程存活 | 空闲超过 `idle_ttl` → IDLE；收到 `close`/TTL/上限 → DEAD |
| IDLE | 浏览器已关闭，仅保留会话元数据（路由粘性 + 上下文备注） | 极小（map 条目） | 收到任意命令 → 重新选 Worker 建 ACTIVE（**页面状态丢失**，快照会体现新页面）；`idle_ttl*2` → DEAD |
| DEAD | 终态 | 无 | 不可迁移；工具返回 `PROBE_SESSION_NOT_FOUND`（ Copilot 提示需重新 `ui_probe_open`） |

- **会话标识**：`session_id` 由 Copilot 生成（每会话一条，`chat-{session_db_id}`），跨多轮对话稳定；Scheduler 侧校验 `tenant_id` 归属，租户隔离与会话绑定。
- **每个会话绑定**：`{session_id, tenant_id, user_id, worker_id, state, created_at, last_active_at, base_url, close_reason}`。
- **一个用户同一时刻最多 1 个 ACTIVE 会话**（防同用户多会话叠加资源）；超限自动关闭最旧会话并在工具结果中提示。

---

## 4. V1 详细设计

### 4.1 proto 契约

#### 4.1.1 `proto/testpilot/copilot/v1/copilot.proto`（Copilot ↔ Scheduler）

追加 5 个 RPC（3 只读 + 2 写类，编号继续沿用文件内分组注释）：

```proto
service CopilotToolService {
  // …既有 12 只读 + 8 写 + 2 触发…

  // ---- UI 探测（v1 新增）：3 只读（会话生命周期，免审批） + 2 写类（副作用，HITL） ----
  rpc OpenProbe(OpenProbeRequest) returns (OpenProbeResponse);       // 写类（见 4.5 审批矩阵：首次建会话需审批）
  rpc GetProbeSnapshot(GetProbeSnapshotRequest) returns (GetProbeSnapshotResponse);  // 只读
  rpc CloseProbe(CloseProbeRequest) returns (CloseProbeResponse);    // 只读
  rpc ActProbe(ActProbeRequest) returns (ActProbeResponse);          // 写类
  rpc EvalProbe(EvalProbeRequest) returns (EvalProbeResponse);       // 写类
}
```

> 归类说明：`OpenProbe` 会启动浏览器进程（资源副作用），按**写类**审批；`ActProbe/EvalProbe` 对被测系统有潜在副作用，写类；`GetProbeSnapshot/CloseProbe` 纯读/释放，只读。

新增消息（追加于文件末尾，字段号从 1 起，均为新消息无冲突）：

> 勘误（实施阶段 1 时修正）：初稿自定义了 `ProbeAction`；实施时改为复用 `common.v1.UiActionStep`
> `{UiAction action; string target; string value;}`（worker.proto 不能跨包 import copilot.proto，
> common 两端已引入，共用避免重复定义）。

```proto
// UI 探测动作（与 worker.v1 的 UiAction 动作枚举对齐，复用其枚举值）
message ProbeAction {
  testpilot.common.v1.UiAction action = 1;  // 复用既有枚举：UI_ACTION_GOTO/CLICK/FILL/SELECT/CHECK/UNCHECK/HOVER/PRESS/WAIT
  string target = 2;                            // Playwright locator（CSS/XPath/text=…）
  string value = 3;                             // fill/select/press 的值；wait 为毫秒
}

message OpenProbeRequest {
  testpilot.common.v1.RequestContext ctx = 1;   // 第一字段必须是 ctx（拦截器反射约定）
  string session_id = 2;                        // Copilot 生成，每 chat 会话一个
  string url = 3;                               // 相对路径基于 env 的 base_url
  string env_id = 4;                            // 解析 base_url；空 = Copilot 当前选中环境
}
message OpenProbeResponse {
  string session_id = 1;
  string worker_id = 2;                         // 实际承载探测的 Worker
  string final_url = 3;                         // 导航后的真实 URL（重定向后）
  string title = 4;
  string aria_snapshot = 5;                     // 截断后的 ARIA YAML（见 4.3.3）
  bool snapshot_truncated = 6;
}

message GetProbeSnapshotRequest {
  testpilot.common.v1.RequestContext ctx = 1;
  string session_id = 2;
  string ref = 3;                               // 可选：子树定位（ARIA YAML 路径，如 "main / form"），空 = 全页
}
message GetProbeSnapshotResponse {
  string final_url = 1;
  string title = 2;
  string aria_snapshot = 3;
  bool snapshot_truncated = 4;
}

message CloseProbeRequest {
  testpilot.common.v1.RequestContext ctx = 1;
  string session_id = 2;
}
message CloseProbeResponse { bool ok = 1; }

message ActProbeRequest {
  testpilot.common.v1.RequestContext ctx = 1;
  string session_id = 2;
  testpilot.common.v1.UiActionStep action = 3;   // 复用 common 的 UI 动作（与声明式步骤同构）
}
message ActProbeResponse {
  string final_url = 1;
  string title = 2;
  string aria_snapshot = 3;      // 动作执行后自动回快照（一步一反馈）
  bool snapshot_truncated = 4;
  string error = 5;              // 空 = 成功；非空为 Playwright 原始报错（locator/状态/超时）
}

message EvalProbeRequest {
  testpilot.common.v1.RequestContext ctx = 1;
  string session_id = 2;
  string expression = 3;         // JS 表达式或 `() => {...}` 函数体（Playwright evaluate 语义）
}
message EvalProbeResponse {
  string result_json = 1;        // JSON 序列化结果（截断至 eval_max_bytes）
  bool result_truncated = 2;
  string error = 3;              // JS 异常原文
}
```

**命名与风格**：遵循文件内既有约定——请求首字段 `ctx`、驼峰 RPC、`Request/Response` 成对、注释写中文语义。`UiActionType` 若 common 里已有等价枚举（`UI_ACTION_*`）则直接 import 复用，不新造枚举（实施时以 `buf lint` 为准）。

#### 4.1.2 `proto/testpilot/worker/v1/worker.proto`（Scheduler ↔ Worker）

下行命令与上行事件各追加一个 oneof 成员（**字段号接续现有最大值，不改动既有成员**）：

```proto
message WorkerEvent {
  oneof event {
    // …register=1 …artifact=7 既有…
    ProbeReply probe_reply = 8;    // UI 探测回执（v1 新增）
  }
}

message SchedulerCommand {
  oneof command {
    // …task=1 cancel=2 config=3 既有…
    ProbeCommand probe = 4;        // UI 探测命令（v1 新增）
  }
}

message ProbeCommand {
  string request_id = 1;           // Scheduler 生成，回执原样带回（配对键）
  string session_id = 2;
  int64 tenant_id = 3;             // 冗余下发，Worker 侧做会话归属校验
  google.protobuf.Duration timeout = 4;  // 单命令执行上限（Scheduler 权威值）
  oneof op {
    ProbeOpen open = 5;
    testpilot.common.v1.UiActionStep act = 6;   // 复用 common 的 UI 动作（与 copilot.proto 同构）
    ProbeEval eval = 7;
    ProbeSnapshot snapshot = 8;
    ProbeClose close = 9;
  }
}

message ProbeOpen {
  string url = 1;                  // 绝对 URL（Scheduler 已解析 base_url，Worker 不做模板渲染）
  int32 snapshot_max_bytes = 2;    // 快照截断上限（Scheduler 权威值）
  bool record = 3;                 // 是否录 HAR/tracing（探测默认 false，见 4.3.2）
}

message ProbeEval {
  string expression = 1;
  int32 result_max_bytes = 2;      // 结果截断上限
}

message ProbeSnapshot {
  string ref = 1;                  // 子树定位，空 = 全页
  int32 snapshot_max_bytes = 2;
}

message ProbeClose { string reason = 1; }   // user | ttl | limit | worker_shutdown

message ProbeReply {
  string request_id = 1;
  string session_id = 2;
  oneof payload {
    ProbeState state = 3;          // open/act/snapshot 成功
    ProbeEvalResult eval = 4;      // eval 成功
    ProbeAck ack = 5;              // close 成功
    ProbeFailure failure = 6;      // 任何失败（含超时/会话不存在）
  }
}

message ProbeState {
  string final_url = 1;
  string title = 2;
  string aria_snapshot = 3;
  bool snapshot_truncated = 4;
}

message ProbeEvalResult {
  string result_json = 1;
  bool result_truncated = 2;
}

message ProbeAck { string session_id = 1; }

message ProbeFailure {
  string code = 1;    // PROBE_SESSION_NOT_FOUND | PROBE_TIMEOUT | PROBE playwright 透传类（见 4.9）
  string message = 2; // 报错原文（Playwright error 原样，Scheduler 原样透传给 Copilot）
}
```

**前向兼容验证点**：Worker `client.py` 命令循环现状为 `which = cmd.WhichOneof("command")` 后 `if/elif` 三分支，未知值自然落入无分支（忽略）——旧 Worker 收到 `probe` 命令安全忽略；旧 Scheduler 不发 `probe` 命令，新 Worker 无感。**混跑窗口安全。**

#### 4.1.3 生成产物（改 proto 后必提交的 6 处）

`scheduler/gen/`、`worker/src/testpilot/`、`copilot/src/testpilot/`（不含 worker.proto）、`scheduler/internal/grpcserver/schema.json`、`copilot/src/testpilot_copilot/grounding/`（若 schema 变更）、`buf.yaml`（如需）。CI `proto-check.sh` 零漂移强制。

### 4.2 Scheduler 侧

#### 4.2.1 新包 `internal/probe/probe.go`

```
type Hub struct {
    mu       sync.Mutex
    sessions map[string]*Session            // session_id → Session
    pending  map[string]chan *workerv1.ProbeReply   // request_id → 回执通道（仿 Dispatcher.RegisterWaiter）
    disp     *dispatch.Dispatcher
    cfg      Config
}

type Session struct {
    ID, TenantID, UserID, WorkerID string
    State    State                 // active / idle / dead
    BaseURL  string
    CreatedAt, LastActiveAt time.Time
    closeCh  chan struct{}
}
```

核心方法签名：

```go
func New(disp *dispatch.Dispatcher, cfg Config) *Hub
func (h *Hub) Open(ctx context.Context, tenantID int64, userID, sessionID, url, envID string) (*workerv1.ProbeState, error)
func (h *Hub) Act(ctx context.Context, tenantID int64, sessionID string, act *commonv1.UiActionStep) (*workerv1.ProbeState, error)
func (h *Hub) Snapshot(ctx context.Context, tenantID int64, sessionID, ref string) (*workerv1.ProbeState, error)
func (h *Hub) Eval(ctx context.Context, tenantID int64, sessionID, expression string) (*workerv1.ProbeEvalResult, error)
func (h *Hub) Close(tenantID int64, sessionID, reason string) error
func (h *Hub) Deliver(reply *workerv1.ProbeReply)          // grpcserver 收流循环调用：pending 配对唤醒
func (h *Hub) OnWorkerDisconnect(workerID string)          // worker 断连钩子：相关会话置 DEAD
func (h *Hub) Sweep()                                      // reaper 周期调用：TTL/上限清理
```

#### 4.2.2 关键流程与规则

1. **选 Worker**（仅 `Open` 与 IDLE→ACTIVE 重建时）：候选 = `disp` 在线且 `CAPABILITY_PLAYWRIGHT` 且非压测占用；租户独占 Worker（`tenant_id` 绑定）优先；负载（`Load()`）最低者胜。无候选 → `PROBE_NO_WORKER`。
2. **粘性路由**：ACTIVE 会话命令只发该 `worker_id`；`w.Send <- cmd` 满或 Worker 已关闭 → `PROBE_WORKER_BUSY` / 触发会话 DEAD。
3. **pending 配对**：发命令前 `pending[request_id] = make(chan, 1)`；`Deliver` 按 `request_id` 唤醒；等待 `min(cmd.timeout, 60s)`，超时返回 `PROBE_TIMEOUT` 并删除 pending（Worker 迟到回执投递不存在的 pending 时静默丢弃——`Deliver` 必须容忍）。
4. **并发与限额**（`Config`，见 §4.7）：每 Worker ACTIVE 会话数、每租户 ACTIVE 会话数、全局会话数三道闸；超限 `PROBE_LIMIT`。
5. **reaper**：挂入 `main.go` 既有 reapers 组（与 artifact 清理同款节奏，30s 周期）：扫 `LastActiveAt` 超过 `idle_ttl` 的会话 → 发 `close(ttl)`（尽力而为）→ 置 IDLE；超 `max_lifetime`（默认 60min）→ `close(limit)` → DEAD。
6. **base_url 解析**：`env_id` → GORM 查 `Environment`（租户校验），`url` 相对路径拼 `base_url`（与 `runner.buildExecutionEnv` 的拼接规则一致）；**变量不注入探测会话**（v1 明确不做 `{{vars}}` 渲染，快照/动作都是字面量——探测的是真实页面形态，模板渲染属于用例运行期语义）。
7. **审计**：`Open/Act/Eval` 三个写类 RPC 写 `AuditLog`（复用 Copilot 写工具既有审计路径，`detail` 记 `session_id/url/action`，**不记 eval 表达式全文超过 1KB 截断**）。

#### 4.2.3 grpcserver 接线

- `copilot_service.go`：新增 5 个 handler，鉴权沿用 `Ctx` 反射 + JWT 交叉校验；RBAC：5 个 RPC 全部 `RoleMember`（探测能触达被测系统，与"创建用例"同级，viewer 不放行）。
- `worker_service.go` 的收流循环：`WorkerEvent` 分支新增 `probe_reply` → `s.Probe.Deliver(...)`（`Server` 结构体加 `probe *probe.Hub`，`main.go` boot 顺序：dispatch 之后、gRPC 之前构造；关停顺序无需变化——Hub 无独立流，仅依赖 `Dispatcher`）。
- Worker 断连处（`Unregister` 调用点旁）加 `probe.OnWorkerDisconnect(workerID)`。

### 4.3 Worker 侧

#### 4.3.1 新文件 `worker/src/testpilot_worker/probes.py`

```python
@dataclass
class ProbeSession:
    id: str
    tenant_id: str
    session: ui.UiSession | None = None      # 惰性创建
    created_at: float = 0.0
    last_active: float = 0.0

class ProbeHub:
    """Scheduler 下行 probe 命令的执行端。
    硬顶护栏（防 DoS，勿随意放宽）：
      - 每 Worker ACTIVE 会话 ≤ TP_PROBE_MAX_SESSIONS（默认 2）
      - 单命令硬超时 ≤ TP_PROBE_HARD_TIMEOUT（默认 90s，取 min(cmd.timeout, 硬顶)）
      - 快照/eval 结果体积按命令携带上限截断，Worker 侧另有绝对上限（快照 32KB / eval 8KB）
    """
    def __init__(self, artifact_root: Path): ...
    async def handle(self, cmd: pb.ProbeCommand) -> pb.ProbeReply: ...
```

- 会话浏览器复用 `ui.UiSession`，惰性启动（首次 `open`/`act` 才 launch Chromium），`base_url` 取自 `ProbeOpen.url` 的 origin（不依赖用例环境）。
- 产物目录：`{TP_ARTIFACT_DIR}/probe/{session_id}/`（失败截图可落此处，uri 直接对前端 artifact 代理可见，人可查看——但 v1 不回传 LLM）。

#### 4.3.2 `UiSession` 小改（`ui.py`）

构造参数增加 `record: bool = True`：

- `record=False`（探测默认）：`new_context` 不传 `record_har_path`、不 `tracing.start`，`finish()` 不导出 trace/har（探测高频开关会话，trace 文件是无意义膨胀）；`failure_screenshot()` 保持可用。
- 既有用例路径（engine 调用）行为不变，默认值兼容。

#### 4.3.3 ARIA 快照（v1 的"眼睛"）

```python
async def aria_snapshot(session: ui.UiSession, ref: str, max_bytes: int) -> tuple[str, bool]:
    """优先 page.locator("html").aria_snapshot()（YAML，含 role/name/[ref]）；
    ref 非空时定位子树：按上一轮快照的 YAML 路径逐级 locator(role).nth(...) 下钻；
    Playwright 异常（旧版本）回退 page.accessibility.snapshot()（deprecated，仍可用）。
    超 max_bytes 截断（按 YAML 顶层块边界截，保证 YAML 可解析），truncated=True。"""
```

- 截断策略：按顶层缩进块保留（浏览器主内容优先），尾部追加 `\n… [截断，共 N 字节；可用 ref 参数取子树]`。
- Worker 绝对上限 32KB（命令上限与之取 min）。

#### 4.3.4 `eval_js` 执行（v1 逃生舱）

```python
async def eval_js(session: ui.UiSession, expression: str, max_bytes: int, timeout_s: float):
    """page.evaluate(expression)：
    - 表达式含箭头函数/function 则原样；否则包一层 `(expr)`（Playwright 语义已兼容，无需包裹）；
    - 结果必须 JSON 可序列化（Playwright 结构化克隆 + evaluate 返回值约定），非序列化返回 str(repr) 截断；
    - 超时由 asyncio.wait_for 控制（默认 10s，硬顶 30s）；
    - JS 异常原文（含行号）放入 failure.message —— 这是纯文本反馈的主通道之一。"""
```

**安全边界说明**：JS 运行在**被测页面**的 origin 里，等价于页面自身脚本能力，不触及 Worker 进程凭据/文件系统；Worker 侧不做白名单（无法可靠静态判断 JS 语义），依赖审批 + 审计 + 体积/超时护栏。

#### 4.3.5 `client.py` 接线

收流循环（`client.py:100` 附近）新增：

```python
elif which == "probe":
    asyncio.create_task(self._handle_probe(cmd.probe))
```

`_handle_probe`：`probe_hub.handle(cmd)` → 经既有事件上行通道发 `WorkerEvent(probe_reply=…)`（复用 outbox 或直接 `send_event`，**必须走既有有界 outbox 语义**，探测回执丢失时 Scheduler 侧靠 pending 超时兜底，不阻塞任务事件）。

### 4.4 Copilot 侧（`tools.py`）

新工具集 `probe = FunctionToolset()`，5 个工具（挂入 `build_agent` 的 `toolsets=[readonly, writes, probe]`）：

```python
@probe.tool
async def ui_probe_open(ctx, url: str, env_id: str | None = None) -> dict:
    """打开 UI 探测会话并返回页面 ARIA 快照（写操作，需审批）。
    返回的快照是页面可交互元素的结构化文本（role/name/层级），据此选择 locator，
    不要凭空猜测 selector。相对路径基于环境 base_url。会话空闲 10 分钟自动回收。"""

@probe.tool
async def ui_probe_snapshot(ctx, ref: str = "") -> dict:
    """获取当前页面 ARIA 快照（免审批）。ref 为上一次快照中的子树路径，用于聚焦局部、
    节省上下文；跳转/弹窗后可反复调用观察页面变化。"""

@probe.tool
async def ui_probe_act(ctx, action: str, target: str = "", value: str = "") -> dict:
    """在探测会话上执行单步动作（写操作，需审批），执行后自动返回新快照。
    action: goto/click/fill/select/check/uncheck/hover/press/wait。
    target 为 Playwright locator；失败时 error 字段为 Playwright 原始报错
    （包含等待超时与元素状态），应据此修正 locator 重试。"""

@probe.tool
async def ui_probe_eval(ctx, expression: str) -> dict:
    """在页面上下文执行 JS 并返回 JSON 结果（写操作，需审批）。
    用于固定动作覆盖不了的探测：枚举候选元素、检查元素状态/属性、多策略验证 locator、
    读取页面状态。例：`[...document.querySelectorAll('button,a')].map(e => ({t: e.textContent.trim(), id: e.id}))`
    结果有体积上限，避免返回整个 DOM。"""

@probe.tool
async def ui_probe_close(ctx) -> dict:
    """关闭探测会话（免审批）。探测结束、开始生成用例前应调用。"""
```

实现要点：

- **deps 扩展**（`CopilotDeps`）：`probe_session_id: str`（chat 会话建立时生成，随 deps 注入；工具不再让 LLM 传 session_id，杜绝串会话）+ `probe_grants: set[str]`（v1.1 会话级放行用，v1 预留字段）。
- **deadline**：`_DeadlineProxy` 默认 30s，对 `Open/Act/Eval` 单独放宽到 70s（> Scheduler pending 60s 上限，保证错误从 Scheduler 侧返回而非客户端超时）。
- **结果体积**：工具返回前二次截断快照（`TP_COPILOT_PROBE_SNAPSHOT_MAX_BYTES`，默认 16KB）——上下文压缩（阈值 0.7、keep 6 条）对高频快照压力很大，工具描述里明确指导"能用 ref 就别拉全页"。

### 4.5 审批策略（HITL 矩阵）

| 工具 | RPC | 审批 | 理由 |
|---|---|---|---|
| `ui_probe_open` | OpenProbe | ✅ 写类 | 启动浏览器资源 + 首次触达被测系统，用户知情 |
| `ui_probe_snapshot` | GetProbeSnapshot | ❌ 只读 | 纯读 |
| `ui_probe_act` | ActProbe | ✅ 写类 | click/fill 对被测系统有副作用（可能点掉"删除"） |
| `ui_probe_eval` | EvalProbe | ✅ 写类 | 任意 JS，等同脚本执行 |
| `ui_probe_close` | CloseProbe | ❌ 只读 | 释放资源 |

- v1 审批粒度 = **逐次**（沿用 DeferredToolRequests 既有机制，零前端改动）。
- **v1.1（可选增强，前端小改）**：审批弹窗加"本会话内放行 UI 探测"复选框 → 回执带 scope → Copilot 写 `deps.probe_grants`，同类工具不再 defer。**不动 proto**（scope 放在审批回执的前端→Copilot 私有通道里），后置实施。
- Scheduler 对写类 RPC 落审计（见 4.2.2-7）。

### 4.6 提示词与 grounding（`prompts/system.md`）

「Playwright UI 用例生成」节追加工作流指导（v1 随工具一起提交）：

```markdown
## UI 探测（用户描述模糊时的标准流程）
- 用户描述无法精确到具体元素（"找到登录按钮"）时，先 ui_probe_open 打开页面读快照，
  绝不凭空猜测 selector；
- 从 ARIA 快照选择 locator 的优先级：role+name（getByRole 语义的 CSS 等价）> data-testid >
  唯一 id > 稳定文本；避免位置型（nth）与自动生成类名；
- 动作后自动返回新快照，逐步推进：打开 → 定位 → 点击 → 观察跳转 → 定位输入框 → 填写 → 提交 → 断言落点；
- 枚举候选、检查元素属性、多策略验证 locator 用 ui_probe_eval；结果体积受限，只取需要的字段；
- 探测确认完整流程后 ui_probe_close，再 create_ui_test_case 固化（必须含 expect_* 断言），
  最后建议 trigger_run 验证；探测会话与用例运行是两回事，不要混用；
- ui_probe_act/eval 失败时 error 是 Playwright 原文（哪个 locator、什么状态、超时多久），
  按报错修正而不是换随机 selector 重试。
```

`grounding/domain-schema.json`：若该文件承载工具清单则同步 5 个新工具（实施时确认）。

### 4.7 配置新增（三级：CLI > TP_* env > YAML > 默认）

| 组件 | 键 | 默认 | 说明 |
|---|---|---|---|
| Scheduler | `probe_enabled` | `false` | 总开关（灰度），关闭时 5 个 RPC 返回 `PROBE_DISABLED` |
| Scheduler | `probe_session_idle_ttl` | `10m` | 空闲回收 |
| Scheduler | `probe_session_max_lifetime` | `60m` | 强制回收 |
| Scheduler | `probe_max_sessions_per_worker` | `2` | ACTIVE 会话硬顶 |
| Scheduler | `probe_max_sessions_per_tenant` | `1` | 租户 ACTIVE 硬顶 |
| Scheduler | `probe_cmd_timeout` | `60s` | pending 等待上限（proto 下行给 Worker） |
| Scheduler | `probe_snapshot_max_bytes` | `16KB` | 下行快照上限（Worker 另有 32KB 绝对顶） |
| Scheduler | `probe_eval_max_bytes` | `4KB` | 下行 eval 结果上限 |
| Copilot | `TP_COPILOT_PROBE_SNAPSHOT_MAX_BYTES` | `16KB` | 工具结果二次截断 |

配置逐键模板同步：`deploy/scheduler.yaml.example`、`deploy/copilot.yaml.example`。Scheduler 侧按惯例**只需加结构体成员 + 默认值**（反射自动派生 CLI/env）。

### 4.8 限额与安全分析

| 威胁 | 缓解 |
|---|---|
| 探测会话泄漏（浏览器进程堆积） | 状态机 TTL 双闸 + reaper + Worker 硬顶 + `probe_enabled` 总开关 |
| 租户越权探测他人会话 | 所有 RPC 校验 `session.tenant_id == ctx.tenant_id`；Worker 侧二次校验 `ProbeCommand.tenant_id` |
| eval_js 打爆内存/上下文 | 结果体积双截断 + 执行超时 + 审批 + 审计 |
| 借探测通道绕过通知私网限制 | 探测目标 URL 不做私网过滤（与测试运行同语义——被测系统本来就可能是内网），但**记录审计**（url 全文） |
| 会话跨用户串用 | session_id 由 deps 注入而非 LLM 参数，用户级绑定 |
| 与正式 run 资源互抢 | probe 会话数硬顶（每 Worker 2）远小于任务并发；`InStress()` 的 Worker 不参与探测选点 |

### 4.9 错误码（`internal/apperr` + `docs/error-codes.md` 同步）

| code | HTTP | 场景 |
|---|---|---|
| `PROBE_DISABLED` | 403 | 总开关关闭 |
| `PROBE_NO_WORKER` | 503 | 无具备 PLAYWRIGHT 能力的在线 Worker |
| `PROBE_LIMIT` | 429 | 会话数超限 |
| `PROBE_SESSION_NOT_FOUND` | 404 | 会话不存在/已 DEAD |
| `PROBE_TIMEOUT` | 504 | 命令执行超时 |
| `PROBE_FAILED` | 500 | Worker 侧执行失败（message 透传 Playwright 原文） |

### 4.10 测试计划

| 层 | 用例 |
|---|---|
| Worker 单测 | ProbeHub 会话状态机（open/act/idle→重建/dead）；快照截断（块边界、ref 子树）；eval_js 序列化失败回退；UiSession record=False 不产生 har/trace；未知命令忽略回归 |
| Scheduler 单测 | Hub pending 配对/超时；粘性路由（Worker 断连→DEAD）；三道限额闸；租户隔离（跨租户 session_id 404）；reaper TTL |
| Copilot 单测 | 5 个工具的请求构造/结果截断/deps 注入 session_id；审批分类（readonly vs 写类）；deadline 覆盖 |
| E2E（`scripts/e2e_probe.py`，离线无 LLM） | echo 服务新增静态页 `/probe-login`（含用户名/密码/登录按钮表单 + 登录后页面）；直连 Scheduler gRPC 依次 Open→Snapshot（断言快照含登录按钮）→Act(fill/click)→Snapshot（断言跳转后文案）→Eval（断言 cookie/localStorage）→Close；再覆盖：TTL 回收、无 Worker（停 worker 后 Open 报 PROBE_NO_WORKER）、租户隔离 |
| LLM 级验证（人工/e2e_copilot 扩展） | 模糊描述"测试一下登录流程" → 观察 agent 是否走 probe → 快照 → act 循环 → create_ui_test_case |

### 4.11 提交物清单

1. proto 两份 + `scripts/proto-gen.sh` 产物（6 处）
2. Scheduler：`internal/probe/`（新）、`grpcserver/copilot_service.go`、`grpcserver/worker_service.go`、`server.go`（gRPC server 构造）、`main.go`（boot/reaper）、`config`、`apperr`、`deploy/scheduler.yaml.example`
3. Worker：`probes.py`（新）、`ui.py`（record 参数）、`client.py`（probe 分支）、`config`（硬顶参数）
4. Copilot：`tools.py`（probe 工具集）、`agent.py`（toolsets 挂载）、`scheduler_client.py`（deadline 覆盖）、`prompts/system.md`、`config.py`、`deploy/copilot.yaml.example`
5. 文档：本文状态更新、`docs/error-codes.md`、`CLAUDE.md`（工具计数 12+8+2 → 15+10+2）、`docs/design.md` 链接

---

## 5. V1 顺手项：`TestStepResult` 补 `error` 字段

**现状**（`common/v1/types.proto:595`）：`TestStepResult` 无 error 字段；步骤失败信息只进 case 级 error（`engine.py:175` 取最后一次失败文案）与 `step_progress` detail（实时事件，不落库）。`get_run(include_steps=true)` 拿不到逐步报错——这既是探测工作流的反馈基础，也是所有失败根因分析的通用缺口。

改动：

```proto
message TestStepResult {
  // …既有 1..10…
  string error = 11;   // 步骤失败原因原文（成功为空；v1 新增）
}
```

- Worker `engine.py`：`_record` 增加 `error` 参数，`StepFailure` 捕获处（`_run_step` 失败路径）把 `str(e)` 记入对应 step result；
- Scheduler 侧 step results 落库为 protojson，新增字段自然透传，无迁移；
- Copilot `get_run` 工具描述补充"include_steps=true 时每步带 error 原文"。

兼容性：追加字段，旧 Worker 不写、旧前端不读，零破坏。

---

## 6. V2 详细设计：`run_py` 常驻沙箱（设计预留，不随 v1 实施）

### 6.1 目标

让 agent 写**一段 Python**在探测会话上执行（枚举+打分+重试的机械搜索、复杂等待逻辑），一段脚本顶多轮工具调用；同时不破坏沙箱铁律：**用户/LLM 代码永远不在 Worker 进程内执行**（Worker 持凭据）。

### 6.2 关键决策：复用 lowcode 沙箱 + 通用桥操作

v2 的唯一新增桥操作（扩 `testpilot_sdk` 能力桥 + Worker 桥处理器）：

```
op = "ui_call"          # 通用 UI 门面调用
args = { "method": "click|fill|goto|evaluate|aria_snapshot|wait_for_selector|content|…",
         "args": [ ... ] }        # 白名单方法 + 位置参数
返回 = { "ok": true, "result": … } / { "ok": false, "error": … }
```

- **白名单**：Worker 侧 `UI_CALL_METHODS` 显式枚举可代理的 page 方法（v1 的 `ui_action` 动作集 + `evaluate/aria_snapshot/content/wait_for_selector/locator introspection`），每个方法声明参数类型签名，桥处理器做校验后转发 `UiSession.page`。
- v1 的 `eval_js` 在 v2 中退化为 `ui_call(evaluate)` 的糖，通道统一。

### 6.3 常驻沙箱执行模型

```
ProbeSession 持有：
  UiSession（浏览器，v1 已有）
  + sandbox: SandboxProc（v2 新增，惰性启动）

sandbox 进程 = python -m testpilot_sdk.entry --mode repl
  stdin : {"type":"exec","id":N,"source":"async def run(ctx): …"}
  stdout: {"type":"done","id":N,"ok":true,"repr":"…","logs":[…]} / {"type":"error",…}
  既有 op 通道（bridge.call）继续可用 → ui_call → Worker → UiSession
```

- **exec 语义**：每次 `exec` 在持久 namespace 里 `exec(source)`，约定入口 `async def run(ctx)`，Worker 侧 `asyncio.run` 调用并捕获返回值/stdout（`contextlib.redirect_stdout`，行数上限 200）。namespace 跨 exec 保留（可定义 helper 函数复用），`ctx` 每次注入同一 ProbePage 门面。
- **`ctx` 门面**：`ctx.page`（SDK `Page` 扩展 v2 方法：`content()/evaluate()/aria_snapshot()/wait_for_selector()…`，全部走 `ui_call` 白名单）、`ctx.log(*args)`（结构化回传，不计 print 上限）。
- **崩溃恢复**：sandbox 进程 crash/超时被杀 → 下次 exec 自动重启（浏览器在 Worker 侧不受影响）；连续 3 次失败则会话置 DEAD。
- **护栏**（在 lowcode 既有护栏上叠加）：单 exec 超时默认 30s（Scheduler 下行权威，Worker 硬顶 60s）；脚本体积 ≤ 16KB；stdout ≤ 200 行；每会话 exec 频率 ≤ 1 次/2s（防死循环刷流）；沙箱进程内存 ceiling 复用 lowcode 配置。
- **proto**：`ProbeEval` 旁新增 `ProbeRun{ source=1; entry=2 }` → `ProbeReply.payload` 新增 `ProbeRunResult{ repr=1; logs=2; truncated=3 }`；Copilot 对应工具 `ui_probe_run(source)`（写类审批）。

### 6.4 v1 → v2 的演进兼容

- v1 的会话管理/路由/审批/审计/截断框架**原样复用**，v2 只加一种 op 与一种工具；
- proto 变更保持 oneof 接续编号，新旧混跑策略同 v1（未知 oneof 成员双方静默忽略）；
- 若 v1 实测 `eval_js` 已覆盖 90% 场景，v2 可降级为"不实施"——这是把 v2 做成预留的初衷。

---

## 7. 实施顺序与发布策略

| 阶段 | 内容 | 交付判据 |
|---|---|---|
| 0 | §5 TestStepResult.error（独立可先行） | 单测 + get_run 返回逐步 error |
| 1 | proto 契约冻结（4.1 全部消息）→ `proto-gen.sh` | `proto-check.sh` 绿 |
| 2 | Worker：`ui.py` record 参数 + `probes.py` + `client.py` 分支 | Worker 单测绿；手动连 dev Scheduler 无回归 |
| 3 | Scheduler：`internal/probe` + grpcserver 接线 + 错误码 + 配置 | Scheduler 单测绿；`probe_enabled=false` 时 RPC 403 |
| 4 | Copilot：工具集 + deps + deadline + 提示词 + 配置 | Copilot 单测绿 |
| 5 | E2E `e2e_probe.py` + echo `/probe-login` 测试页 | 全栈 e2e 绿 |
| 6 | 灰度：dev 开 `probe_enabled`，LLM 级人工验证后再默认放开 | 按钮流程实测走通 |

分支策略：阶段 1-2 一条分支（proto+worker），3-5 各自可独立 PR；全程不 commit 到 main，迭代验收（用户既定策略）。

## 8. 风险与开放问题

| # | 风险/问题 | 倾向 |
|---|---|---|
| 1 | ARIA 快照对 canvas/复杂组件信息不足 | 接受（快照+eval_js 组合已优于盲猜）；v2 run_py 兜底 |
| 2 | 高频快照撑爆 64k 上下文 | ref 子树聚焦 + 二次截断；必要时 `context_window` 上调（128K 模型） |
| 3 | 探测写动作（如点击"删除"）造成被测系统副作用 | 审批逐次 + 审计；v1.1 会话级放行属 UX 增强，不改安全语义 |
| 4 | `aria_snapshot()` 的 ref 子树定位实现复杂度 | 可先只支持全页快照（ref 传空），子树作为 v1.x 增强——工具契约已预留 ref 字段 |
| 5 | 多 Worker 时登录态迁移（Worker 重启后会话 DEAD） | 接受：重新 open，快照会如实反映新页面 |
| 6 | Copilot 采样参数（已落地）与探测循环的稳定性 | 建议生产配置 `temperature: 0.2`；探测工作流对采样敏感，e2e_copilot 扩展用例覆盖 |
| 7 | CLAUDE.md/docs 的工具计数与链路描述需随合并更新 | 列入提交物清单（4.11-5） |
