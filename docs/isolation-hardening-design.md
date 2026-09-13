# 执行隔离与出网管控强化设计

> 📚 文档导航：[设计](design.md) · [数据模型](data-model.md) · [部署](deployment.md) · [UI 探测](ui-probe-design.md)
>
> 状态：设计稿（未实施），r2。来源：2026-09 全项目代码审查确认的三个"设计级遗留"：
>
> | # | 缺口 | 审查出处 | 根治手段 |
> |---|------|----------|----------|
> | A | 浏览器出网无 DNS pinning：goto 重定向中间跳已真实发出、浏览器自解析 DNS 可被 rebinding 利用 | REVIEW_SANDBOX_STACK S3/S6、ui.py goto 注释 | 本地 CONNECT 转发代理（feature A） |
> | B | 低代码沙箱不是安全边界：与 Worker 同 uid/同解释器/完整文件系统，env scrub 可经 /proc 旁路 | REVIEW_SANDBOX_STACK S1、design.md 6.3 升级路径 | 容器化沙箱后端（feature B） |
> | C | Worker 镜像以 root 运行（Chromium 沙箱依赖 userns，docker 默认 seccomp 不可用） | P2 遗留（worker.Dockerfile 注释） | 非 root + 分阶段补偿（feature C） |
>
> **r2 修订**（自审修正，实施前必须读）：Chromium 对 loopback 默认绕过代理（需
> `--proxy-bypass-list=<-loopback>`）；scheduler/worker 共享 artifacts 卷要求跨镜像
> 统一显式 uid；"Worker 降权收窄沙箱面"的论述纠正（同 uid 不构成隔离）；B 的
> sidecar 形态经 docker.sock 挂载宿主路径的语义错误（改 named volume / stdin 引导）；
> 容器 CPU 限额映射错误（`--cpus` ≠ RLIMIT_CPU）；新增跨租户 scratch 可读性分析
> （共享 Worker 形态下 payload 泄漏面）与镜像分发、容器 GC、/dev/shm、fork 炸弹等缺项。
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
  沙箱与 Worker 同 uid：可读 `/proc/<ppid>/environ`（已由启动期 env scrub 缓解，
  见 main.py）、可写 Worker 用户可及的任意文件、可用自己的 httpx 绕过能力桥直连内网。

### 1.2 威胁模型（谁在对抗什么）

| 对抗者 | 能力 | 主要攻击面 | 本设计对策 |
|--------|------|-----------|-----------|
| 恶意/被投毒的租户测试用例（声明式 + 低代码脚本） | 任意 Python 逻辑、任意 URL/参数 | 沙箱逃逸读凭据、绕过 egress 打内网（SSRF→metadata/私网）、读其他租户 payload | B（容器边界 + stdin 引导）+ A（统一出口策略） |
| 被测系统的恶意页面（UI_ACTION 访问的目标） | 页面脚本、重定向、可控子资源 URL | 引导浏览器触达私网/metadata（含环回）、DNS rebinding | A（每连接强制校验 + loopback 不豁免） |
| 被攻破的 Worker 容器内进程 | 容器内权限（C 后非 root、无 cap） | 逃逸到宿主、横向移动 | C（降权 + 只读根 fs）+ B（runsc 时内核级边界） |

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
   浏览器代理不引入新依赖（asyncio 标准库实现）。

---

## 2. Feature A：浏览器出网转发代理（DNS pinning）

### 2.1 为什么选本地转发代理，而不是别的

| 备选 | 结论 |
|------|------|
| `--host-resolver-rules=MAP host ip` 启动参数 | 只能在 launch 时静态映射，goto 时才知道目标；对重定向的每一跳无法动态 pin。**否**（A 的 DNS 静态映射子集由 2.4 的 `TP_EGRESS_DNS_MAP` 承担） |
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

- 一个 Worker 一个共享代理实例（策略是 Worker 级的，连接相互独立），懒启动、
  随 Worker 生命周期关停（不随 UI 会话关停——会话只是使用方）。
- 明文 HTTP：Chromium 经代理发**绝对形式**请求（`GET http://host/path`），
  代理同样做解析→校验→对允许 IP 建连，`Host` 头保持原值（不破坏 vhost 路由）。
