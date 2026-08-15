# TestPilot Worker 沙箱/压测/网络/SDK 代码审查报告

审查范围：worker/src/testpilot_worker/{sandbox,stress,stress_runner,egress,tracing,engine,ui,http_exec,grpc_exec,config,client,main,expr}.py、worker/src/testpilot_sdk/{bridge,entry,page,assertions,context,models}.py、worker/pyproject.toml。以下每条均有代码依据。

## 一、严重问题

### S1. 沙箱不是安全边界：与 Worker 同解释器、同用户、无文件系统隔离，环境白名单/能力桥可被直接绕过
[sandbox.py:151-166 `SubprocessBackend.run` / `_scrub_env` sandbox.py:89-100]

沙箱子进程直接用 Worker 的 venv 解释器（sys.executable）启动，PYTHONPATH 指向 worker/src（含 testpilot_worker 包），且 site-packages 完整（httpx/playwright 等全可 import）。没有 chroot、没有用户降权、没有文件系统限制。因此沙箱内的不可信代码可以：
- `import httpx` 直接发起任意网络请求（完全绕过能力桥与 egress.check_url）。网络隔离仅靠 sandbox-exec/bwrap 尽力而为（sandbox.py:111-121 自己承认），而 macOS 新版无 sandbox-exec、Linux 无 bwrap 或 userns 被禁的机器上（这是常态）网络出口完全放开；
- 在 Linux 下读 /proc/<PPID>/environ（沙箱是 Worker 直系子进程、同 uid，Yama ptrace_scope=1 下可读）→ env scrub 形同虚设，Worker 进程环境里所有凭据（含运维注入的 AWS_* 等）直接可读；
- 读写 Worker 用户可访问的任意文件（worker.yaml、产物目录、/etc/passwd 等）。

修复：换 container/gVisor 后端 + 非 root 运行 + seccomp；至少给 sandbox-exec/bwrap 加只读绑定根 + 写仅限 scratch 的文件规则。当前代码把安全边界押在 LLM 不会写恶意代码上，不成立。

### S2. Linux 下 bwrap 参数错误：空 rootfs，所有沙箱必然启动失败（devtmpfs 还是隐患）
[sandbox.py:117-118 `_net_deny_wrapper`]

`bwrap --unshare-net --dev /dev -- <cmd>`：bwrap 默认创建空 rootfs（无 --ro-bind/--bind 挂载宿主目录），`--dev /dev` 只是新建 devtmpfs。sys.executable 的绝对路径在空 rootfs 中不存在 → exec 必败，Linux 上只要装了 bubblewrap，每次沙箱运行都失败（日志只显示 exit rc≠0）。若有人修复为加 --ro-bind /usr 之类，又会顺带把 devtmpfs 的全部宿主块设备节点（/dev/sda…）暴露给沙箱。

修复：完整写法如 `bwrap --unshare-net --die-with-parent --ro-bind / / --proc /proc --dev /dev --tmpfs /tmp -- <cmd>`，dev 最小化或 ro；并在 CI 对 Linux 路径做真实冒烟测试。

### S3. 能力桥 ui_action 零校验：浏览器 SSRF、file:// 读本地文件、任意文件上传/写入
[ui.py:85-158 `UiSession.execute` / ui.py:224-241 `bridge_ui_handler` / engine.py:519]

低代码沙箱可随意调用 op=ui_action（Page 模型），Worker 侧不校验任何参数：
- `goto` 接受任意 URL（私网 IP、云 metadata 169.254.169.254、file:///etc/passwd）→ 浏览器 SSRF + 本地文件内容经截图/断言外带；
- `upload`（set_input_files，ui.py:147-149）把 value 当本机路径 → 任意文件读取后上传到测试目标站点；
- screenshot/download 文件名（ui.py:131-135、154-158）未 sanitize：`case_dir / name`，name 为绝对路径时直接拼接（已实测 `Path('/data/a') / '/etc/x' == '/etc/x'`），含 ../ 可穿越 → 在 Worker 上任意路径写文件。ui.py:210-211 有现成的 sanitize() 但从未使用。

修复：goto 前 scheme 白名单 + egress.check_url；upload/screenshot/download 路径必须 resolve 后仍在 artifact 根目录内（resolve().is_relative_to(artifact_root())），文件名过 sanitize()。

### S4. 超时强杀后 result.ok 不重置 + 沙箱可伪造 result 消息 → 崩溃/超时用例被判 PASSED
[sandbox.py:220-237 `SubprocessBackend.run`]

