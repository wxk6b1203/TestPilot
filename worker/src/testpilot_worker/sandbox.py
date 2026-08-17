"""低代码沙箱：subprocess 后端 + 能力桥服务端。

隔离模型（docs/design.md §6.3）：
- subprocess 后端：独立进程 + setrlimit + env scrub + scratch 目录 + 超时强杀
- 能力桥：沙箱内 SDK 是瘦客户端，HTTP/变量等副作用经控制通道（stdin/stdout
  JSON Lines）转发给 Worker 执行 —— 沙箱进程无网络出口（sandbox-exec/bwrap
  可用时强制）、无 Worker 凭据（env scrub）、无明文密钥；用户 print 重定向到
  stderr（日志通道），不污染协议帧
- ExecutionBackend 抽象：后续可切 container(gVisor)/dedicated 后端

安全修复（P0 审查）：
- bwrap 参数：必须 --ro-bind / /（空 rootfs 下解释器不存在，Linux 必启动失败）
- 取消/超时路径 finally 化：CancelledError 也杀进程、收管道、清 scratch
- 超时/非零退出 → 无条件失败（防沙箱抢先伪造 ok=true 的 result 帧）
- 沙箱输出/桥操作/日志 设上限（防刷屏 OOM Worker）
- SandboxLimits 惰性读 env（修复 CLI/YAML 配置时序失效）
"""

from __future__ import annotations

import asyncio
import inspect
import json
import os
import platform
import shutil
import subprocess
import sys
import tempfile
import time
from abc import ABC, abstractmethod
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Awaitable, Callable

import httpx

from . import egress

log = __import__("logging").getLogger("testpilot.sandbox")

HttpHandler = Callable[[dict[str, Any]], Awaitable[dict[str, Any]]]
OpHandler = HttpHandler  # 扩展桥操作处理器（args dict → result dict）
# api_request 处理器需要 (args, merged_vars, payload)：接口快照执行时要运行
# pre/post 脚本并把变量写回当前沙箱 ctx.vars。
ApiHandler = Callable[[dict[str, Any], dict[str, Any], dict[str, Any]], Awaitable[dict[str, Any]]]

# 能力桥通道上限
_BRIDGE_RESP_LIMIT = 256 * 1024
_BRIDGE_REQ_BODY_LIMIT = 1 * 1024 * 1024
# 沙箱输出与桥操作上限（防租户脚本 DoS Worker）
_MAX_LOG_LINES = 2000          # 日志/输出行数上限（超限丢弃并终止进程）
_MAX_CONCURRENT_OPS = 64       # 桥 op 并发上限（超限拒绝）
_MAX_RESP_HEADERS = 200        # 桥响应 header 条目上限
_MAX_PROTO_LINE = 1 << 20      # 协议通道(fd1)单行上限：脚本 os.write(1, b"x"*1GB) 无换行
                               # 可让 Worker 缓冲任意大小（rlimit 只限沙箱自身）——超限杀进程


def _env_int(key: str, default: int) -> int:
    try:
        return int(os.environ.get(key, str(default)))
    except ValueError:
        return default


@dataclass
class SandboxLimits:
    # 惰性读取：实例化时才取 env（修复模块 import 时冻结导致的配置失效）
    cpu_seconds: int = field(default_factory=lambda: _env_int("TP_SANDBOX_CPU", 30))
    mem_mb: int = field(default_factory=lambda: _env_int("TP_SANDBOX_MEM_MB", 1024))
    max_procs: int = field(default_factory=lambda: _env_int("TP_SANDBOX_NPROC", 128))
    max_fds: int = field(default_factory=lambda: _env_int("TP_SANDBOX_NOFILE", 128))
    max_fsize_mb: int = field(default_factory=lambda: _env_int("TP_SANDBOX_FSIZE_MB", 32))
    net_deny: bool = field(default_factory=lambda: os.environ.get("TP_SANDBOX_NET", "deny") != "allow")
    # 隔离强制开关（默认关=尽力而为）：开启后若无 OS 隔离工具（sandbox-exec/bwrap）
    # 可用，沙箱直接失败（fail-closed）而非静默裸奔。生产配合容器后端使用。
    require_isolation: bool = field(
        default_factory=lambda: os.environ.get("TP_SANDBOX_REQUIRE_ISOLATION", "") in ("1", "true", "yes"))


