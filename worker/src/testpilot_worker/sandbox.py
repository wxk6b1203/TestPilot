"""低代码沙箱：subprocess 后端 + 能力桥服务端。

隔离模型（docs/design.md §6.3）：
- subprocess 后端：独立进程 + setrlimit + env scrub + scratch 目录 + 超时强杀
- 能力桥：沙箱内 SDK 是瘦客户端，HTTP/变量等副作用经控制通道（stdin/stdout
  JSON Lines）转发给 Worker 执行 —— 沙箱进程无网络出口（sandbox-exec/bwrap
  可用时强制）、无 Worker 凭据（env scrub）、无明文密钥；用户 print 重定向到
  stderr（日志通道），不污染协议帧
- ExecutionBackend 抽象：后续可切 container(gVisor)/dedicated 后端
"""

from __future__ import annotations

import asyncio
import inspect
import json
import os
import platform
import shutil
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

# 能力桥通道上限
_BRIDGE_RESP_LIMIT = 256 * 1024
_BRIDGE_REQ_BODY_LIMIT = 1 * 1024 * 1024


@dataclass
class SandboxLimits:
    cpu_seconds: int = int(os.environ.get("TP_SANDBOX_CPU", "30"))
    mem_mb: int = int(os.environ.get("TP_SANDBOX_MEM_MB", "1024"))
    max_procs: int = int(os.environ.get("TP_SANDBOX_NPROC", "128"))
    max_fds: int = int(os.environ.get("TP_SANDBOX_NOFILE", "128"))
    max_fsize_mb: int = int(os.environ.get("TP_SANDBOX_FSIZE_MB", "32"))
    net_deny: bool = os.environ.get("TP_SANDBOX_NET", "deny") != "allow"


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


def _apply_rlimits(limits: SandboxLimits) -> None:
    """子进程 preexec：资源约束（逐项尽力而为，平台不支持的跳过）。"""
    import resource

    def _try(what: int, soft: int, hard: int | None = None):
        try:
            resource.setrlimit(what, (soft, hard if hard is not None else soft))
        except (ValueError, OSError):
            pass

    _try(resource.RLIMIT_CPU, limits.cpu_seconds, limits.cpu_seconds + 5)
    _try(resource.RLIMIT_AS, limits.mem_mb * 1024 * 1024)
    if hasattr(resource, "RLIMIT_NPROC"):
        _try(resource.RLIMIT_NPROC, limits.max_procs)
    _try(resource.RLIMIT_NOFILE, limits.max_fds)
    _try(resource.RLIMIT_FSIZE, limits.max_fsize_mb * 1024 * 1024)


def _scrub_env(scratch: str, sdk_root: str, payload_path: str) -> dict[str, str]:
    """环境白名单：不继承 Worker 的任何变量/凭据。"""
    return {
        "PATH": os.environ.get("PATH", "/usr/bin:/bin"),
        "HOME": scratch,
        "TMPDIR": scratch,
        "LANG": "en_US.UTF-8",
        "PYTHONPATH": os.pathsep.join([sdk_root, scratch]),
        "PYTHONNOUSERSITE": "1",
        "PYTHONHASHSEED": "0",
        "TP_PAYLOAD": payload_path,
    }


_NET_DENY_PROFILE = """(version 1)
(allow default)
(deny network-outbound)
(deny network-inbound)
(deny network-bind)
"""


def _net_deny_wrapper(cmd: list[str], scratch: str) -> list[str]:
    """OS 级网络隔离（尽力而为）：macOS sandbox-exec / Linux bubblewrap。"""
    if platform.system() == "Darwin" and shutil.which("sandbox-exec"):
        profile = os.path.join(scratch, "sandbox.sb")
        Path(profile).write_text(_NET_DENY_PROFILE)
        return ["sandbox-exec", "-f", profile, *cmd]
    if platform.system() == "Linux" and shutil.which("bwrap"):
        return ["bwrap", "--unshare-net", "--dev", "/dev", "--", *cmd]
    log.warning("no OS sandbox tool found (sandbox-exec/bwrap); "
                "sandbox runs WITHOUT network denial — 建议容器后端")
    return cmd