- WebSocket：`ws://`/`wss://` 的握手就是 HTTP 升级请求，走同一代理路径，覆盖。
- **loopback 不豁免（r2 关键修正）**：Chromium 的隐式 bypass 规则默认放行
  `localhost/127.0.0.1/[::1]` 目标——不处理的话，`TP_EGRESS_BLOCK_PRIVATE=1` 时
  "重定向到环回地址"的连接恰好落在代理盲区、直接出网，这正是 A 要堵的核心场景。
  必须在 launch 参数加 `--proxy-bypass-list=<-loopback>` 取消隐式豁免（该 token
  表示"从隐式 bypass 集合中移除 loopback"，见 Chromium proxy bypass 文档）。

### 2.3 组件与落点

新增 `worker/src/testpilot_worker/ui_proxy.py`（约 300 行，仅标准库 asyncio）：

```python
class BrowserEgressProxy:
    """Chromium 出网转发代理：每连接执行 egress 策略并对允许 IP 建连。

    - 不解密 TLS：CONNECT 隧道端到端，SNI/证书校验由浏览器与目标完成
    - 解析→校验→连接使用同一次 DNS 结果（复用 egress.resolve_host_for_connect，
      与 httpx 的 EgressPinnedBackend 消费同一实现——策略单点原则）
    - 拒绝：CONNECT 回 403 并关闭隧道 / 明文请求回 403 响应体；同时把
      (时间戳, 连接 id, host, 拒绝原因) 记入环形缓冲，供 goto 异常时关联
      （浏览器侧只能看到通用 net::ERR_*，见 2.4"错误传播"）
    """
    async def start(self) -> str   # 返回 "http://127.0.0.1:<port>"，幂等
    async def stop(self) -> None
    def recent_denials(self) -> list[DenialRecord]
    stats: dict                    # allowed/denied 计数（先日志，后并入 /metrics）
```

改动点：

| 文件 | 改动 |
|------|------|
| `ui.py` `UiSession.ensure()` | 策略启用时 launch 追加参数：`--proxy-server=http://127.0.0.1:1`（占位，见 2.4 Playwright 前置）+ `--proxy-bypass-list=<-loopback>` + `--disable-quic`（+ `--disable-dev-shm-usage`，属 C1 但同在 launch 参数堆里）；每个 context `proxy={"server": await proxy.start()}`。未启用策略时不附加任何参数（dev 行为不变） |
| `ui.py` `execute(GOTO)` | 保留 goto 前字符串校验（快速失败 + 友好文案，`egress: host ... not in allow`）；代理启用后最终 URL 复核降级为断言日志；goto 抛 `net::ERR_TUNNEL_CONNECTION_FAILED`/`ERR_PROXY_*` 类异常时，查 `proxy.recent_denials()` 关联最近 3s 内同会话的拒绝记录，把 egress 拒绝原因附加进步骤失败信息（关联是尽力而为：找不到就保留 Chromium 原始错误 + 指引查 worker 日志） |
| `probes.py` 探测会话 | `ProbeUiSession` 同样接代理（探测访问的页面同属不可信输入） |
| `config.py` | 见 2.5 |
| `main.py` | lifespan 启停共享代理；`/healthz` 附带代理状态 |

### 2.4 协议细节与边界情况

- **错误传播的边界（r2 修正预期）**：代理拒绝在浏览器侧只表现为通用网络错误
  （`net::ERR_TUNNEL_CONNECTION_FAILED` / `ERR_PROXY_CONNECTION_FAILED` 等），
  **拿不到** egress 文案——页面协议层没有携带自定义原因的通道。分层处理：
  ① goto 前的 `acheck_url` 仍是"友好报错层"（顶层导航大多在此拦截）；
  ② 代理拒绝记录进环形缓冲 + worker 日志（带连接 id / 会话 id，可检索）；
  ③ UiSession 按"异常类型 + 时间窗"尽力关联（2.3）。验收以①②③的组合为准，
  不承诺"页面必显 egress 原因"。
- **代理协议要点**（实现清单）：CONNECT 成功必须先回
  `HTTP/1.1 200 Connection Established\r\n\r\n` 再开始双向泵；明文绝对形式请求
  需改写为 origin-form（`GET /path`）转发、`Host` 头保持原值；逐请求剥离
  hop-by-hop 头（`Proxy-Connection`/`Connection`/`Keep-Alive`/`Proxy-Authorization`）；
  双方任一端关闭即拆隧道；解析/校验失败一律 403 + 短响应体（不悬挂连接）。
- **Playwright per-context proxy 前置**：Chromium 的 context 级 proxy 覆盖要求
  browser launch 时也带一个全局代理参数（官方语义：所有 context 都覆盖时全局
  占位代理永远不会被真正使用）。故 launch 固定占位 `--proxy-server=http://127.0.0.1:1`。