@dataclass
class SandboxResult:
    ok: bool
    error: str = ""
    logs: list[str] = field(default_factory=list)
    vars: dict[str, Any] = field(default_factory=dict)
    assertions: list[dict[str, Any]] = field(default_factory=list)
    duration_ms: int = 0
    timed_out: bool = False


class ExecutionBackend(ABC):
    """低代码执行后端抽象（subprocess / container / dedicated）。"""

    @abstractmethod
    async def run(self, source: str, entry: str, payload: dict[str, Any],
                  timeout_s: float, loop: bool = False) -> SandboxResult: ...


def limits_env_json(limits: SandboxLimits) -> str:
    """沙箱限额序列化为子进程 env；由 testpilot_sdk.entry 在用户代码加载前应用。

    不再使用 preexec_fn：Worker 进程含 grpc aio 线程，Python 文档明确
    多线程进程中的 preexec_fn 存在 fork 死锁风险（M22）。
    """
    return json.dumps({
        "cpu_seconds": limits.cpu_seconds,
        "mem_mb": limits.mem_mb,
        "max_procs": limits.max_procs,
        "max_fds": limits.max_fds,
        "max_fsize_mb": limits.max_fsize_mb,
    }, ensure_ascii=False)


def _scrub_env(scratch: str, sdk_root: str, payload_path: str, limits_json: str) -> dict[str, str]:
    """环境白名单：不继承 Worker 的任何变量/凭据；rlimits 经 env 传给子进程入口。"""
    return {
        "PATH": os.environ.get("PATH", "/usr/bin:/bin"),
        "HOME": scratch,
        "TMPDIR": scratch,
        "LANG": "en_US.UTF-8",
        "PYTHONPATH": os.pathsep.join([sdk_root, scratch]),
        "PYTHONNOUSERSITE": "1",
        "PYTHONHASHSEED": "0",
        "TP_PAYLOAD": payload_path,
        "TP_SANDBOX_LIMITS": limits_json,
    }


_NET_DENY_PROFILE = """(version 1)
(allow default)
(deny network-outbound)
(deny network-inbound)
(deny network-bind)
"""


# sandbox-exec 可用性探测缓存（受限环境/CI 沙箱内 sandbox_apply 会被系统拒绝，
# 若不加探测则每个沙箱必启动失败——与"无工具"一样优雅降级为无网络隔离）
_sandbox_exec_ok: bool | None = None


async def _probe_sandbox_exec() -> None:
    """异步探测 sandbox-exec 可用性（同步 subprocess.run 会冻结事件循环 5s）。"""
    global _sandbox_exec_ok
    if _sandbox_exec_ok is not None:
        return
    try:
        probe = await asyncio.create_subprocess_exec(
            "sandbox-exec", "-p", "(version 1)(allow default)", "/usr/bin/true",
            stdout=asyncio.subprocess.DEVNULL, stderr=asyncio.subprocess.DEVNULL)
        rc = await asyncio.wait_for(probe.wait(), timeout=5)
        _sandbox_exec_ok = rc == 0
    except Exception:
        _sandbox_exec_ok = False
    if not _sandbox_exec_ok:
        log.warning("sandbox-exec unavailable (restricted environment); "
                    "sandbox runs WITHOUT network denial")


def _kill_process_group(proc) -> None:
    """SIGKILL 整个进程组（沙箱 start_new_session 后为独立会话）；
    仅杀主进程会让其派生的子进程继续运行（rlimit 允许时）成孤儿。"""
    import signal
    try:
        os.killpg(proc.pid, signal.SIGKILL)
    except (ProcessLookupError, PermissionError):
        try:
            proc.kill()
        except ProcessLookupError:
            pass