class SubprocessBackend(ExecutionBackend):
    def __init__(self, http_handler: HttpHandler, limits: SandboxLimits | None = None,
                 extra_ops: dict[str, OpHandler] | None = None):
        self.http_handler = http_handler
        self.limits = limits or SandboxLimits()
        self.extra_ops = extra_ops or {}  # 扩展桥操作（如 ui_action → bridge_ui_handler）
        # 循环模式（行为压测）：每个迭代结束的回调（可 async），在控制循环协程内直接调用
        self._loop_cb: Callable[[dict], Any] | None = None

    def set_loop_callback(self, cb: Callable[[dict], Any] | None) -> None:
        """循环模式迭代回调（entry 的 iteration event → cb(msg)）。"""
        self._loop_cb = cb

    async def run(self, source: str, entry: str, payload: dict[str, Any],
                  timeout_s: float, loop: bool = False) -> SandboxResult:
        started = time.perf_counter()
        result = SandboxResult(ok=False)
        scratch = tempfile.mkdtemp(prefix="tp-sandbox-")
        # sdk_root = worker/src（包含 testpilot_sdk 包）
        sdk_root = str(Path(__file__).resolve().parent.parent)
        src_path = os.path.join(scratch, "user_case.py")
        payload_path = os.path.join(scratch, "payload.json")
        Path(src_path).write_text(source, encoding="utf-8")
        if loop:
            payload["loop"] = True  # entry 循环模式：迭代门控 + iteration 事件流
        Path(payload_path).write_text(json.dumps(payload, ensure_ascii=False), encoding="utf-8")

        cmd = [sys.executable, "-m", "testpilot_sdk.entry", src_path, entry]
        if self.limits.net_deny:
            cmd = _net_deny_wrapper(cmd, scratch)

        def _preexec():
            _apply_rlimits(self.limits)

        try:
            proc = await asyncio.create_subprocess_exec(
                *cmd,
                stdin=asyncio.subprocess.PIPE,   # Worker → 沙箱：桥响应
                stdout=asyncio.subprocess.PIPE,  # 沙箱 → Worker：桥请求/事件
                stderr=asyncio.subprocess.PIPE,  # 用户日志（print 已重定向到这里）
                cwd=scratch,
                env=_scrub_env(scratch, sdk_root, payload_path),
                preexec_fn=_preexec,
            )
        except Exception as e:
            result.error = f"sandbox spawn failed: {e}"
            shutil.rmtree(scratch, ignore_errors=True)
            return result

        done = asyncio.Event()
        merged_vars: dict[str, Any] = {}

        async def _respond(call_id: int, ok: bool, value: Any):
            msg = {"id": call_id, "ok": ok, "result" if ok else "error": value}
            try:
                proc.stdin.write(json.dumps(msg, ensure_ascii=False).encode() + b"\n")
                await proc.stdin.drain()
            except (BrokenPipeError, ConnectionResetError):
                done.set()

        async def _control_loop():
            while True:
                line = await proc.stdout.readline()
                if not line:
                    break
                try:
                    msg = json.loads(line)
                except ValueError:
                    continue
                mtype = msg.get("type")
                if mtype == "log":
                    result.logs.append(str(msg.get("message", ""))[:2000])
                elif mtype == "result":
                    result.ok = bool(msg.get("ok"))
                    result.error = str(msg.get("error", ""))[:4000]
                    result.vars = msg.get("vars") or {}
                    result.assertions = msg.get("assertions") or []
                    done.set()
                elif mtype == "op":
                    asyncio.create_task(self._handle_op(msg, _respond, payload, merged_vars))
                elif mtype == "event" and msg.get("name") == "iteration" and self._loop_cb:
                    res = self._loop_cb(msg)
                    if inspect.isawaitable(res):
                        await res

        async def _drain(stream):
            while True:
                line = await stream.readline()
                if not line:
                    break
                result.logs.append(line.decode("utf-8", "replace").rstrip()[:2000])

        pump_err = asyncio.create_task(_drain(proc.stderr))
        ctrl = asyncio.create_task(_control_loop())

        try:
            await asyncio.wait_for(done.wait(), timeout=max(timeout_s, 1))
        except TimeoutError:
            result.timed_out = True
            result.error = f"sandbox timeout after {timeout_s}s (killed)"
            try:
                proc.kill()
            except ProcessLookupError:
                pass
        await proc.wait()
        for t in (pump_err, ctrl):
            t.cancel()
        await asyncio.gather(pump_err, ctrl, return_exceptions=True)

        if not done.is_set() and not result.error:
            # 进程结束但没发 result（脚本异常崩溃或入口故障）
            if proc.returncode not in (0, None) and not result.timed_out:
                tail = "\n".join(result.logs[-5:])
                result.error = f"sandbox exited rc={proc.returncode}" + (f"\n{tail}" if tail else "")

        result.vars = {**merged_vars, **result.vars}
        result.duration_ms = int((time.perf_counter() - started) * 1000)
        shutil.rmtree(scratch, ignore_errors=True)
        return result

    async def _handle_op(self, msg: dict, respond, payload: dict, merged_vars: dict):
        call_id = int(msg.get("id", 0))
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
            elif op in self.extra_ops:
                value = await self.extra_ops[op](args)
            else:
                raise ValueError(f"unknown bridge op: {op}")
            await respond(call_id, True, value)
        except Exception as e:
            await respond(call_id, False, f"{type(e).__name__}: {e}")


async def bridge_http_handler(client: httpx.AsyncClient, base_url: str,
                              args: dict[str, Any]) -> dict[str, Any]:
    """能力桥 http_request 的 Worker 侧实现（引擎 httpx 客户端复用）。"""
    uri = str(args.get("uri") or "")
    if not (uri.startswith("http://") or uri.startswith("https://")):
        uri = base_url.rstrip("/") + "/" + uri.lstrip("/")
    egress.check_url(uri)
    body = args.get("body")
    kwargs: dict[str, Any] = {
        "method": str(args.get("method") or "GET"),
        "url": uri,
        "params": args.get("params") or None,
        "headers": args.get("headers") or None,
        "timeout": min(float(args.get("timeout") or 30), 120),
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
    resp = await client.request(**kwargs)
    elapsed = int((time.perf_counter() - started) * 1000)
    text = resp.content[:_BRIDGE_RESP_LIMIT].decode(resp.encoding or "utf-8", "replace")
    try:
        parsed: Any = json.loads(text)
    except ValueError:
        parsed = None
    return {
        "status": resp.status_code,
        "headers": dict(resp.headers),
        "body": parsed if parsed is not None else text,
        "text": text,
        "elapsed_ms": elapsed,
    }