超时分支只置 timed_out=True 和 error，不把 result.ok 置 False；且沙箱进程 stdout 是协议通道，任何一行 {"type":"result","ok":true}（用户代码 os.write(1, ...) 即可发出，sys.stdout 重定向挡不住 fd 1）都会被 _control_loop 接受并 done.set()。于是：沙箱先伪造 ok=true 再崩溃（rc≠0）或死循环超时 → 返回 ok=True + error，引擎 _do_code_block（engine.py:308-310）只看 res.ok → 用例通过，error 被忽略。

修复：timeout 分支显式 result.ok = False；进程 rc≠0 或 done 未置时无条件 ok=False；对 result 消息做可信性约束（只接受 entry 正常收尾路径发出的、带会话随机数的帧）。

### S5. 声明式 api_call 默认跟随重定向，但只校验原始 URL → 重定向 SSRF
[http_exec.py:75-83 `build_request` / http_exec.py:98]

`follow = True`（无 settings 时默认跟随），egress.check_url 只校验初始 URL；httpx 跟随 302 到 http://127.0.0.1:6379 或 http://169.254.169.254/latest/meta-data/ 时不再做任何出口检查。能力桥路径（bridge_http_handler 不传 follow_redirects，httpx 默认 False，已核实）不受影响，两条路径行为不一致。

修复：跟随重定向时对每个 Location 递归 check_url（或默认不跟随、显式开启才跟随且逐跳校验）。

### S6. egress 检查是阻塞式 + TOCTOU：阻塞 getaddrinfo 冻结事件循环、DNS rebinding 可绕过
[egress.py:39-48 `_is_private` / sandbox.py:286 / http_exec.py:98]

socket.getaddrinfo 是阻塞调用、无超时，直接在 asyncio 任务里执行：沙箱/用例传一个 DNS 挂起的主机 → 整个 Worker 事件循环冻结（连 asyncio.wait_for 的超时定时器都不再触发）→ 永久卡死。且 check_url 解析一次、httpx 再解析一次，两次结果可不同（DNS rebinding）→ 私网检查可被绕过（第一次给公网 IP 通过，连接时解析到内网）。

修复：改用 loop.getaddrinfo/线程池 + 超时；把解析结果绑到连接上（解析出 IP→校验→直连该 IP，或走强制出口代理），消除 TOCTOU。

### S7. 用例超时/取消路径泄漏子进程，且可永久卡死协程（并发信号量泄漏）
[engine.py:147 `wait_for(_run_steps)` → sandbox.py:219-231 `run`；client.py:97-101 `_cancel`；stress.py:128-145]

- 用例级 wait_for 取消 → backend.run 的 wait_for(done.wait()) 抛 CancelledError，proc.kill()/proc.wait()/rmtree(scratch)（含源码与 payload.json）全部不执行 → 沙箱子进程变孤儿继续跑、scratch 残留；
- Scheduler 下发 cancel → 任务取消 → run_stress 的 await readers（stress.py:144 finally）阻塞在活进程的管道 readline 上永不 EOF → 协程卡死，async with self.sem 永不释放 → 并发槽位永久泄漏，且 Locust 子进程不被杀；
- 超时路径只 proc.kill() 单进程，不杀进程组/子孙 → 沙箱 fork 的子进程（可 fork，NPROC 上限 128）成孤儿继续占 CPU。

修复：run/run_stress 全程 try/finally（finally 中 killpg + wait + rmtree + unlink）；子进程 start_new_session=True 并记录 pgid，超时 killpg(-pgid, SIGKILL)；取消也走同一清理路径。

### S8. 沙箱 stdout/stderr 输出无总量上限 → Worker 内存 DoS
[sandbox.py:184-217 `_control_loop` / `_drain`]

readline 无行长上限、result.logs 无条数上限（单条截 2000 字符但条数不限）：沙箱循环 print('x'*2000) 即可让 Worker 无限积累日志（单进程内存可被推到 GB 级 OOM）。RLIMIT_FSIZE 只限制文件，不限制管道。

修复：累计字节/条数上限（如 2MB 或 1000 条），超限即 kill 沙箱。

## 二、中等问题

### M1. CLI/YAML 安全配置静默失效：egress 白名单与沙箱限额在 import 时冻结
[egress.py:19-20 模块级 _ALLOW/_BLOCK_PRIVATE；sandbox.py:43-49 SandboxLimits 字段默认值；main.py:12 vs 24]