class _LineReader:
    """带行长上限的按行读取器：无换行的巨量输出（如 os.write(1, b'x'*1GB)）
    不得让 Worker 无限缓冲——超过 max_len 返回 None（调用方应终止进程）。"""

    def __init__(self, reader: asyncio.StreamReader, max_len: int):
        self.reader = reader
        self.max_len = max_len
        self.buf = b""

    async def readline(self) -> bytes | None:
        while True:
            idx = self.buf.find(b"\n")
            if idx >= 0:
                line, self.buf = self.buf[:idx], self.buf[idx + 1:]
                return line
            if len(self.buf) > self.max_len:
                return None
            chunk = await self.reader.read(4096)
            if not chunk:
                if self.buf:
                    line, self.buf = self.buf, b""
                    return line
                return b""
            self.buf += chunk


def _net_deny_wrapper(cmd: list[str], scratch: str) -> tuple[list[str], bool]:
    """OS 级网络隔离（尽力而为）：macOS sandbox-exec / Linux bubblewrap。
    返回 (命令, 是否真正隔离)；无法隔离时返回原命令 + False（由调用方按
    require_isolation 决定降级或 fail-closed）。
    sandbox-exec 可用性须先经 _probe_sandbox_exec（异步，不阻塞事件循环）确认。"""
    if platform.system() == "Darwin" and shutil.which("sandbox-exec") and _sandbox_exec_ok:
        profile = os.path.join(scratch, "sandbox.sb")
        Path(profile).write_text(_NET_DENY_PROFILE)
        return ["sandbox-exec", "-f", profile, *cmd], True
    if platform.system() == "Linux" and shutil.which("bwrap"):
        # 必须 --ro-bind / /：bwrap 默认空 rootfs，解释器不存在则 exec 必失败。
        # --die-with-parent：父进程退出时子进程连带终止（防孤儿）。
        # --tmpfs /tmp 会用全新 tmpfs 遮蔽 host 的 /tmp——scratch（mkdtemp 默认在
        # /tmp）与其中的 user_case.py/payload.json 将不可见，entry 必失败；因此
        # 显式 --bind scratch scratch 重新挂载（可写；scratch 为专用临时目录）。
        return ["bwrap", "--ro-bind", "/", "/", "--proc", "/proc",
                "--dev", "/dev", "--tmpfs", "/tmp",
                "--bind", scratch, scratch,
                "--unshare-net", "--die-with-parent", "--", *cmd], True
    log.warning("no OS sandbox tool found (sandbox-exec/bwrap); "
                "sandbox runs WITHOUT network denial — 建议容器后端")
    return cmd, False