- **WebRTC 泄漏**：ICE 候选可绕过 HTTP 代理直连（UDP）。按序验证：launch
  `--disable-webrtc`（生效性随 Chromium 版本有差异，C1 验收清单实测确认）→
  备选 CDP 限制 ICE policy → 兜底靠 B 的容器网络层（UDP 出口随 `--network none`
  一并否决）。UI 引擎当前没有 WebRTC 相关 action，禁用不影响功能。
- **本地滥用面（r2 新增）**：代理监听 `127.0.0.1`，在无 OS 网络隔离的机器上，
  沙箱进程也能把它当 HTTP 出口用——请求仍受同一策略检查（无权限提升），但
  与"沙箱零网络"的叙事冲突，须知悉：net deny（sandbox-exec/bwrap）在位时沙箱
  连 127.0.0.1 也不可达，无在位时沙箱本就能直连出网，代理未恶化任何面。
  可选纵深防御：proxy URL 带每 Worker 随机凭据（`http://<user>:<pass>@127.0.0.1:port`，
  Chromium 支持），拒绝无凭据连接。
- **QUIC/HTTP3**：配置代理后 Chromium 不协商 QUIC（代理仅 TCP），`--disable-quic`
  保险起见一并加。
- **非 http(s) scheme**：goto 前校验已拒绝 `file://`/`ftp://` 等（现状保留）；
  子资源里的 `data:`/`blob:` 不走网络，无面。
- **DNS 映射（测试后门 + 企业 pinning）**：`TP_EGRESS_DNS_MAP=host:ip,host:ip`，
  代理与 httpx 后端共用的解析函数优先查静态映射（不走系统解析）。用途：
  离线 e2e（把 `api.example.com` 映到本机 echo）、给内网固定 IP 的服务显式 pin。
  落点：`egress.resolve_host_for_connect` 头部插入映射查询（一处改动三通道受益）。
  注意它同时是 `<-loopback>` 的配套：CI 里把测试域名映射到 127.0.0.1 的 echo，
  走代理路径（bypass 已取消）即可在无外网环境验证命中/拒绝两分支。
- **IPv6**：`getaddrinfo` 返回的 AAAA 同样过 `_private_ip` 判定（现实现已覆盖）。
- **性能**：每连接多一次本地 loopback 跳 + 一次 Worker 内解析（3s 上限）；UI
  步骤本身是百 ms 级，可忽略。下载产物（数十 MB）经代理是事件循环内双拷贝，
  双向泵按 64KB 块读写即可（不逐字节），UI 会话 cap（probe 2 / 用例并行 16）
  远低于吞吐瓶颈；如未来出现大流量场景再考虑 executor 线程泵。

### 2.5 配置与开关

| 环境变量 | 默认 | 语义 |
|----------|------|------|
| `TP_UI_EGRESS_PROXY` | `auto` | `auto`=当 `TP_EGRESS_ALLOW` 非空或 `TP_EGRESS_BLOCK_PRIVATE=1` 时启用；`1`/`0` 强制开/关（组合矩阵见 §5） |
| `TP_EGRESS_DNS_MAP` | 空 | 静态 host→IP 映射，三通道（httpx/桥/浏览器）共用 |

启动时若策略启用而代理启动失败（端口耗尽等）→ **fail-closed**：UI 步骤全部报
`UiUnavailable(egress proxy failed)`，不允许静默退回无代理模式。

### 2.6 测试

- 单元（`tests/test_ui_proxy.py`）：伪造浏览器流量直接对代理发 CONNECT/绝对 GET——
  白名单命中/拒绝、私网拒绝、DNS 映射、CONNECT 200 应答与双向泵、明文请求改写、
  hop-by-hop 头剥离、半包粘包、无凭据拒绝（若启用代理凭据）。
- 集成：`TP_EGRESS_DNS_MAP` 把测试域名钉到本地 echo，`page.goto` 命中分支；**重
  定向链专项**：echo 返回 302 → `127.0.0.1:<echo端口>`，断言导航失败且代理拒绝
  记录命中（同时就是 `<-loopback>` 生效的回归——若无该参数，环回目标会绕过
  代理直接连上，用例会失败）。
- 错误传播：代理拒绝 → goto 异常信息含关联的 egress 原因（或最近拒绝记录可查）。
- 回归：现有 `test_ui.py`、`test_egress.py` 全绿；`TP_UI_EGRESS_PROXY=0` 时行为与
  当前完全一致。