main.py 顶部 from .client import WorkerClient 级联 import engine→http_exec→egress、sandbox，此时 _ALLOW/_BLOCK_PRIVATE 和 SandboxLimits 默认值（int(os.environ.get(...))）已按当前进程环境求值；config.apply_environ（main.py:24）在 import 之后才回写 os.environ → 通过 --egress-allow/YAML 配置的白名单、私网阻断、--sandbox-cpu 等全部被忽略（只有直接设进程环境变量才生效）。安全配置静默失效是最危险的失效模式。

修复：改为函数内惰性读取 env（每次 check 时读），或把 apply_environ 提到所有业务 import 之前/在 entry 里显式重建模块状态。

### M2. 生产默认不拦私网（egress_block_private=False）→ SSRF 面默认打开
[config.py:52 `Settings.egress_block_private` / egress.py:20]

默认配置下声明式 api_call 与能力桥 http_request 可直连 127.0.0.1/10.x/169.254.x（云 metadata）等内网地址，仅靠对测试目标自身的隐式信任。dev 便利不应成为默认安全姿态。

修复：默认 True（私网阻断），dev 场景显式关闭。

### M3. RLIMIT_NPROC 是 per-user 限制 + 无进程组强杀
[sandbox.py:83-84 `_apply_rlimits` / sandbox.py:224-227 超时 kill]

Linux/macOS 的 RLIMIT_NPROC 按真实 uid 计数：子进程里设 128 会限制整个用户（含 Worker 自身、同机其他租户/沙箱）的总进程数 → 一个沙箱 fork 满 128 即可让 Worker 再也无法创建新沙箱/子进程（fork EAGAIN）→ DoS。同时 proc.kill() 不杀子孙进程。

修复：start_new_session=True + killpg；NPROC 上限评估 per-user 影响或降为 per-process 近似（配合 seccomp 禁 fork/clone 更彻底）。

### M4. 能力桥读线程单点崩溃 → 所有 pending call 永久挂起
[bridge.py:54-73 `Bridge._read_loop` / bridge.py:80-88 `call`]

fut = self._pending.pop(int(msg.get("id", -1)), None) 在 try 之外：Worker 侧任一条 id 非数字的响应（或用户代码抢读 stdin 造成的残帧）→ int() 抛 ValueError → 读线程死亡 → 全部未决 future 永不 resolve → 沙箱静默死锁到超时。且 call() 无超时保护。

修复：id 解析容错（try/except + continue）；call 增加超时并清理 pending。

### M5. 用户代码可抢读 stdin / 伪造 stdout → 协议帧竞争与损坏
[entry.py:39 `sys.stdout = sys.stderr` / bridge.py:42-48]

sys.stdout 重定向只挡 print，挡不住 os.write(1,...)（伪造任意桥消息/result，见 S4）和 input()/read(0)（抢走桥响应帧 → SDK 读到残帧 → 该 call 永久挂起、协议无法恢复）。协议对同进程不可信代码没有完整性手段。

修复：桥改走私有 socketpair 或加帧序号/会话随机数；文档明确沙箱代码不得直接读写 fd 0/1。

### M6. 行为压测：迭代崩溃致 inflight 泄漏 → 并发坍缩为 0 但仍 PASSED；task.timeout 被忽略
[stress.py:214-230 `gate`/`on_iteration` / stress.py:293-309 / stress.py:294]

gate 在迭代前 inflight += 1，靠 iteration event 减回；沙箱在 gate 与 event 之间崩溃 → inflight 永久泄漏 → 其余沙箱全部阻塞在 gate（限流失效，并发坍缩），但结果仍 RUN_STATUS_PASSED（判定只认 timed_out/BaseException）。另外 _run_behavior 只用 timeout_s=duration+60，完全不执行 task.timeout（Locust 路径有执行，见 stress.py:85-86），行为压测可突破任务级超时约束。

修复：event 超时兜底（gate 授予后 N 秒无 event 自动回收）；task.timeout 参与封顶。

### M7. stress 指标行字段缺失 → KeyError 炸掉整个 run_stress（无 TaskResult）
[stress.py:107-116 `_read_metrics`]

pt.rps = msg["rps"] 等直接索引，runner 输出缺字段/类型错 → KeyError/TypeError → gather 崩溃 → run_stress 抛异常（任务被 client.py 兜底为 FAILED，但 runner 进程可能遗留、spec 临时文件不清理）。

