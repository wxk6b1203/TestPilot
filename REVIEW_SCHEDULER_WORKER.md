# TestPilot Scheduler & Worker 代码审查报告

审查范围：`scheduler/`（Go 1.25 + fiber v3 + GORM + gRPC）与 `worker/`（Python 3.13 + grpcio + asyncio）全部源码。
方法：4 路并行子代理分模块审查 + 主审查人逐文件人工核对（重点交叉验证调度链路、沙箱、认证三块）。

---

## 一、严重问题（P0）

### A. 调度链路（scheduler/internal/dispatch|runner|grpcserver）

**A1. gRPC 完全零认证：Worker 注册/结果上报/Copilot 工具面均可伪造**
- `cmd/scheduler/main.go:59-62`（grpc.NewServer 无拦截器）；`grpcserver/worker_service.go:27-53`（register 首帧即入池）；`copilot_service.go`（只信任请求内自带的 RequestContext）
- 后果：TCP 可达 :9090 即可 1) 注册任意 tenant 的 Worker → 收到该租户全部任务（内联 API 定义、明文变量含敏感值、脚本源码）→ 跨租户窃取；2) 伪造 TaskResult 按 case_result_id 无条件 UPDATE（`dispatch.go:389-455` 无租户校验）→ 篡改任意租户结果、触发 run 收尾；3) 以任意租户/用户调 Copilot 工具面，完全绕过 REST 的 JWT+RBAC
- 修复：gRPC 认证拦截器（Worker 令牌/mTLS；Copilot 复用 JWT），TaskResult 按派发时 task_id→tenant 绑定校验归属

**A2. 派发路径 send-on-closed-channel panic 竞态（可击穿进程）**
- `dispatch.go:216-232 Dispatch` / `278-291 DispatchStress` 在无锁状态下向 `w.Send` 发送；`worker_service.go:57` defer 中 `close(w.Send)`。worker 断连瞬间 → select 发送到已关闭 channel → panic → 整个 Scheduler 崩溃（无 recover 链）
- 修复：发送前持锁重查 worker 仍在线；或 per-worker closed 标记 + select 监听；或改为不 close channel

**A3. 失联 Worker 无剔除 + run 状态机无 reaper：重启/断网后永久卡死**
- 无心跳超时判定（ConnectedAt 无人消费；gRPC 无 keepalive 参数）；死 worker 留在池中持续被派发（每次阻塞 5s）；已派发未回报的 case 永远 RUNNING → `maybeFinishRun` 的 done<total 恒成立 → run 永久 RUNNING；cron overlap 策略按 RUNNING 计数 → 定时任务被永久跳过；进程重启后历史 RUNNING 无启动恢复
- 修复：keepalive + 心跳超时剔除 + 启动时 RUNNING→ABORTED + 超时 reaper

**A4. 压测 remaining 计数与实发数不一致：压测永久挂起 + Worker 永久独占**
- `runner/stress.go:134` 派发前 RegisterStressRun(n)；`165-167` DispatchStress 失败仅 continue，不递减 remaining → `handleStressResult`（dispatch.go:332）永不收尾：StressRun 永久 RUNNING、stressRuns 泄漏、已派发 worker 的 stress 独占标记不解除（永久退出功能调度）
- 修复：按实际 dispatched 登记或失败时递减；StressRun 加整体超时兜底

**A5. 跨租户创建漏洞（tenant_id 可注入）**
- `httpserver/json.go` 的 `setIntField` 只在字段为 0 时赋值，`assignIDs` 基于它绑定租户；而 `decode()` 允许请求体携带 `tenant_id`（字符串/数字均可）→ `createProject/createEnvironment/createVariable/createCase/createPlan/createSuite/createGrpcAPI/createProtoFile/createScript` 全部可把资源创建到任意租户（member 角色即可）；`id` 同理可自定义主键。createStressPlan/createFolder 为强制赋值（安全）
- 修复：createOf 改为无条件强制 ID/TenantID（同 updateOf 的 forceIntField）