---

## 3. Feature B：容器化沙箱后端

### 3.1 目标与非目标

- **目标**：低代码/CODE_BLOCK 的不可信 Python 在独立容器内执行——独立 uid、
  独立 mount namespace（只见自己的工作目录）、无网络（`--network none`）、
  资源硬限（memory/pids）、seccomp 默认；无容器运行时时按 `require_isolation`
  fail-closed。
- **非目标**：不做跨租户混部专用集群（独占 Worker 已覆盖）；不引入
  Firecracker/Kata；不重做能力桥语义（stdio 协议保留，仅按需扩展结果帧）。

### 3.2 后端选择与探测顺序

```
TP_SANDBOX_BACKEND = auto | subprocess | container
                        │
        auto: container 可用？ ──否──► subprocess（现状 + require_isolation 判定）
                        │是
        优先 runsc（gVisor）► 其次 docker/podman(runc)
```

- **gVisor 优先**：用户态内核，逃逸面比共享内核小一个量级；低代码脚本只有纯
  Python + 能力桥，无 Chromium/UI 在沙箱内，syscall 兼容面小。
- 探测命令：`docker info --format '{{.ServerVersion}}'`（同时可读
  `{{.Runtimes}}` 判断 runsc 是否注册）/ `podman version --json`；结果缓存 +
  启动日志打印实际选中的后端与 runtime（原则 3）。
- **runsc 只作为 docker/podman 的 runtime handler 使用**（`daemon.json` 注册
  `"runtimes": {"runsc": {"path": "/usr/bin/runsc"}}`，创建容器时
  `--runtime runsc`）。不做"裸 `runsc run` 直调"——它要求调用方自行准备 OCI
  bundle（rootfs 导出/config.json 生成），复杂度远超收益。
- 容器内**不需要**再跑 bwrap/sandbox-exec（网络已由 `--network none` 根治），
  `_net_deny_wrapper` 在 container 后端下跳过。

### 3.3 容器运行规格

```bash
<runtime> run -i --rm \
  --name tp-sbx-<run_id> --label testpilot.sandbox=1 --label run_id=<run_id> \
  --network none \                  # 出口唯一通道是能力桥 stdio（原则 2）
  --read-only \                     # 根文件系统只读；可写仅 /tmp（tmpfs）
  --tmpfs /tmp:size=64m \
  --memory <limits.mem_mb>m \       # 外层硬顶；entry 内 RLIMIT_AS/RLIMIT_CPU 照旧自应用
  --pids-limit <limits.max_procs> \ # fork 炸弹根治（subprocess 后端做不到，见 4.2）
  --user <TP_SANDBOX_UID, 默认 65534:65534> \
  --cap-drop ALL --security-opt no-new-privileges \
  -- <sandbox 镜像> python -m testpilot_sdk.entry --stdin-bootstrap <entry>
```

要点：

- **负载与产物的两条通道（r2 重设计，核心）**。经 docker.sock 创建容器时，
  `-v <路径>` 的宿主侧路径由 **daemon 所在主机**解析——worker 容器内的
  `/tmp/tp-sandbox-*` 对宿主不可见，直接挂载会得到宿主的空目录（r1 的原始
  方案在 sidecar 形态下第一天就失效）。且共享 scratch 卷还有一个更隐蔽的
  面租户泄漏：多租户共享 Worker（tenant_id=0）时，A 租户的 payload（含敏感
  变量明文）落在共享卷上，B 租户的沙箱（同容器 uid）可读。因此：
  - **默认：stdin 引导（无卷）**。payload 与源码不走文件系统挂载——worker
    把 `user_case.py + payload.json` 打成一个微型 tar 从 stdin 送入，entry
    新增 `--stdin-bootstrap` 模式：先读 tar 解到自己的 tmpfs（/tmp），再照常
    执行。容器零挂载，路径域问题与跨租户可读面同时消失。
  - **产物回流：结果帧扩展**（向后兼容）。现 subprocess 后端由 worker 从
    scratch 收产物文件；容器模式改为 result JSON 帧新增可选 `artifacts`
    字段（`[{name, data_b64}]`，总量上限 8MB，超限报错）——SDK 侧产物本就是
    显式 `ctx.artifact(...)` 声明（量小：截图之外低代码基本没有），8MB 覆盖
    现实用例；声明式引擎/Playwright 的产物不经过沙箱，不受影响。
  - **卷模式保留给独占 Worker**：`TP_SANDBOX_SCRATCH=volume` 时挂 named
    volume（daemon 管理路径，两侧语义一致）+ per-run 子目录 + 文件 0644——
    仅建议单租户独占 Worker 使用（无跨租户面），省去 base64 往返。