修复：.get() + 类型容错 + 行长度上限。

### M8. preexec_fn 在多线程进程中使用（asyncio + grpc aio）
[sandbox.py:166 `preexec_fn=_preexec`]

Python 文档明确 preexec_fn 在线程化进程中不安全（fork 死锁风险）；Worker 进程含 grpc aio 线程。当前恰好单线程事件循环才没炸，属定时炸弹。

修复：改 start_new_session + 在子进程入口（entry.py 内）应用 rlimits，或容器化后移除。

### M9. code_block 后端与低代码后端能力不一致
[engine.py:85-90 `backend` 属性]

code_block 的 SubprocessBackend 未传 auto_headers（声明式 api_call 自动注入环境 HEADER 变量，engine.py:262-264），也未注册 ui_action（低代码路径 engine.py:519 有）→ 同一用例里 code_block 内 http 缺环境头、Page 模型直接 BridgeError。

修复：统一复用 _run_lowcode 的后端构造（传 auto_headers + ui_action handler）。

### M10. grpc_exec：描述符缓存不含 target；无默认 deadline
[grpc_exec.py:29 `_resolved` / grpc_exec.py:90-127 `call`]

_resolved[(service, method)] 全局缓存：两个不同目标服务器提供同名 service 但 proto 不同 → 第二个目标复用第一个的 message 类，序列化错乱（难排查）。且 call() 在 api 未设 deadline 且 timeout_s=None 时无超时 → unary 永久阻塞在线程池（engine 取消无法中断 to_thread）→ 线程泄漏。

修复：缓存 key 加 target；默认 deadline（如 30s）。

## 三、轻微问题

- [sandbox.py:203] 每个 op 消息 create_task 无任务跟踪/上限，沙箱刷 op 可堆积任务；[sandbox.py:244-247] int(msg.get("id",0)) 在 try 外，非数字 id 使 op 任务崩溃、该 call 挂起至超时。→ 放进 try 内 + 任务集合回收。
- [stress_runner.py:61-83, 132-137] controller 绿子程异常被 gevent 吞掉 → ctrl.join() 正常返回、done.ok=True 但 total=0，压测成功发出 0 请求。→ 捕获 controller 异常写入 done.ok=False。
- [sandbox.py:204-207] _loop_cb 在控制协程内 await，回调异常会杀死 ctrl 任务 → 该沙箱后续 op 无人处理。→ try/except 包裹。
- [main.py:27 + tracing.py:76-81] log format 硬编码 %(trace_id)s，TraceLogFilter 只挂到 attach 时已存在的 root handlers，后续库新增 handler 的日志会缺属性报格式错误。
- [client.py:82-87] 断线重连后 outbox 里滞留的旧事件（含旧任务结果）会在新会话注册后继续发送 → 可能重复投递。
- [sandbox.py:310-320] 响应 dict(resp.headers) 无上限；256KB 截断后 json.loads 基本必失败 → body 变成截断文本，断言易被误导。
- [ui.py:175-203] finish() 用 except Exception: pass 静默吞掉 trace/har 导出失败（无日志，产物丢失不可见）。
- [assertions.py:26-33] _check 把完整 actual 值（可含超大 body/页面文本）写进 _records 并经 result 帧回传，无截断 → 配合 S8 放大内存问题。

## 核心风险总结（5 条）

1. **沙箱本质上是同进程不可信代码**：与 Worker 同解释器同用户，无文件系统/网络隔离，env scrub 可被 /proc 父进程 environ 绕过，可直接 import httpx 出网、可伪造 result 谎报通过（S1、S4）——建议按沙箱必破设计，把安全边界收敛到 op handler 校验与容器隔离。
2. **SSRF 面默认过大且多处可绕**：egress 私网阻断默认关闭（M2）、声明式路径跟随重定向不校验（S5）、浏览器桥 goto/upload/文件名零校验可打内网、读文件、写文件（S3）。
3. **进程生命周期管理缺陷**：超时/取消泄漏沙箱与 Locust 子进程、孤儿进程、管道卡死协程并泄漏并发信号量（S7、M3）。
4. **能力桥协议无完整性/超时/容错**：伪造帧、读线程单点崩溃死锁、用户代码抢读 stdin、call 无超时（S4、M4、M5）。
5. **配置与运行时脱节**：egress 白名单/私网阻断/沙箱限额在 import 时冻结，CLI/YAML 安全配置静默失效（M1、M2）——安全策略看似配置了、实际没生效。