class SubprocessBackend(ExecutionBackend):
    def __init__(self, http_handler: HttpHandler, limits: SandboxLimits | None = None,
                 extra_ops: dict[str, OpHandler] | None = None,
                 api_handler: ApiHandler | None = None,
                 grpc_handler: HttpHandler | None = None):
        self.http_handler = http_handler
        self.limits = limits or SandboxLimits()
        self.extra_ops = extra_ops or {}  # 扩展桥操作（如 ui_action → bridge_ui_handler）
        self.api_handler = api_handler   # op=api_request（按接口 ID 执行，见 lowcode_api）
        self.grpc_handler = grpc_handler  # op=grpc_request（无 api_id 的 raw gRPC）
        # 循环模式（行为压测）：每个迭代结束的回调（可 async），在控制循环协程内直接调用
        self._loop_cb: Callable[[dict], Any] | None = None

    def set_loop_callback(self, cb: Callable[[dict], Any] | None) -> None:
        """循环模式迭代回调（entry 的 iteration event → cb(msg)）。"""
        self._loop_cb = cb

    async def run(self, source: str, entry: str, payload: dict[str, Any],
                  timeout_s: float, loop: bool = False,
                  extra_files: dict[str, str] | None = None) -> SandboxResult:
        started = time.perf_counter()
        result = SandboxResult(ok=False)
        scratch = tempfile.mkdtemp(prefix="tp-sandbox-")
        # sdk_root = worker/src（包含 testpilot_sdk 包）
        sdk_root = str(Path(__file__).resolve().parent.parent)
        src_path = os.path.join(scratch, "user_case.py")
        payload_path = os.path.join(scratch, "payload.json")
        Path(src_path).write_text(source, encoding="utf-8")
        for name, content in (extra_files or {}).items():
            if "/" in name or "\\" in name or name.startswith("."):
                raise ValueError(f"invalid extra file name: {name!r}")
            Path(scratch, name).write_text(content, encoding="utf-8")
        if loop:
            payload["loop"] = True  # entry 循环模式：迭代门控 + iteration 事件流
        Path(payload_path).write_text(json.dumps(payload, ensure_ascii=False), encoding="utf-8")

        cmd = [sys.executable, "-m", "testpilot_sdk.entry", src_path, entry]
        isolated = False
        if self.limits.net_deny:
            # macOS sandbox-exec 可用性探测：异步执行，不阻塞事件循环 5s
            if platform.system() == "Darwin" and shutil.which("sandbox-exec") and _sandbox_exec_ok is None:
                await _probe_sandbox_exec()
            cmd, isolated = _net_deny_wrapper(cmd, scratch)
            if not isolated and self.limits.require_isolation:
                # fail-closed：强制隔离但无工具（如本地无 gVisor/bwrap/sandbox-exec）
                result.error = (
                    "sandbox isolation required (TP_SANDBOX_REQUIRE_ISOLATION=1) "
                    "but no OS sandbox tool available; refusing to run unprotected "
                    "or set TP_SANDBOX_NET=allow to disable isolation")
                shutil.rmtree(scratch, ignore_errors=True)
                return result

        try:
            proc = await asyncio.create_subprocess_exec(
                *cmd,
                stdin=asyncio.subprocess.PIPE,   # Worker → 沙箱：桥响应
                stdout=asyncio.subprocess.PIPE,  # 沙箱 → Worker：桥请求/事件
                stderr=asyncio.subprocess.PIPE,  # 用户日志（print 已重定向到这里）
                cwd=scratch,
                env=_scrub_env(scratch, sdk_root, payload_path, limits_env_json(self.limits)),
                start_new_session=True,  # 独立进程组：kill 时可连子孙一起杀（M21）
            )
        except Exception as e:
            result.error = f"sandbox spawn failed: {e}"
            shutil.rmtree(scratch, ignore_errors=True)
            return result

        done = asyncio.Event()
        merged_vars: dict[str, Any] = {}
        op_inflight = 0
        log_overflow = False

        def _append_log(line: str) -> None:
            nonlocal log_overflow
            # gRPC C-core 在 fork 后继承 fd 的噪声（父进程已启用 grpc 时每个沙箱都打印）
            if "ev_poll_posix.cc" in line and "FD from fork parent" in line:
                return
            if len(result.logs) >= _MAX_LOG_LINES:
                if not log_overflow:
                    log_overflow = True
                    result.logs.append("[logs truncated: output limit exceeded]")
                return
            result.logs.append(line[:2000])

        async def _respond(call_id: int, ok: bool, value: Any):
            msg = {"id": call_id, "ok": ok, "result" if ok else "error": value}
            try:
                proc.stdin.write(json.dumps(msg, ensure_ascii=False).encode() + b"\n")
                await proc.stdin.drain()
            except (BrokenPipeError, ConnectionResetError):
                done.set()

        async def _control_loop():
            nonlocal op_inflight
            reader = _LineReader(proc.stdout, _MAX_PROTO_LINE)
            while True:
                line = await reader.readline()
                if line is None:
                    # 协议行超限（无换行的巨量输出）：终止沙箱防 Worker OOM
                    _append_log("[bridge protocol line exceeded limit; killing sandbox]")
                    _kill_process_group(proc)
                    break
                if not line:
                    break
                try:
                    msg = json.loads(line)
                except ValueError:
                    continue
                mtype = msg.get("type")
                if mtype == "log":
                    _append_log(str(msg.get("message", "")))
                elif mtype == "result":
                    result.ok = bool(msg.get("ok"))
                    result.error = str(msg.get("error", ""))[:4000]
                    result.vars = msg.get("vars") or {}
                    result.assertions = msg.get("assertions") or []
                    done.set()
                elif mtype == "op":
                    # 并发上限：超限拒绝而非无限 create_task（防沙箱 flood DoS Worker）
                    if op_inflight >= _MAX_CONCURRENT_OPS:
                        try:
                            await _respond(int(msg.get("id", 0)), False,
                                           "too many concurrent bridge ops")
                        except (ValueError, TypeError):
                            pass
                        continue
                    op_inflight += 1
                    task = asyncio.create_task(self._handle_op(msg, _respond, payload, merged_vars))
                    task.add_done_callback(lambda _t: op_decrement())
                elif mtype == "event" and msg.get("name") == "iteration" and self._loop_cb:
                    # 回调异常不再杀死整个控制循环（那样后续 op/event 无人处理）；
                    # 记错误并终止沙箱，由父层按失败收尾。
                    try:
                        res = self._loop_cb(msg)
                        if inspect.isawaitable(res):
                            await res
                    except Exception as e:
                        _append_log(f"loop callback failed: {type(e).__name__}: {e}")
                        result.error = f"loop callback failed: {e}"
                        _kill_process_group(proc)
                        break

        def op_decrement():
            nonlocal op_inflight
            op_inflight = max(0, op_inflight - 1)

        async def _drain(stream):
            while True:
                line = await stream.readline()
                if not line:
                    break
                _append_log(line.decode("utf-8", "replace").rstrip())

        pump_err = asyncio.create_task(_drain(proc.stderr))
        ctrl = asyncio.create_task(_control_loop())

        # 全程 finally：超时、取消、异常路径都保证杀进程组/收管道/清 scratch
        try:
            try:
                await asyncio.wait_for(done.wait(), timeout=max(timeout_s, 1))
            except TimeoutError:
                result.timed_out = True
                result.error = f"sandbox timeout after {timeout_s}s (killed)"
                result.ok = False
                _kill_process_group(proc)
            except asyncio.CancelledError:
                result.timed_out = True
                result.error = "sandbox cancelled"
                result.ok = False
                _kill_process_group(proc)
                raise  # 取消向上传播（finally 清理后）
            await proc.wait()

            if not done.is_set() and not result.error:
                # 进程结束但没发 result（脚本异常崩溃或入口故障）
                if proc.returncode not in (0, None) and not result.timed_out:
                    tail = "\n".join(result.logs[-5:])
                    result.error = f"sandbox exited rc={proc.returncode}" + (f"\n{tail}" if tail else "")

            # 防伪造：超时或非零退出 → 无条件失败（即使沙箱抢先发了 ok=true 的 result 帧）
            if result.timed_out or proc.returncode not in (0, None):
                result.ok = False
        finally:
            if proc.returncode is None:
                _kill_process_group(proc)
                try:
                    await asyncio.wait_for(proc.wait(), timeout=3)
                except (TimeoutError, ProcessLookupError):
                    pass
            # 正常路径先给读取任务一个收尾窗口：stdout/stderr EOF 会自然结束，
            # asyncio subprocess transport 随之关闭（避免 loop 关闭后 __del__ 告警）。
            for t in (pump_err, ctrl):
                try:
                    await asyncio.wait_for(asyncio.shield(t), timeout=1.0)
                except (TimeoutError, asyncio.CancelledError):
                    t.cancel()
            await asyncio.gather(pump_err, ctrl, return_exceptions=True)
            if proc.stdin is not None:
                try:
                    proc.stdin.close()
                    await proc.stdin.wait_closed()
                except (BrokenPipeError, ConnectionResetError):
                    pass
            # 给 asyncio 子进程 transport 一个事件循环周期完成管道收尾，
            # 避免测试线程在 loop 关闭后由 __del__ 触发 unraisable warning。
            await asyncio.sleep(0)
            shutil.rmtree(scratch, ignore_errors=True)

        result.vars = {**merged_vars, **result.vars}
        result.duration_ms = int((time.perf_counter() - started) * 1000)
        return result

    async def _handle_op(self, msg: dict, respond, payload: dict, merged_vars: dict):
        try:
            call_id = int(msg.get("id", 0))
        except (TypeError, ValueError):
            return  # 坏帧：不响应（读线程已有防御）
        op = msg.get("op")
        args = msg.get("args") or {}
        try:
            if op == "http_request":
                value = await self.http_handler(args)
            elif op == "set_var":
                merged_vars[args["key"]] = args.get("value")
                value = True
            elif op == "get_var":
                key = args.get("key", "")
                if key in merged_vars:
                    value = merged_vars[key]
                else:
                    value = (payload.get("vars") or {}).get(key)
            elif op == "api_request":
                if self.api_handler is None:
                    raise ValueError("api_request not available in this sandbox context")
                value = await self.api_handler(args, merged_vars, payload)
            elif op == "grpc_request":
                if self.grpc_handler is None:
                    raise ValueError("grpc_request not available in this sandbox context")
                value = await self.grpc_handler(args)
            elif op in self.extra_ops:
                value = await self.extra_ops[op](args)
            else:
                raise ValueError(f"unknown bridge op: {op}")
            await respond(call_id, True, value)
        except Exception as e:
            await respond(call_id, False, f"{type(e).__name__}: {e}")