**A6. maybeFinishRun 无状态守卫：重复终结、覆盖 ABORTED**
- 并发回报最后两个 case 时两个 goroutine 都可能计数 done==total 并双写（双通知/双指标）；debug 超时置 ABORTED 后，迟到结果会把它改写为 PASSED/FAILED
- 修复：Updates 加 `WHERE status=RUNNING` + 检查 RowsAffected

### B. Worker 沙箱与执行（worker/src/testpilot_worker | testpilot_sdk）

**B1. 沙箱不是安全边界：同解释器、同用户、可直连网络、可读 Worker 凭据**
- `sandbox.py:151-166`：沙箱直接用 Worker venv 解释器，PYTHONPATH 含 worker/src，site-packages 完整；无 chroot/降权/文件系统隔离
- 后果：1) 可 import httpx 直接出网，完全绕过能力桥与 egress（OS 沙箱仅尽力而为，多数环境没有）；2) Linux 下可读 `/proc/<PPID>/environ`（同 uid 直系子进程）→ env scrub 形同虚设，Worker 全部凭据可读；3) 可读写 Worker 用户可达的任意文件
- 修复：容器/gVisor + 非 root + seccomp；OS 沙箱加文件系统规则；至少把 secrets 移出 Worker 进程环境

**B2. Linux bwrap 参数错误：空 rootfs，沙箱必启动失败**
- `sandbox.py:117-118`：`bwrap --unshare-net --dev /dev -- <cmd>` 无 `--ro-bind / /` → 空 rootfs 中 sys.executable 不存在 → exec 必败（Linux 生产环境低代码/行为压测全部不可用）
- 修复：`--ro-bind / / --proc /proc --dev /dev --tmpfs /tmp --die-with-parent` + CI 冒烟

**B3. 超时/取消路径系统性资源泄漏**
- `sandbox.py:219-231`：CancelledError（用例 wait_for 超时、任务取消）时 kill/wait/cancel/rmtree 全部被跳过 → 沙箱子进程成孤儿继续跑、scratch 泄漏
- `stress.py:128-146`：取消时 locust 子进程不杀、`await readers` 永久阻塞（等管道 EOF）→ 任务永不结束、并发信号量永不释放
- `ui.py`：浏览器收尾异常被静默吞
- 修复：全程 try/finally（killpg + wait + rmtree）、`start_new_session=True`、reader 循环加超时/看门狗

**B4. 超时后 result.ok 不重置 + 沙箱可伪造 result：崩溃/超时被判 PASSED**
- `sandbox.py:220-237`：超时分支不置 ok=False；沙箱写 fd 1 伪造 `{"type":"result","ok":true}` 即可让 done.set() → 超时/崩溃后返回 ok=True，引擎只看 res.ok → 失败用例显示通过
- 修复：timeout 分支显式 ok=False；rc≠0 或 done 未置时无条件 ok=False；协议帧加随机 token 防伪造

**B5. SSRF 多处可绕过（防护形同虚设）**
- `http_exec.py:75-98`：声明式 api_call 默认 follow_redirects=True，egress.check_url 只查初始 URL → 302 到 127.0.0.1/metadata 不再校验（桥路径默认不跟随，行为不一致）
- `ui.py:85-88`：page.goto 任意 URL（私网/metadata/file://）→ 浏览器 SSRF + 本地文件外带；UPLOAD 把 value 当本机路径 → 任意文件读取上传
- `egress.py:39-48`：`socket.getaddrinfo` 阻塞式（无超时）→ 冻结事件循环（超时定时器都不触发）；check_url 与 httpx 各解析一次 → DNS rebinding TOCTOU
- `config.py:52`：egress_block_private 默认 False，SSRF 面默认打开
- 修复：逐跳校验重定向；goto/upload 前 scheme 白名单 + check_url；解析入线程池 + 绑定连接

**B6. UI 产物路径穿越：任意路径写文件（sanitize() 是死代码）**
- `ui.py:131-136/150-158`：截图文件名（用户 value）与下载文件名（suggested_filename 服务器可控）直接拼 `case_dir / name`；`sanitize()`（ui.py:210）定义了但全项目无一处调用 → `value="../../x.png"` 可写任意文件
- 修复：basename-only + 字符白名单 + `resolve().is_relative_to(case_dir)` 二次校验

