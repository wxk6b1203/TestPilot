# 执行隔离与出网管控强化设计

> 📚 文档导航：[设计](design.md) · [数据模型](data-model.md) · [部署](deployment.md) · [UI 探测](ui-probe-design.md)
>
> 状态：设计稿（未实施）。来源：2026-09 全项目代码审查确认的三个"设计级遗留"，
> 均有明确代码缺口，但根治超出单次修复的体量，需要设计评审后分期实施：
>
> | # | 缺口 | 审查出处 | 根治手段 |
> |---|------|----------|----------|
> | A | 浏览器出网无 DNS pinning：goto 重定向中间跳已真实发出、浏览器自解析 DNS 可被 rebinding 利用 | REVIEW_SANDBOX_STACK S3/S6、ui.py goto 注释 | 本地 CONNECT 转发代理（feature A） |
> | B | 低代码沙箱不是安全边界：与 Worker 同 uid/同解释器/完整文件系统，env scrub 可经 /proc 旁路 | REVIEW_SANDBOX_STACK S1、design.md 6.3 升级路径 | 容器化沙箱后端（feature B） |
> | C | Worker 镜像以 root 运行（Chromium 沙箱依赖 userns，docker 默认 seccomp 不可用） | 本轮 P2 遗留（worker.Dockerfile 注释） | 非 root + 分阶段补偿（feature C） |
>
> 三者相互独立、可分别实施，但组合后构成完整的执行安全模型：
> **B 解决"用户代码跑在哪"，C 解决"Worker 自己以什么身份跑"，A 解决"两条出网通道（httpx/浏览器）同一策略同一解析"**。

## 目录