- **镜像**：`deploy/worker.Dockerfile` 增加多阶段 `sandbox` target——同一份
  pyproject/sdk 构建，但不含 playwright/Chromium（镜像从 ~1GB 降到 ~200MB 级，
  分发与冷启动都受益）；解释器与依赖和 worker 一致，无第二套依赖维护。
- **stdio 即桥**：`docker run -i --rm` attach 的 stdin/stdout 对接现有
  `_Bridge` 管线，协议零改动（除上述结果帧可选扩展）；stderr 照旧为日志通道。
- **限额分层（r2 修正）**：`limits.cpu_seconds` 是 RLIMIT_CPU（总量）语义，
  容器对等物是 `--ulimit cpu=`，**不是** `--cpus`（速率）——且现行机制本就是
  entry 在子进程内自应用 rlimit（降额 setrlimit 非特权可调用），容器内保留；
  容器只额外加 `--memory`/`--pids-limit` 两个外层硬顶（rlimit 管不了的面）。
- **生命周期治理**：所有沙箱容器打 `testpilot.sandbox=1` + `run_id` label；
  worker 启动时按 label GC 残留容器（`ps -aq --filter label=... | xargs rm -f`，
  覆盖 worker 崩溃后 `--rm` 未执行的情况）；容器创建加信号量（默认并发 4），
  防 behavior 压测的 spawn 突发打爆 daemon。
- **行为压测循环模式**（stress.py `_run_behavior`）每迭代 spawn 沙箱：容器冷
  启动 runc 约 100–300ms、runsc 首启更慢。首版接受（吞吐瓶颈分析见审查 S-D）；
  后续可加**每 Worker 预热池**（N 个已启动等 payload 的容器，仅无状态迭代可
  复用，用 payload 序号校验防串台）。

### 3.4 部署形态（与部署层的接口）

| 形态 | 做法 | 适用 |
|------|------|------|
| compose（单机生产，本期默认） | 可选 sidecar `dockerhost`（挂 `docker.sock`），worker 经 `DOCKER_HOST` 使用；scratch 走 stdin 引导（无挂载，天然免疫 sock 路径域问题） | 快速落地；**接受 docker.sock = 宿主 root 等价**的信任前提（worker 已被 C 降权 + cap 收敛，攻击链要求先逃逸 worker 容器） |
| 独立 Worker 主机（推荐的生产终态） | 裸机/VM 装 docker/podman + runsc handler，worker 直连本机 daemon（无 sock 暴露面） | 最强边界 |
| k8s（远期） | 沙箱 Pod 用 RuntimeClass=gVisor；本设计的参数与 RuntimeClass handler 一一对应 | design 13.4 之后的阶段 |

**镜像分发（r2 补）**：宿主 daemon 必须先拿到 sandbox 镜像。CI 已推
`ghcr.io/<owner>/testpilot-worker`（cd.yml）——sidecar/独立主机形态给 daemon
配置 ghcr 只读凭据拉取；离线环境 `docker save | docker load`。镜像 digest 记入
worker 启动日志（排查"沙箱行为和预期不符"时先对版本）。

`TP_SANDBOX_BACKEND=container` 但探测失败 + `require_isolation=1` → Worker 启动
Fatal（明确报缺哪个组件）；`require_isolation=0` → 降级 subprocess 并 Warn。

### 3.5 改动落点

| 文件 | 改动 |
|------|------|
| `sandbox.py` | 新增 `ContainerBackend(ExecutionBackend)`（复用 `run()` 清理骨架，抽出 `SandboxProcess` 协议：`wait/kill/stdin/stdout/stderr`）；`SandboxResult` 增加可选 `artifacts`；`make_backend()` 工厂按 `TP_SANDBOX_BACKEND` 选择 |
| `testpilot_sdk/entry.py` + `bridge.py` | `--stdin-bootstrap` 模式（tar 解包到 tmpfs）；result 帧 `artifacts` 可选字段（b64，8MB 上限，旧 worker 忽略新字段=向后兼容） |
| `engine.py` 产物归集 | 沙箱 artifacts 字段 → 现有 UiArtifact/上传管线（uri 命名沿用 `sbx/<name>`） |
| `main.py` / `engine.py` / `stress.py` / `probes.py` | 构造 `SubprocessBackend` 的位置改走工厂（约 4 处调用点，签名不变）；main 启动时 label-GC 残留容器 |
| `config.py` | `TP_SANDBOX_BACKEND`、`TP_SANDBOX_CONTAINER_RUNTIME`、`TP_SANDBOX_UID`、`TP_SANDBOX_SCRATCH`（stdin/volume） |
| `deploy/worker.Dockerfile` | 多阶段 `sandbox` target；文档给 runsc handler 主机安装步骤 |
| `docs/deployment.md` | "沙箱隔离等级"一节：三级矩阵（subprocess 尽力而为 / container-runc / container-runsc）+ 宿主要求 + 镜像分发 |