**B7. egress/沙箱配置时序错误：CLI/YAML 配置静默失效**
- `egress.py:19-20`/`sandbox.py:43-49` 在模块 import 时读 env；`main.py:22-24` 在 entry() 运行期才 apply_environ 回写（且顶层 import client→engine→http_exec→egress 早已完成）→ 运维在 YAML/CLI 配的白名单、私网阻断、沙箱限额全部不生效（以为开了实际没开）
- 修复：配置改为惰性读取/显式注入 Settings，删除 env 回写时序依赖

**B8. 并发与资源无上限（租户可触发自我 DoS）**
- `engine.py:362-395` 并行 loop 对 count 无封顶：每个迭代新建 CaseRunner（各持 httpx client，含 ui_action 时各起一个 Chromium）→ count=1000 即拉起上千浏览器
- `sandbox.py:184-217`：op 任务 create_task 无上限；日志行数/行长无上限 → 沙箱刷日志 OOM Worker
- `http_exec.py:94-100`/`sandbox.py:310`：响应体先全量下载再截断（64KB/256KB 只截快照），大响应 OOM；桥响应 header 无上限
- `client.py:40`：断连期间 outbox 无界积压 → OOM；断线重连后旧结果被当新会话发出
- 修复：并行度/日志/响应体/op 数设上限并超限 kill；outbox 带会话代次

**B9. 表达式求值无资源上限（Worker 进程内 OOM）**
- `expr.py` AST 白名单本身扎实（无函数调用/dunder，Attribute 限 Mapping），但 `"x"*10**9`、`[0]*10**7`、超深嵌套均无长度/深度限制，且在 Worker 进程内求值；裸 RecursionError/TypeError 未包装成 ExprError（错误契约不一致）
- 修复：表达式长度/深度/乘法结果长度上限；RecursionError→ExprError

### C. HTTP 与身份安全

**C1. JWT 默认密钥不强制替换**：`config.go:72` 默认 `dev-secret-change-me`，生产未配置即被伪造任意 owner token。修复：启动检测弱密钥 Fatal + 校验 iss