1. [背景与威胁模型](#1-背景与威胁模型)
2. [Feature A：浏览器出网转发代理（DNS pinning）](#2-feature-a浏览器出网转发代理dns-pinning)
3. [Feature B：容器化沙箱后端](#3-feature-b容器化沙箱后端)
4. [Feature C：Worker 镜像降权](#4-feature-cworker-镜像降权)
5. [配置面汇总](#5-配置面汇总)
6. [实施计划与验收](#6-实施计划与验收)
7. [风险与回滚](#7-风险与回滚)

---

## 1. 背景与威胁模型

### 1.1 现状（本轮修复后的基线）

Worker 有两条租户可控的出网通道与一个代码执行面：

```
┌─ Worker 容器（当前 root）────────────────────────────────────┐
│  声明式引擎 http_exec ──httpx──► 目标服务                        │
│      │                          ▲ EgressPinnedBackend：        │
│      │ egress.acheck_url        │ 解析→策略校验→绑定同一 IP 建连  │
│      ▼                          │ （无 rebinding 窗口）           │
│  能力桥 ◄──stdin/stdout──┐      │                               │
│  低代码沙箱进程 ──────────┘      │ net_deny 尽力而为              │
│  （subprocess：同 uid/同解释器， │ （sandbox-exec/bwrap，          │
│    无文件系统/凭据隔离 = S1）     │  无工具时完全放开 = S1）         │
│  Playwright Chromium ──► 目标页面 │ goto 前后各查一次 URL（事后复核，│
│                                 │  中间跳已发出；DNS 自解析 = S3/S6）│
└──────────────────────────────────────────────────────────────┘
```

- `http_exec` 通道已闭环：`EgressPinnedBackend` 在连接层解析并绑定校验通过的 IP，
  `check_url` 与建连共用同一次解析，rebinding TOCTOU 已消除。
- 浏览器通道只有"goto 前 + 最终 URL"两次 URL 字符串校验，浏览器自行解析 DNS、
  自行跟随重定向——策略检查与真实连接之间没有绑定关系。
- 低代码沙箱靠 `sandbox-exec`/`bwrap` 做网络否定的"尽力而为"，两工具都缺失时
  （macOS 新版本移除 sandbox-exec、Linux 无 bwrap 或 userns 被禁——云主机常态）
  沙箱与 Worker 同 uid：可读 `/proc/<ppid>/environ`（env scrub 旁路）、可写
  Worker 用户可及的任意文件、可用自己的 httpx 绕过能力桥直连内网。

### 1.2 威胁模型（谁在对抗什么）

| 对抗者 | 能力 | 主要攻击面 | 本设计对策 |
|--------|------|-----------|-----------|
| 恶意/被投毒的租户测试用例（声明式 + 低代码脚本） | 任意 Python 逻辑、任意 URL/参数 | 沙箱逃逸读凭据、绕过 egress 打内网（SSRF→metadata/私网） | B（容器边界）+ A（统一出口策略） |
| 被测系统的恶意页面（UI_ACTION 访问的目标） | 页面脚本、重定向、可控子资源 URL | 引导浏览器触达私网/metadata、DNS rebinding | A（每连接强制校验） |
| 被攻破的 Worker 容器内进程 | 容器内 root 权限 | 逃逸到宿主、横向移动 | C（非 root + cap 收敛）+ B（gVisor 时内核级边界） |

明确不在对抗范围内（与 design.md 6.3 一致）：宿主 root 被攻破、内核 0day（B 的
gVisor 可显著抬高成本但不承诺）、租户间的资源公平性（由配额体系承担）。

### 1.3 设计原则

1. **策略单点**：egress 策略（白名单/私网阻断/DNS 映射）只在 `egress.py` 一处定义，
   httpx 后端、桥 HTTP、浏览器代理全部消费同一实现，禁止第三份复制。
2. **校验即连接**：凡是"先检查后由别人连接"的模式都视为有缺口；检查者必须同时
   是连接者（httpx 已做到，A 让浏览器也做到，B 让沙箱完全没有自己的连接能力）。
3. **fail-closed 可配置**：所有新边界默认"尽力而为"保开发体验，但每个边界都有
   `*_REQUIRE`/`*_ENFORCE` 开关，生产模板默认开；开关关闭时启动日志必须打印
   当前隔离等级（防止"以为开了实际没开"——B7 的教训）。
4. **复用现有抽象**：沙箱走既有 `ExecutionBackend` 插件点（design.md 15.1 已预留）；
   浏览器代理不引入新依赖（asyncio 标准库实现，worker 已有同风格管线代码）。

---

## 2. Feature A：浏览器出网转发代理（DNS pinning）

### 2.1 为什么选本地转发代理，而不是别的

| 备选 | 结论 |
|------|------|
| `--host-resolver-rules=MAP host ip` 启动参数 | 只能在 launch 时静态映射，goto 时才知道目标；对重定向的每一跳无法动态 pin。**否**（可作为测试后门的补充，见 2.6） |
| MITM 代理（解密 TLS 后逐请求校验） | 需要 CA 证书装进浏览器上下文，证书私钥管理成为新风险面；性能开销大。**否** |
| **HTTP CONNECT 转发代理（不解密）** | Chromium 对 http(s)/ws(s)/wss 一律经代理发 CONNECT（或绝对形式 GET），代理在 CONNECT 时解析→校验→连接"校验通过的那个 IP"，TLS 端到端由浏览器与目标完成（无需 CA）；每个重定向跳都会发起新连接 = 天然逐跳校验。**采纳** |

关键观察：**不需要解密 TLS 就能 pin DNS**。CONNECT 隧道里代理只知道 `host:port`，
但"解析这个 host 并把隧道接到哪个 IP"由代理决定——检查者与连接者合一，S3/S6
的两个缺口（中间跳已发出、两次解析不一致）同时消除。

### 2.2 架构

```
UiSession.ensure()                     BrowserEgressProxy（Worker 进程内）
   │ new_context(proxy={                     ┌────────────────────────┐
   │   server: http://127.0.0.1:<port>})    │ asyncio start_server    │
   ├───────────────────────────────────────►│ 127.0.0.1:动态端口        │
   │                                        │                        │
   │  Chromium（每连接）                      │ CONNECT api.example.com │
   ├───────────────────────────────────────►│  1. host 白名单校验       │
   │                                        │  2. resolve（3s 超时）    │
   │ ◄──────200 Connection Established──────│  3. 私网阻断校验          │
   │ ═══════ TLS 端到端（SNI=原 host）═════►│  4. connect(允许的 IP)    │
   └──────────── 双向泵直到任一端关闭 ────────┤    （同一解析结果）        │
                                            └────────────────────────┘
```

- 一个 Worker 一个共享代理实例（策略是 Worker 级的，连接相互独立），
  `UiSession`/probe 会话按需引用，懒启动、Worker 退出时关闭。
- 明文 HTTP：Chromium 经代理发**绝对形式**请求（`GET http://host/path`），
  代理同样做解析→校验→对允许 IP 建连，`Host` 头保持原值（不破坏 vhost 路由）。
- WebSocket：`ws://`/`wss://` 的握手就是 HTTP 升级请求，走同一代理路径，覆盖。

### 2.3 组件与落点

新增 `worker/src/testpilot_worker/ui_proxy.py`（约 300 行，仅标准库 asyncio）：

```python
class BrowserEgressProxy:
    """Chromium 出网转发代理：每连接执行 egress 策略并对允许 IP 建连。

    - 不解密 TLS：CONNECT 隧道端到端，SNI/证书校验由浏览器与目标完成
    - 解析→校验→连接使用同一次 DNS 结果（复用 egress.resolve_host_for_connect，
      与 httpx 的 EgressPinnedBackend 消费同一实现——策略单点原则）
    - 拒绝时回 403（明文）/ 关闭隧道（CONNECT），页面表现为导航失败，
      步骤结果正常失败并带 egress 拒绝原因（经由 goto 的异常/最终 URL 复核）
    """
    async def start(self) -> str   # 返回 "http://127.0.0.1:<port>"，幂等
    async def stop(self) -> None
    stats: dict                    # allowed/denied 计数（并入 /metrics 之前先进日志）
```

改动点：

| 文件 | 改动 |
|------|------|
| `ui.py` `UiSession.ensure()` | 策略启用时：`ctx_kwargs["proxy"] = {"server": await proxy.start()}`；上下文关闭时代理引用计数回收（共享实例不随会话关停） |
| `ui.py` `execute(GOTO)` | 保留现有 goto 前字符串校验（快速失败、错误信息友好），把"已知缺口"注释改为指向本代理；代理启用后最终 URL 复核降级为断言日志 |
| `probes.py` 探测会话 | `ProbeUiSession` 同样接代理（探测访问的页面同属不可信输入） |
| `config.py` | 见 2.5 |
| `main.py` | lifespan 启停共享代理；`/healthz` 附带代理状态 |

### 2.4 协议细节与边界情况

- **QUIC/HTTP3**：Chromium 配置代理后不协商 QUIC（代理仅支持 TCP 通道），无需额外处理；
  保险起见 launch 参数加 `--disable-quic`。
- **Playwright per-context proxy 前置**：Chromium 的 context 级 proxy 覆盖要求
  browser launch 时也带一个（任意占位的）全局代理参数——官方语义是"所有 context
  都覆盖时全局代理永远不会被真正使用"。实现上 launch 固定
  `--proxy-server=http://127.0.0.1:1`（占位端口）+ context 指向真实本地代理；
  C1 验收时断言：未配置代理策略的 dev 环境不附加任何 proxy 参数（行为不变）。
- **WebRTC 泄漏**：ICE 候选可绕过 HTTP 代理直连。目标：禁掉 WebRTC 或强制其走代理。
  实施时按序验证：launch `--disable-webrtc`（生效性以 C1 验收清单确认，Chromium
  版本间有差异）→ 备选 CDP 限制 ICE policy → 兜底靠 B 的容器网络层（UDP 出口
  随 `--network none`/worker 容器网络策略一并否决）。UI 引擎当前没有 WebRTC
  相关 action，禁用不影响功能。
- **非 http(s) scheme**：goto 前校验已拒绝 `file://`/`ftp://` 等（现状保留）；
  代理侧对 CONNECT 目标端口做最小约束（拒绝 22/25 等？不限制——白名单已足够，
  保持与 httpx 通道一致不额外加规则）。
- **DNS 映射（测试后门 + 企业 pinning）**：`TP_EGRESS_DNS_MAP=host:ip,host:ip`，
  代理与 httpx 后端共用的解析函数优先查静态映射（不走系统解析）。用途：
  离线 e2e（把 `api.example.com` 映到本机 echo）、给内网固定 IP 的服务显式 pin。
  落点：`egress.resolve_host_for_connect` 头部插入映射查询（一处改动三通道受益）。
- **IPv6**：`getaddrinfo` 返回的 AAAA 同样过 `_private_ip` 判定（现实现已覆盖）。
- **性能**：每连接多一次本地 loopback 跳 + 一次 Worker 内解析（3s 超时上限）；
  UI 步骤本身是百 ms 级，可忽略。并发连接受 asyncio 单循环调度，UI 会话
  cap（max_sessions=2）与用例并行上限（16）远低于瓶颈。

### 2.5 配置与开关

| 环境变量 | 默认 | 语义 |
|----------|------|------|
| `TP_UI_EGRESS_PROXY` | `auto` | `auto`=当 `TP_EGRESS_ALLOW` 非空或 `TP_EGRESS_BLOCK_PRIVATE=1` 时启用；`1`/`0` 强制开/关。dev（本机 echo 被私网阻断策略排除时）可显式关 |
| `TP_EGRESS_DNS_MAP` | 空 | 静态 host→IP 映射，三通道（httpx/桥/浏览器）共用 |

启动时若策略启用而代理启动失败（端口耗尽等）→ **fail-closed**：UI 步骤全部报
`UiUnavailable(egress proxy failed)`，不允许静默退回无代理模式。

### 2.6 测试

- 单元（`tests/test_ui_proxy.py`）：伪造浏览器流量直接对代理发 CONNECT/绝对 GET——
  白名单命中/拒绝、私网拒绝、DNS 映射、CONNECT 后双向泵、明文重写、半包粘包。
- 集成：httpx 走同一 `resolve_host_for_connect` 的既有测试回归 + `TP_EGRESS_DNS_MAP`
  把测试域名钉到本地 echo 服务器，端到端跑 `page.goto` 命中/拒绝两分支（CI 无外网依赖）。
- 回归：现有 `test_ui.py`、`test_egress.py` 全绿；`TP_UI_EGRESS_PROXY=0` 时行为与
  当前完全一致。

---

## 3. Feature B：容器化沙箱后端

### 3.1 目标与非目标

- **目标**：低代码/CODE_BLOCK 的不可信 Python 在独立容器内执行——独立 uid、
  独立 mount namespace（只见 scratch）、无网络（`--network none`）、资源硬限、
  seccomp 默认；无容器运行时时按 `require_isolation` fail-closed。
- **非目标**：不做跨租户混部专用集群（独占 Worker 已覆盖，见 design.md 6.3）；
  不引入 Firecracker/Kata（运维成本与收益不匹配，gVisor 足够）；
  不改变能力桥协议（stdin/stdout 语义保持，容器只是把进程包了一层）。

### 3.2 后端选择与探测顺序

```
TP_SANDBOX_BACKEND = auto | subprocess | container
                        │
        auto: container 可用？ ──否──► subprocess（现状 + require_isolation 判定）
                        │是
        优先 runsc（gVisor）► 其次 docker/podman(runc)
```

- **gVisor 优先**：用户态内核，逃逸面比共享内核小一个量级；Chromium/UI 不在
  沙箱内跑，不受 syscall 兼容性影响（低代码脚本只有纯 Python + 能力桥）。
- 探测命令：`runsc --version` / `docker version --format {{.ServerVersion}}` /
  `podman --version`，结果缓存 + 启动日志打印实际选中的后端与版本（原则 3）。
- 容器内**不需要**再跑 bwrap/sandbox-exec（网络已由 `--network none` 根治），
  `_net_deny_wrapper` 在 container 后端下跳过。

### 3.3 容器运行规格

```bash
<runtime> run \
  --network none \                 # 出口唯一通道是能力桥 stdio（原则 2）
  --read-only \                    # 根文件系统只读；可写仅 scratch 挂载点
  --tmpfs /tmp:size=64m \
  --memory <limits.mem_mb>m --cpus <limits.cpu_seconds 的等效核数> \
  --pids-limit <limits.max_procs> \
  --cap-drop ALL --security-opt no-new-privileges \
  -v <scratch>:/work:rw \          # user_case.py / payload.json / 产物
  -e TP_PAYLOAD=/work/payload.json -e TP_SANDBOX_LIMITS='...' \
     -e HOME=/work -e TMPDIR=/work -e LANG=... -e PYTHONPATH=/app/src \
  -- <worker 镜像> python -m testpilot_sdk.entry /work/user_case.py <entry>
```

要点：

- **镜像复用 worker 镜像**（`deploy/worker.Dockerfile`）：解释器、testpilot_sdk、
  依赖完全一致，无第二套镜像维护；gVisor 模式无需内核模块。
- **stdio 即桥**：`SubprocessBackend` 的桥管线是 `stdin/stdout` 管道，容器后端用
  runtime 的 attach stdio（`docker run -i` / runsc 等价）对接同一套 `_Bridge` 代码，
  协议零改动。stderr 照旧作为日志通道（`docker logs`/attach 并读）。
- **stdin/stdout 生命周期**：容器进程退出码、管道 EOF、超时 `kill`（`docker kill`
  / runsc kill）全部走现有 `run()` 的 finally 清理骨架，仅把 `proc` 抽象成
  `SandboxProcess` 协议（`wait()/kill()/stdin/stdout/stderr`），两个后端各自实现。
- **产物**：脚本只写 scratch；产物归集仍由 Worker 从 scratch 读取上传（现状）。
- **行为压测循环模式**（stress.py `_run_behavior`）每迭代 spawn 沙箱：容器冷启动
  runc 约 100–300ms、runsc 首启较慢。首版接受（压测吞吐瓶颈在桥 RTT 的场景
  参见审查 S-D）；后续可加**每 Worker 预热池**（N 个已启动等 payload 的容器，
  复用要小心状态残留——仅无状态迭代可复用，用 payload 序号校验）。

### 3.4 部署形态（与部署层的接口）

| 形态 | 做法 | 适用 |
|------|------|------|
| compose（单机生产，本期默认） | worker 容器内不嵌 docker daemon；增加可选 sidecar `dockerhost`（挂 `docker.sock`）并让 worker 经 `DOCKER_HOST` 使用 | 快速落地；**接受 docker.sock = 宿主 root 等价**的信任前提（worker 自身已被 feature C 降权 + cap 收敛，攻击链要求先逃逸 worker 容器） |
| 独立 Worker 主机（推荐的生产终态） | 裸机/VM 装 runsc，worker 进程直接调 `runsc`（非 docker），scratch 走本地目录 | 最强边界、无 sock 暴露 |
| k8s（远期） | 沙箱 Pod 用 RuntimeClass=gVisor；本设计的 CLI 参数与 RuntimeClass handler 一一对应 | design 13.4 之后的阶段 |

`TP_SANDBOX_BACKEND=container` 但探测失败 + `require_isolation=1` → Worker 启动
Fatal（明确报缺哪个组件）；`require_isolation=0` → 降级 subprocess 并 Warn。

### 3.5 改动落点

| 文件 | 改动 |
|------|------|
| `sandbox.py` | 新增 `ContainerBackend(ExecutionBackend)`（复用 `run()` 清理骨架，抽出 `SandboxProcess` 协议）；`make_backend()` 工厂按 `TP_SANDBOX_BACKEND` 选择 |
| `main.py` / `engine.py` / `stress.py` / `probes.py` | 构造 `SubprocessBackend` 的位置改走工厂（约 4 处调用点，签名不变） |
| `config.py` | `TP_SANDBOX_BACKEND`、`TP_SANDBOX_CONTAINER_RUNTIME`（auto/runsc/docker/podman） |
| `deploy/worker.Dockerfile` | 可选 target 安装 docker CLI（仅作 client）；文档给 runsc 主机安装步骤 |
| `docs/deployment.md` | "沙箱隔离等级"一节：三级矩阵（subprocess 尽力而为 / container-runc / container-runsc）+ 宿主要求 |

### 3.6 测试

- 单元：`SandboxProcess` 协议的假实现跑通桥协议全套既有测试（`test_bridge.py`、
  `test_sandbox_isolation.py` 参数化到两个后端）。
- 集成（CI 带 docker 的 runner；本地 dev.sh 检测）：真容器跑样例脚本——网络否定
  断言（容器内 `import socket; socket.socket()` 连接必须失败）、scratch 只读边界
  （写 `/usr` 失败）、env 断言（无 TP_WORKER_TOKEN 等泄漏）、超时 kill、payload
  越权路径（`/etc/passwd` 不可读）。
- 性能基线：冷启动开销记录到 `docs/deployment.md`（runc 目标 <300ms p95）。

---

## 4. Feature C：Worker 镜像降权

### 4.1 约束：Chromium 沙箱与 user namespaces

非 root 跑 Chromium 的两难：

- Chromium 渲染进程沙箱依赖 `CLONE_NEWUSER`（user namespaces）。docker 默认
  seccomp profile 拦 `unshare`（除非 `CAP_SYS_ADMIN`）→ 非 root 容器内 Chromium
  必须加 `--no-sandbox`，渲染层隔离降级。
- 保持 root 则镜像内 uid 0，逃逸后即宿主 root 等价（本轮 P2 遗留的核心顾虑）。

**决策：分两阶段，方向是"外层边界换内层沙箱"。**

### 4.2 阶段一：非 root + `--no-sandbox` + 补偿控制（本期）

```
cap_drop ALL + no-new-privileges + 只读根 fs + 非 root uid
+ Chromium --no-sandbox（渲染层弱化，接受）
+ 补偿：A 的 egress 代理（浏览器出网被策略锁死）
       + seccomp docker 默认 profile
       + B 落地后可选 runsc 包 worker 容器本身（外层内核边界补回渲染层损失）
```

具体改动：

| 项 | 内容 |
|----|------|
| `worker.Dockerfile` | 构建 Phase 保持 root（`playwright install --with-deps` 装系统库）；`RUN useradd -m tp`；`PLAYWRIGHT_BROWSERS_PATH=/home/tp/.cache/ms-playwright`（构建期 chown tp）；`USER tp` |
| `entry.py` / `ui.py` | chromium launch args 增加 `--no-sandbox`，**仅当** `os.getuid() != 0`（或 `TP_CHROMIUM_NO_SANDBOX=1` 显式）时附加——root 场景不加，保持现状语义；注释引用本节 |
| `docker-compose.prod.yml` worker | `user: "1000:1000"`、`read_only: true`、`tmpfs: /tmp`、可写卷仅 `/home/tp`（chromium 配置/XDG 缓存）与 `/data/artifacts`、`cap_drop: [ALL]`、`security_opt: [no-new-privileges:true]` |
| 沙箱连带收益 | Worker 非 root 后，现有 subprocess 沙箱（B 未落地时）也自动非 root——`/proc/<ppid>/environ` 旁路读到的至多是 tp 用户的干净 env（配合既有 env scrub），S1 的"读 Worker 凭据"面显著收窄 |
| 文件权限 | `/data/artifacts` named volume 首挂继承镜像内属主（同 scheduler 的做法）；产物上传经 gRPC 流不受影响 |

验证清单（compose 实测）：

1. `id` = tp(1000)；`/home/tp`、`/data/artifacts`、`/tmp` 可写，其余只读。
2. 功能/低代码/UI 三类样例任务全过；HAR/trace/截图产物正常落盘上传。
3. `chromium --no-sandbox` 生效路径断言（launch args 单测 + e2e goto 本地页）。
4. `capsh --print` 显示无任何 cap；`no-new-privileges` 生效（`su` 失败）。
5. 沙箱内读 `/proc/1/environ`：无任何 TOKEN/KEY 类值（env scrub 回归）。

### 4.3 阶段二（可选增强，按需排期）

- **runsc 包 worker 自身**：宿主装 gVisor，compose/主机脚本把 worker 容器 runtime
  换 runsc——`--no-sandbox` 的渲染层损失被外层用户态内核补回。代价：syscall
  兼容性回归测试（Playwright + Chromium 在 gVisor 下官方支持，仍需全量 UI e2e）。
- **userns 路线**（替代 runsc）：自定义 seccomp profile 放行 `unshare(CLONE_NEWUSER)`
  → Chromium 保留真实渲染沙箱。适合能管控宿主 docker 参数的独立 Worker 主机
  （与 B 的"独立 Worker 主机"形态合并实施）。

### 4.4 明确不做

- rootless docker/podman 跑 worker 本体：与 B 的沙箱容器嵌套（rootless 套 rootless）
  组合复杂度不成比例；独立主机形态下直接裸进程 + systemd 更简单。
- Firecracker/Kata：见 design.md 6.3 决策——独占 Worker 已覆盖最强隔离需求。

---

## 5. 配置面汇总

新增配置（worker，YAML 键 ↔ env 自动映射，规则同现有 `TP_*`）：

| env | 默认 | 说明 |
|-----|------|------|
| `TP_UI_EGRESS_PROXY` | `auto` | 浏览器转发代理：auto=策略启用即启用；1/0 强制 |
| `TP_EGRESS_DNS_MAP` | 空 | `host:ip,...` 静态解析映射（三通道共用） |
| `TP_SANDBOX_BACKEND` | `auto` | `auto`（容器可用即用）/ `subprocess` / `container` |
| `TP_SANDBOX_CONTAINER_RUNTIME` | `auto` | `auto` / `runsc` / `docker` / `podman` |
| `TP_CHROMIUM_NO_SANDBOX` | `auto` | 非 root 自动附加；显式 1/0 覆盖 |

启动时打印一行隔离等级摘要（强制项）：

```
isolation: sandbox=container(runsc 1.8) ui_egress_proxy=on(policy) chromium_no_sandbox=on(non-root) worker_uid=1000
```

## 6. 实施计划与验收

| 阶段 | 内容 | 依赖 | 工作量 | 验收门 |
|------|------|------|--------|--------|
| A1 | `egress.resolve_host_for_connect` 增加 DNS 映射 + 单测 | 无 | 0.5d | `test_egress.py` 新增映射用例 |
| A2 | `ui_proxy.py` 代理 + UiSession/probe 接线 + 开关 | A1 | 2–3d | 代理单元/集成全绿；`TP_UI_EGRESS_PROXY=0` 行为不变 |
| C1 | worker 非 root + `--no-sandbox` 条件附加 + compose 收敛 | 无（与 A 无依赖，建议同期） | 1–2d | 4.2 的 5 项验证清单 |
| B1 | `SandboxProcess` 协议抽取 + `ContainerBackend`（runc） | 无 | 3–4d | 桥协议测试参数化双后端全绿；容器内网络否定断言 |
| B2 | runsc 探测优先 + 部署文档 + compose sidecar 形态 | B1 | 1–2d | `deployment.md` 三级矩阵；runsc 冒烟 |
| C2 | runsc 包 worker / userns 路线 | B2 + 独立主机形态 | 按需 | 全量 UI e2e 在 gVisor 下通过 |

推荐组合：**C1 + A1/A2 一期同车**（浏览器缺口是当前唯一的"检查与连接分离"残留，
C1 又顺手把 subprocess 沙箱降权），B 随下一个迭代窗口。

## 7. 风险与回滚

| 风险 | 缓解 | 回滚 |
|------|------|------|
| 代理成为 UI 通道单点故障 | fail-closed 但可 `TP_UI_EGRESS_PROXY=0` 一键回旧行为（保留 goto 双查代码路径） | 配置级 |
| 用户站点被代理误伤（白名单配置错） | 拒绝原因回传到步骤结果（`egress: host ... not in allow`），与 httpx 通道同文案；`auto` 模式下未配策略不启用 | 配置级 |
| gVisor 下 Chromium/低代码行为差异 | B 分阶段：先 runc（共享内核但边界完整）再 runsc；UI 不在沙箱内，兼容面小 | `TP_SANDBOX_BACKEND=subprocess` |
| 非 root 后产物/缓存权限问题 | compose 只读根 + 显式可写白名单；构建期 chown；4.2 清单第 1/2 项把关 | compose `user:` 移除 |
| `--no-sandbox` 渲染逃逸面 | 阶段二 runsc/userns 补回；UI 会话 cap=2、单用例上下文隔离，攻击链长 | — |
| docker.sock sidecar 的信任前提 | 文档明示"sock=宿主 root 等价"；推荐形态是独立主机 + runsc（无 sock） | 形态选择 |