### 3.6 测试

- 单元：`SandboxProcess` 协议的假实现跑通桥协议全套既有测试（`test_bridge.py`、
  `test_sandbox_isolation.py` 参数化到两个后端）。
- 集成（CI 带 docker 的 runner；本地 dev.sh 检测）：真容器跑样例脚本——网络否定
  断言（容器内 `socket` 连接必须失败）、只读 rootfs（写 `/usr` 失败）、env 断言
  （无 TP_WORKER_TOKEN 等泄漏）、超时 kill、`/etc/passwd` 不可读以外的越权路径、
  stdin 引导的 payload 正确落地、artifacts 回流（含超 8MB 报错分支）、
  **worker kill -9 后重启 GC 清残留容器**（按 label 断言清零）、跨租户负向
  （卷模式下 B run 容器读不到 A run 目录——若保留卷模式）。
- 性能基线：冷启动开销记录到 `docs/deployment.md`（runc 目标 <300ms p95，
  含 daemon 创建；runsc 单列）。

---

## 4. Feature C：Worker 镜像降权

### 4.1 约束：Chromium 沙箱与 user namespaces

非 root 跑 Chromium 的两难：

- Chromium 渲染进程沙箱依赖 `CLONE_NEWUSER`。docker 默认 seccomp profile 拦
  `unshare`（除非 `CAP_SYS_ADMIN`）→ 非 root 容器内 Chromium 必须加
  `--no-sandbox`，渲染层隔离降级。
- 保持 root 则镜像内 uid 0，逃逸后即宿主 root 等价。

**决策：分两阶段，方向是"外层边界换内层沙箱"。**

### 4.2 阶段一：非 root + `--no-sandbox` + 补偿控制（本期）

**前置（r2 新增，不做则产物链路当场断）：跨镜像统一 uid。**
`artifacts` 是 scheduler 与 worker 共享的 named volume；当前 scheduler 镜像
tp=100（busybox `adduser -S` 分配）、worker 为 root——worker 降为非 root 后若
uid 与 scheduler 不一致，写对方建立的目录会 EACCES。规定：scheduler 与 worker
镜像用**同一显式 uid 常量**（建议 1500，两 Dockerfile 同一 `ARG TP_UID=1500` +
`useradd -u` / `adduser -u`），compose `user:` 同值；本次发布必须同时重建两个
镜像（已在运行的卷目录属主用一次性 init/chown 迁移，部署文档给步骤）。
copilot 不挂 artifacts 卷，不受影响。

具体改动：

| 项 | 内容 |
|----|------|
| `worker.Dockerfile` | 构建 Phase 保持 root（`playwright install --with-deps` 装系统库）；`ARG TP_UID=1500` + `useradd -u $TP_UID -m tp`；`PLAYWRIGHT_BROWSERS_PATH=/home/tp/.cache/ms-playwright`（构建期 chown）；`USER tp`。`scheduler.Dockerfile` 同步改 `-u $TP_UID` |
| `entry.py` / `ui.py` | chromium launch args：`--no-sandbox`（**仅当** `os.getuid() != 0` 或 `TP_CHROMIUM_NO_SANDBOX=1`，root 场景不加保持现状语义）+ `--disable-dev-shm-usage`（无条件：容器 /dev/shm 默认 64MB，Chromium page crash 的经典成因，与 uid 无关；或 compose 加 `shm_size: 1gb` 二选一，默认选 flag 更省内存） |
| `docker-compose.prod.yml` worker | `user: "1500:1500"`、`read_only: true`、`tmpfs: /tmp`、可写卷仅 `/home/tp`（chromium/XDG 缓存）与 `/data/artifacts`、`cap_drop: [ALL]`、`security_opt: [no-new-privileges:true]` |
| 文件权限 | `/data/artifacts` named volume 首挂继承镜像内属主（uid 统一后两侧一致）；产物上传经 gRPC 流不受影响 |