def env_auto_headers(env: Any) -> dict[str, str]:
    """项目/环境级 HEADER 类变量（非敏感）→ 自动注入头集合。"""
    out: dict[str, str] = {}
    for v in env.variables:
        if not v.sensitive and v.category == 1:  # VARIABLE_CATEGORY_HEADER
            out[v.key] = v.value
    return out


async def bridge_http_handler(client: httpx.AsyncClient, base_url: str,
                              args: dict[str, Any],
                              auto_headers: dict[str, str] | None = None) -> dict[str, Any]:
    """能力桥 http_request 的 Worker 侧实现（引擎 httpx 客户端复用）。
    auto_headers = HEADER 类环境变量默认注入（SDK 显式传的同名头优先，忽略大小写）。"""
    uri = str(args.get("uri") or "")
    if not (uri.startswith("http://") or uri.startswith("https://")):
        uri = base_url.rstrip("/") + "/" + uri.lstrip("/")
    await egress.acheck_url(uri)
    body = args.get("body")
    headers = {str(k).lower(): v for k, v in (args.get("headers") or {}).items()}
    if auto_headers:
        for k, v in auto_headers.items():
            headers.setdefault(k.lower(), v)  # SDK 显式头优先
    kwargs: dict[str, Any] = {
        "method": str(args.get("method") or "GET"),
        "url": uri,
        "params": args.get("params") or None,
        "headers": headers or None,
        "timeout": min(float(args.get("timeout") or 30), 120),
        "follow_redirects": False,  # 桥路径不跟随重定向（与声明式逐跳校验不同，避免 SSRF 绕行）
    }
    if body is not None:
        raw = body if isinstance(body, str) else json.dumps(body, ensure_ascii=False)
        if len(raw.encode()) > _BRIDGE_REQ_BODY_LIMIT:
            raise ValueError("bridge request body too large")
        kwargs["content"] = raw
        if not isinstance(body, str):
            kwargs.setdefault("headers", {})
            (kwargs["headers"] or {}).setdefault("Content-Type", "application/json")
    started = time.perf_counter()
    # 流式限读（httpx 先全量下载再截断会 OOM）；header 条目设上限
    from .http_exec import request_limited
    resp, raw = await request_limited(client, kwargs, _BRIDGE_RESP_LIMIT)
    elapsed = int((time.perf_counter() - started) * 1000)
    text = raw.decode(resp.encoding or "utf-8", "replace")
    resp_headers = dict(list(resp.headers.items())[:_MAX_RESP_HEADERS])
    try:
        parsed: Any = json.loads(text)
    except ValueError:
        parsed = None
    return {
        "status": resp.status_code,
        "headers": resp_headers,
        "body": parsed if parsed is not None else text,
        "text": text,
        "elapsed_ms": elapsed,
    }