**C2. /copilot-api/* 反向代理零认证**（server.go:221、proxy.go）——仅依赖 Copilot 侧自校验，无限流。修复：移入受保护组 + 限流

**C3. OIDC email 联结无验证锚点（账户接管面）**：oidc_handlers.go:179-239 仅凭 email 登录既有账号，不校验 email_verified、不持久化 IdP sub。修复：sub↔user 绑定表 + email_verified 校验

**C4. moveNode 可移入自身子树 → 树成环 → getProjectTree 递归栈溢出（自伤 DoS）**：tree_handlers.go:293-358 无环检测。修复：移动前校验目标父 path 前缀 + attachChildren 深度上限

**C5. 配额 TOCTOU**：quota.go check-then-act 非原子，并发 Trigger 双双通过 → 配额绕过。修复：concurrent_runs 用条件更新/行锁

**C6. 引用 ID 无租户校验（隔离不完整）**：updateOf 只保护 ID/TenantID，project_id/environment_id/case_id 可改任意值 → 跨租户关联污染（运行期被 runner 静默跳过或泄漏）

---

## 二、中等问题（P1）

| # | 位置 | 问题 |
|---|---|---|
| M1 | client.py:97-117 | 排队任务不可取消（tasks 注册在 semaphore 之后）；结果 put 在 try/finally 外，取消瞬间任务静默消亡无回执 |
| M2 | grpc_exec.py:29/108-117 | 无 deadline 的阻塞调用挂死 to_thread 线程池；描述符缓存键不含 target（同名 service 跨目标串类） |
| M3 | engine.py:345-360 | 循环迭代变量写入父 vars 且不恢复（vars[var]=i 残留、覆盖用户变量） |
| M4 | engine.py:362-395 | 并行 loop 各迭代共用同一 case_dir → 截图/trace/har 互相覆盖（clone 的 _ui 用相同产物目录） |
| M5 | client.py:38 | max_concurrency=0 → Semaphore(0) 静默吞掉全部任务 |
| M6 | handlers.go:592-607 | listWorkers 泄漏全部租户的 Worker 信息（id/tenant/caps/tags） |
| M7 | member_handlers.go:87-111 | admin 可修改自己角色自我提权；viewer 可无限创建租户 |
| M8 | oidc_handlers.go:69-75/193-196 | OIDC 回跳 token 放 URL fragment（历史/代理日志残留）；Host 头参与 redirect 判定可注入 |
| M9 | member_handlers.go:57-73 / oidc_handlers.go:205-230 | 非 RecordNotFound 的 DB 错误被当用户不存在 → 重复创建；查重+创建有竞态 |
| M10 | retention.go:41-59 | 与运行中任务竞态：长时间运行 run 被连根删除（worker 回报 UPDATE 被软删跳过 → 孤儿+配额永久占用）；Limit 500 无 ORDER BY 不稳定 |
| M11 | dispatch.go:399-409 | step 结果 tenant 查不到时静默落 tenant_id=0；mustInt64 解析失败静默归零（无日志） |
| M12 | notify.go:84-91 | webhook URL 无 SSRF 防护（租户 admin 可让 Scheduler 打内网）；每渠道一个 goroutine 不读 Body（连接不可复用） |
| M13 | server.go:74-75 | login/register 无速率限制（暴力破解面）；login 不检查 u.Status（禁用账号可登录） |
| M14 | copilot_handlers.go:64-98 | REST 写 Copilot 消息不扣 ai_calls 配额（注释承诺了配额，仅 gRPC 侧扣）——语义不一致 |
| M15 | handlers.go:121 | 注册审计 Detail 用字符串拼接 JSON（tenantName 含引号 → 非法 JSON/注入） |
| M16 | json.go:246/264/300/315 等 | 内部 DB 错误原文（SQLSTATE）回传客户端（信息泄漏） |
| M17 | handlers.go:565 / stress_handlers.go:108 | getRun N+1（循环内逐 case 查 steps）；getStressRun 全量加载指标点无上限 |
| M18 | stress.py:214-230/293-309 | 行为压测 gate→event 间崩溃致 inflight 泄漏 → 并发坍缩为 0 仍报 PASSED；task.timeout 被忽略 |
| M19 | stress.py:107-116 | 指标行字段缺失直接 KeyError 炸掉 run_stress |
| M20 | bridge.py:63/80-88 | 单条坏帧（id 非数字）→ 读线程崩溃 → 全部 bridge call 永久挂起；call 无超时 |
| M21 | sandbox.py:83-84 | RLIMIT_NPROC 是 per-user 限制，沙箱 fork 满可让 Worker 无法再起进程（DoS）；proc.kill() 不杀进程组 |
| M22 | sandbox.py:166 | preexec_fn 在线程化进程（grpc aio）中不安全 |
| M23 | main.py:51-54 | Worker 无 SIGTERM 处理：停机即泄漏孤儿进程，无任务回执 |
| M24 | scheduler/cmd/main.go | Scheduler 无优雅停机（无 signal 处理、无 GracefulStop）——与 A3 叠加 |

---

## 三、轻微问题与设计建议（节选）

- 雪花 ID 节点位硬编码 1（models.go:18/25-41）：多 Scheduler 实例部署必然主键冲突；时钟早于 epoch 时生成负 ID。建议配置注入 + 时钟保护
- summary 统计错误（dispatch.go:481）：skipped:0 硬编码，SKIPPED 的 case 被计入 passed
- debug waiter 泄漏（runner/debug.go:213-218）：超时路径不 TakeWaiter → waiters 永久残留
- cron overlap_policy=2 无法并发（cronsched.go）：robfig/cron 单 worker 串行执行，并发策略不生效；fire 用 context.Background() 无超时；overlap Count+Trigger 非原子
- handleStressResult 先删状态再落库（dispatch.go:335/359）：落库失败后无法重试
- 压测负载计数无效（dispatch.go:280-281）：DispatchStress 的 load+1/defer-1 立即归零；Locust 压测无 egress 检查、headers 不渲染 {{var}} 模板
- http_exec base_url 为空时静默产出相对 URL（报晦涩 httpx 错误）；render_map 渲染出 dict/list 值时 httpx TypeError
- 断言 MATCHES 无正则超时（assertions.py:136-142）：恶意正则 ReDoS 阻塞事件循环；$.a["b"] 引号键语法不支持且静默返回 None（NOT_EXISTS 误通过）
- 敏感数据回传：请求快照含完整 URL/query params、set_var 日志 value!r——token 可能落运行记录，建议脱敏
- 容器步骤无 StepResult、超时中断的步骤卡 RUNNING 进度（engine.py:200-204）
- notify 的 json.RawMessage(run.Summary)：Summary 为空时 marshal 失败
NaN
- register 无速率限制 + 默认凭据 admin/admin123 不轮换

---

## 四、做得好的地方（正向确认）

- SQL 全部参数化（GORM），无注入面；getOf/updateOf/deleteOf 均带 tenant_id 条件；PasswordHash/Secret/ClientSecret 全部 json:"-" 不外泄
- Local.resolve 与 S3 key 正确防路径穿越；switchTenant 校验成员关系
- 表达式引擎 AST 白名单扎实：无函数调用/dunder、Attribute 限 Mapping、f-string 被拒——无 RCE 面
- 能力桥协议设计（fd 隔离、stdout 协议帧、stderr 日志）清晰；低代码沙箱零凭据的意图明确（但实现未达预期，见 B1）
- Dispatcher 单一锁 + sync.Map + 有界 channel（32）：未发现死锁与 channel 无界增长
- 分页、ID 字符串化（JS 安全）、审计中间件只记成功变更等细节到位
- Python 3.13 兼容性：asyncio.TimeoutError 别名、to_thread、wait_for 语义均正确使用；ipaddress 正确判定 IPv4-mapped 地址

---

## 五、修复优先级建议

1. P0-认证与租户隔离：A1（gRPC 认证）+ A5（createOf 跨租户）+ C1（JWT 密钥）——三处合起来攻击者 TCP 可达即可读写全部数据
2. P0-进程稳定性：A2（panic 竞态）→ A3（失联剔除+重启恢复）→ A4（压测计数）→ A6（终结守卫）
3. P0-沙箱与 SSRF：B1（沙箱边界）→ B5/B6（SSRF 与路径穿越）→ B2（bwrap）→ B4（假 PASSED）
4. P1-资源与取消：B3（取消泄漏）→ B8/B9（无上限）→ M1/M2/M18
5. P1-外围：C2-C6、M6-M17 按性价比收敛

附：worker 侧完整分项报告已留存 `worker/REVIEW_SANDBOX_STACK.md`。

---

# 六、P0 修复记录（已实施并验证）

> 本轮已将全部 P0 问题修复完成。验证：`go build/vet/test` 16 包全绿；`pytest` 146 全绿；
> 新增跨租户回归测试 `TestCreateForcesTenantID`（scheduler/internal/httpserver/tenant_isolation_test.go）。

## Scheduler（Go）

| 编号 | 修复内容 | 位置 |
|---|---|---|
| A1 | gRPC 认证：Copilot 工具面校验 JWT + RequestContext 一致性；Worker 流校验 x-worker-token（未配置 token 拒绝注册） | grpcserver/auth.go（新）、config.go、cmd/scheduler/main.go |
| A2 | send-on-closed panic 竞态：Send 永不 close，改 closed 信号 + select 感知 | dispatch.go、grpcserver/worker_service.go |
| A3 | 失联剔除：gRPC keepalive + lastSeen 心跳 + ReapStaleWorkers + 启动恢复（RUNNING→FAILED）+ 2h 超时 reaper + 优雅停机 | dispatch.go、runner/recover.go（新）、main.go |
| A4 | 压测 remaining 与实发数对齐：派发失败递减 + 全败清理 + 落库成功后才删状态 | dispatch.go、runner/stress.go |
| A5 | 跨租户创建：assignIDs/createOf 强制覆盖 ID/TenantID + 回归测试 | httpserver/json.go、tenant_isolation_test.go（新） |
| A6 | maybeFinishRun/handleStressResult 状态守卫（WHERE status=RUNNING + RowsAffected）+ summary 修正（skipped 真实计数） | dispatch.go |
| C1 | JWT 弱密钥启动 Fatal + issuer/算法白名单校验 | main.go、auth/auth.go |
| C2 | /copilot-api/* 反代加 JWT 认证 | httpserver/server.go |
| C3 | OIDC：IdP sub 绑定优先 + email_verified 联结门槛 + RecordNotFound 判定 | auth/oidc.go、httpserver/oidc_handlers.go、model/models.go |
| C4 | moveNode 移入自身子树检测 + attachChildren 深度上限 | httpserver/tree_handlers.go |
| C5 | 配额 TOCTOU：CheckTx（PG 行锁 / SQLite IMMEDIATE 事务）+ Trigger/Debug 事务化 | quota/quota.go、runner/runner.go、runner/debug.go |
| C6 | 引用 ID 租户校验：validateRefs/ensureEntity 接入 createOf/updateOf 与 plan/suite/schedule/stress 创建 | httpserver/refs.go（新）及各 handler |
| M4 | SQLite WAL + busy_timeout(5s) + _txlock=immediate（并发写不再 SQLITE_BUSY） | db/db.go |

## Worker（Python）

| 编号 | 修复内容 | 位置 |
|---|---|---|
| B1（缓解） | token 从进程环境移除（防 /proc/<PPID>/environ 窃取）；sandbox-exec 受限环境探测降级。根治（容器/gVisor 隔离）属部署层工作 | main.py、config.py、sandbox.py |
| B2 | bwrap 空 rootfs 修复：--ro-bind / / + --proc/--dev/--tmpfs + --die-with-parent | sandbox.py |
| B3 | 取消/超时 finally 化：沙箱与 Locust 子进程必杀、管道必收、scratch 必清、信号量必释放 | sandbox.py、stress.py |
| B4 | 超时/非零退出无条件失败（防伪造 result 假 PASSED） | sandbox.py |
| B5 | 重定向逐跳 egress 校验；egress 异步化（executor + 3s 超时）；goto scheme 白名单 + egress；upload 路径限制；桥路径显式不跟随 | http_exec.py、egress.py、ui.py、sandbox.py |
| B6 | 产物文件名净化（basename + 字符白名单，sanitize 复活） | ui.py |
| B7 | egress/SandboxLimits 惰性读 env（CLI/YAML 配置不再静默失效） | egress.py、sandbox.py |
| B8 | 并行 loop 上限 16；沙箱日志 2000 行 / 桥 op 64 并发 / 响应 header 200；httpx 流式限读（64KB/256KB）；outbox 断连清理 + 任务取消；max_concurrency≥1 | engine.py、sandbox.py、http_exec.py、client.py |
| B9 | 表达式长度/深度/乘法结果/容器字面量上限 + RecursionError/TypeError 统一包装 | expr.py |
| M20 | bridge 坏帧防御（id 解析不再炸读线程） | testpilot_sdk/bridge.py |
| M19 | 压测指标行字段缺失防御 | stress.py |

## 配套

- scripts/dev.sh：生成/复用 TP_WORKER_TOKEN 与 TP_JWT_SECRET（dev 默认也强密钥）
- deploy/.env.example + docker-compose.prod.yml：scheduler/worker/worker-stress 注入 TP_WORKER_TOKEN（必填）
- copilot/：SchedulerClient AuthInterceptor 注入当前用户 JWT（chat 入口 contextvar 设置）
- 测试修正：applyOverrides 按 key 排序确定性（原 map 随机序 flaky）；test_config 环境污染清理（autouse fixture）
- 遗留（需部署层配合，已提供配置开关，默认不启用不影响本地开发）：
  - 沙箱容器化隔离（gVisor 等）：本地无容器运行时可用 TP_SANDBOX_REQUIRE_ISOLATION=1 开启 fail-closed（无隔离工具时沙箱直接失败而非裸奔）；容器后端就绪后配合使用
  - /metrics 来源限制：TP_METRICS_ALLOWED_CIDRS（逗号分隔 CIDR；空=不限制，生产配 Prometheus 网段后其余来源 403）——已实现并带单测
  - gRPC mTLS 可选增强

---

# 七、P1 修复记录（能修的已全部修复）

> 24 项 P1 中：M3/M5/M19/M20(坏帧)/M24 已在 P0 轮顺带完成；本轮补齐其余各项。
> 验证：Go 16 包全绿；Worker pytest 149 全绿。

## Go 侧

| 编号 | 修复内容 | 位置 |
|---|---|---|
| M6 | listWorkers 租户隔离（viewer 只见本租户+共享 Worker；admin 全量） | httpserver/handlers.go |
| M7 | updateMemberRole 禁止修改自己的角色（防 admin 自我提权） | httpserver/member_handlers.go |
| M8 | OIDC redirect 加固：拒绝 userinfo URL、host:port 精确匹配、回跳响应 no-store | httpserver/oidc_handlers.go |
| M9 | addMember 仅 RecordNotFound 才走创建分支；用户+成员创建同事务 | httpserver/member_handlers.go |
| M10 | retention 只清理终态 run（RUNNING 不再被连根删除）+ ORDER BY started_at | retention/retention.go |
| M11 | step 结果引用的 case result 缺失时事务失败（不再静默落 tenant_id=0） | dispatch/dispatch.go |
| M12 | notify webhook SSRF 防护（仅 http/https + 默认拒绝私网/环回，TP_NOTIFY_ALLOW_PRIVATE=1 放开内网 webhook）+ 读尽 Body 复用连接 | notify/notify.go |
| M13 | login 检查账号 Status（禁用不可登录）+ 来源 IP 固定窗口限流（10 次/分钟） | httpserver/handlers.go、ratelimit.go（新） |
| M14 | appendCopilotMessage 用户消息扣 ai_calls 配额（与 gRPC 工具面一致） | httpserver/copilot_handlers.go |
| M15 | register 审计 Detail 改 json.Marshal（消除 JSON 拼接注入/非法 JSON） | httpserver/handlers.go |
| M16 | 18 处 500 err.Error() 改 writeInternalErr（细节进日志，客户端通用文案） | 各 handler（writeInternalErr helper 在 json.go） |
| M17 | getRun steps 批量 IN 查询（消除 N+1）；getStressRun 指标点上限 3000（倒序取最近再转正序） | httpserver/handlers.go、stress_handlers.go |

## Python 侧

| 编号 | 修复内容 | 位置 |
|---|---|---|
| M1 | 任务自创建即注册（排队期可取消）；结果投递 shield 保护（取消不丢回执） | testpilot_worker/client.py |
| M2 | grpc 描述符缓存键含 target（防同名 service 跨目标串类）；无 deadline 强制 30s；engine 传任务级超时 | testpilot_worker/grpc_exec.py、engine.py |
| M4 | 并行 loop 各迭代产物目录隔离（case_rel 加 -iN 后缀，防截图/trace 互相覆盖） | testpilot_worker/engine.py |
| M18 | 行为压测 gate 等待 10s 超时兜底（防崩溃沙箱致 inflight 泄漏、并发坍缩）；沙箱超时取 min(duration+60, task.timeout) | testpilot_worker/stress.py |
| M20 | bridge call 加 120s 超时 + pending 清理（防沙箱协程永久挂起） | testpilot_sdk/bridge.py |
| M21 | 沙箱与 Locust 子进程 start_new_session + SIGKILL 进程组（防子进程孤儿化） | testpilot_worker/sandbox.py、stress.py |
| M23 | Worker SIGTERM/SIGINT 优雅停机：停止取任务、取消在途任务、关闭连接 | testpilot_worker/main.py、client.py |