**降权收益的诚实边界（r2 重写）**：
- 真正的收益是**容器逃逸影响面**：逃出 worker 容器的进程是非 root、无任何
  cap、根文件系统只读——对宿主的横向移动成本显著抬高。
- **不是**沙箱隔离：subprocess 沙箱与 worker 同为 tp uid，沙箱读 tp 属主的
  worker 文件、`/proc/<ppid>/environ` 的能力**不受降权影响**（后者已由启动期
  env scrub 缓解，与 uid 无关；前者要等 B 的容器边界 + 独立 uid）。
- **已知代价——本地 fork 炸弹面变差**：RLIMIT_NPROC 是 per-uid 限制，root 下
  worker 豁免、tp 下 worker 与沙箱共享进程预算，沙箱 fork 炸弹可让 worker 无法
  再起进程（DoS 自伤，不跨租户）。一期接受（entry 的 rlimit 仍在，量级受限）；
  根治靠 B 的 `--pids-limit`（cgroup 级、不占 worker 预算）。风险表挂条目。

验证清单（compose 实测）：

1. worker 与 scheduler 容器 `id -u` 相同（=1500）；`/home/tp`、`/data/artifacts`、
   `/tmp` 可写，其余只读；共享卷内互写对方目录成功（uid 一致性回归）。
2. 功能/低代码/UI 三类样例任务全过；HAR/trace/截图产物正常落盘上传（含大页面
   长截图，验证 /dev/shm 处理后无 page crash）。
3. launch args 单测：非 root 附加 `--no-sandbox`/`--disable-dev-shm-usage`，
   root 不附加；e2e goto 本地页通过。
4. `capsh --print` 显示无任何 cap；`no-new-privileges` 生效（`su` 失败）。
5. 沙箱内读 `/proc/1/environ`：无任何 TOKEN/KEY 类值（env scrub 回归）。
6. `--proxy-bypass-list=<-loopback>` 在带代理启动时存在（A 的 C1 联动断言）。

### 4.3 阶段二（可选增强，按需排期）

- **runsc 包 worker 自身**：宿主装 gVisor 并把 worker 容器 runtime 换
  runsc——`--no-sandbox` 的渲染层损失被外层用户态内核补回。代价：全量 UI
  e2e 在 gVisor 下的兼容性回归（Playwright+Chromium 官方支持，仍需实测）。
- **userns 路线**（替代 runsc）：自定义 seccomp profile 放行
  `unshare(CLONE_NEWUSER)` → Chromium 保留真实渲染沙箱。适合能管控宿主
  docker 参数的独立 Worker 主机（与 B 的推荐形态合并实施）。

### 4.4 明确不做

- rootless docker/podman 跑 worker 本体：与 B 的沙箱容器嵌套组合复杂度不成
  比例；独立主机形态下直接裸进程 + systemd 更简单。
- Firecracker/Kata：见 design.md 6.3 决策——独占 Worker 已覆盖最强隔离需求。

---

## 5. 配置面汇总

新增配置（worker，YAML 键 ↔ env 自动映射，规则同现有 `TP_*`）：

| env | 默认 | 说明 |
|-----|------|------|
| `TP_UI_EGRESS_PROXY` | `auto` | 浏览器转发代理（组合矩阵见下） |
| `TP_EGRESS_DNS_MAP` | 空 | `host:ip,...` 静态解析映射（三通道共用） |
| `TP_SANDBOX_BACKEND` | `auto` | `auto`（容器可用即用）/ `subprocess` / `container` |
| `TP_SANDBOX_CONTAINER_RUNTIME` | `auto` | `auto` / `runsc` / `docker` / `podman`（runsc 须先在 daemon 注册为 handler） |
| `TP_SANDBOX_UID` | `65534` | 容器后端 `--user`（与 worker uid 不同即可，默认 nobody） |
| `TP_SANDBOX_SCRATCH` | `stdin` | `stdin`（默认，无挂载）/ `volume`（仅独占 Worker 建议） |
| `TP_CHROMIUM_NO_SANDBOX` | `auto` | 非 root 自动附加；显式 1/0 覆盖 |

`TP_UI_EGRESS_PROXY` 与 egress 策略的组合语义（r2 补矩阵）：

| TP_EGRESS_ALLOW / BLOCK_PRIVATE | TP_UI_EGRESS_PROXY | 行为 |
|---|---|---|
| 均未配置（dev 默认） | `auto` | 代理关闭；行为与现状一致 |
| 任一配置 | `auto` | 代理强制启用；启动失败 = UI fail-closed |
| 任一配置 | `0` | 代理关闭；仅剩 goto 前后字符串校验（**回到现状缺口**，仅调试用，生产禁用） |
| 均未配置 | `1` | 代理启用但策略空 = 全放行（仅用于提前验证代理基础设施本身） |

启动时打印一行隔离等级摘要（强制项）：

```
isolation: sandbox=container(runsc via docker 1.8) scratch=stdin ui_egress_proxy=on(policy) chromium_no_sandbox=on(non-root) worker_uid=1500
```

## 6. 实施计划与验收

| 阶段 | 内容 | 依赖 | 工作量 | 验收门 |
|------|------|------|--------|--------|
| A1 | `egress.resolve_host_for_connect` 增加 DNS 映射 + 单测 | 无 | 0.5d | `test_egress.py` 新增映射用例 |
| A2 | `ui_proxy.py` 代理 + UiSession/probe 接线 + launch 参数（含 `<-loopback>`）+ 开关 | A1 | 3d | 2.6 全部（重点：重定向链→环回被拦、`<-loopback>` 回归）；`TP_UI_EGRESS_PROXY=0` 行为不变 |
| C1 | **uid 统一（scheduler+worker 同批重建）** + worker 非 root + `--no-sandbox`/`--disable-dev-shm-usage` 条件附加 + compose 收敛 | 无（建议与 A2 同车） | 2d | 4.2 六项验证清单 |
| B1 | `SandboxProcess` 协议 + `ContainerBackend`（runc、stdin 引导、结果帧 artifacts）+ label/GC | 无 | 4–5d | 桥协议测试参数化双后端全绿；3.6 集成用例（含 worker 崩溃 GC、跨租户负向） |
| B2 | runsc handler 探测优先 + sandbox 镜像 target/分发 + 部署文档 + sidecar 形态 | B1 | 2d | `deployment.md` 三级矩阵 + 镜像分发步骤；runsc 冒烟 |
| C2 | runsc 包 worker / userns 路线 | B2 + 独立主机形态 | 按需 | 全量 UI e2e 在 gVisor 下通过 |

推荐组合：**C1 + A1/A2 一期同车**（浏览器是当前唯一"检查与连接分离"的残留，
C1 的 uid 统一是纯部署协调项宜早做），B 随下一个迭代窗口。

## 7. 风险与回滚

| 风险 | 缓解 | 回滚 |
|------|------|------|
| 代理成为 UI 通道单点故障 | fail-closed 但可 `TP_UI_EGRESS_PROXY=0` 一键回旧行为（保留 goto 双查代码路径） | 配置级 |
| 代理误伤用户站点（白名单配置错） | 顶层导航由前置 `acheck_url` 给出与 httpx 通道同文案的友好错误；代理层拒绝有带连接 id 的日志与环形缓冲可关联 | 配置级 |
| `<-loopback>`/`--disable-webrtc` 参数在特定 Chromium 版本行为漂移 | 两者均列入 C1/A2 验收断言（重定向链→环回用例本身就是回归探针）；漂移时升级参数或走 CDP 备选 | 参数级 |
| scheduler/worker uid 不一致 → 共享卷写失败 | 两镜像同一 `ARG TP_UID` 常量 + 同批发布 + 一次性 chown 迁移步骤（部署文档）；验收清单第 1 项把关 | 重建镜像 |
| 非 root 后沙箱 fork 炸弹耗尽 worker 进程预算（同 uid nproc） | 一期接受（entry rlimit 限幅）；B 的 `--pids-limit` 根治；风险窗口内监控 worker 进程创建失败率 | — |
| `--no-sandbox` 渲染逃逸面 | 阶段二 runsc/userns 补回；UI 会话 cap=2、每用例独立 context，攻击链长 | — |
| docker.sock sidecar 的信任前提 | 文档明示"sock=宿主 root 等价"；scratch 默认 stdin 引导不依赖挂载；推荐形态是独立主机（无 sock） | 形态选择 |
| 容器 daemon 故障/限流拖垮沙箱执行 | 创建并发信号量（4）；daemon 不可达时按 `require_isolation` 决定降级或 fail-closed；label-GC 防残留累积 | `TP_SANDBOX_BACKEND=subprocess` |
| 结果帧 artifacts 的 8MB 上限不满足某些用例 | 超限显式报错（不静默截断）；独占 Worker 可切 `TP_SANDBOX_SCRATCH=volume` 走文件 | 配置级 |
