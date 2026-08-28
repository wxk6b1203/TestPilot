"""UI 探测命令执行端（v1，docs/ui-probe-design.md §4.3）。

Scheduler 经既有 Worker 命令流下发 ProbeCommand，本模块执行并回 ProbeReply。
浏览器层完全复用 ui.UiSession（record=False：探测高频开关会话，不录 HAR/tracing）。

护栏宁紧勿松（防 DoS，勿随意放宽）：
- 每 Worker ACTIVE 探测会话 ≤ TP_PROBE_MAX_SESSIONS（config.probe_max_sessions，默认 2）
- 单命令硬超时 PROBE_HARD_TIMEOUT=90s（取 min(cmd.timeout, 硬顶)）
- 快照体积绝对上限 32KB / eval 结果绝对上限 8KB（与命令携带的 Scheduler 权威值取 min）
- 会话归属二次校验（tenant_id 不符按不存在处理，不泄漏会话存在性）
- ref 子树聚焦为 v1.x 增强，v1 仅支持全页快照（契约已预留字段）
"""

from __future__ import annotations

import asyncio
import json
import logging
import tempfile
import time
from collections import deque
from dataclasses import dataclass, field
from pathlib import Path
from urllib.parse import urlsplit
from typing import Any, Awaitable, Callable

from testpilot.common.v1 import types_pb2 as pb
from testpilot.worker.v1 import worker_pb2 as wpb

from . import ui
from .sandbox import (
    SandboxLimits, _LineReader, _kill_process_group, _net_deny_wrapper,
    _probe_sandbox_exec, _scrub_env, limits_env_json,
)

log = logging.getLogger("testpilot.worker.probe")

# 硬顶（防 DoS 常量，勿随意放宽；Scheduler 侧权威值只会更低）
PROBE_HARD_TIMEOUT = 90.0        # 单命令执行硬顶（秒）
EVAL_HARD_TIMEOUT = 30.0         # eval_js 执行硬顶（秒）
SNAPSHOT_ABS_MAX = 32 * 1024     # 快照体积绝对上限（字节）
EVAL_ABS_MAX = 8 * 1024          # eval 结果体积绝对上限（字节）
RUN_ABS_MAX = 32 * 1024          # run_py 源码绝对上限（字节；Scheduler 权威 16KB）
RUN_MIN_INTERVAL = 2.0           # 每会话 run 频率闸（秒，防死循环刷流）
RUN_MAX_LOG_LINES = 200          # 单次 exec 捕获 print 行数上限（entry 侧同值）

# 失败码（与 Scheduler docs/error-codes.md 的 PROBE_* 对齐）
CODE_SESSION_NOT_FOUND = "PROBE_SESSION_NOT_FOUND"
CODE_LIMIT = "PROBE_LIMIT"
CODE_TIMEOUT = "PROBE_TIMEOUT"
CODE_FAILED = "PROBE_FAILED"

# 探测会话允许的 UI 动作白名单（排除 screenshot/download 等产物型动作：
# 探测的感知通道是快照与 eval，产物落盘需求尚未出现，先收紧；
# proto 无 UNCHECK —— 取消勾选是 UI_ACTION_CHECK + value=false 语义，与声明式引擎一致）
_ACT_WHITELIST = frozenset({
    pb.UI_ACTION_GOTO, pb.UI_ACTION_CLICK, pb.UI_ACTION_FILL,
    pb.UI_ACTION_SELECT, pb.UI_ACTION_CHECK,
    pb.UI_ACTION_HOVER, pb.UI_ACTION_PRESS, pb.UI_ACTION_WAIT,
})

SessionFactory = Callable[[str, str, bool], ui.UiSession]  # (session_id, base_url, record) -> UiSession


def _origin_of(url: str) -> str:
    parts = urlsplit(url)
    if not parts.scheme or not parts.netloc:
        raise ValueError(f"probe url 必须是绝对地址（Scheduler 已解析 base_url）: {url!r}")
    return f"{parts.scheme}://{parts.netloc}"


def _truncate(text: str, max_bytes: int) -> tuple[str, bool]:
    """按 UTF-8 字节预算截断，尽量在行边界切断并追加说明尾注。"""
    raw = text.encode("utf-8")
    if len(raw) <= max_bytes:
        return text, False
    budget = max(max_bytes - 128, 0)  # 留出尾注空间（中文尾注约 90 字节）
    cut = raw[:budget]
    # 回退到最后一个完整行边界（快照为 YAML 文本，行边界切断最可读）
    nl = cut.rfind(b"\n")
    if nl > 0:
        cut = cut[:nl]
    head = cut.decode("utf-8", errors="ignore")
    return head + f"\n… [已截断：快照超过 {max_bytes} 字节；可用 ref 参数取子树（v1.x）]", True


@dataclass
class _Session:
    id: str
    tenant_id: int
    base_url: str
    created_at: float = field(default_factory=time.time)
    last_active: float = field(default_factory=time.time)
    last_run: float = 0.0            # v2 run_py 频率闸
    ui: ui.UiSession | None = None   # 惰性创建（首次真正触页时启动浏览器）

    def touch(self):
        self.last_active = time.time()


class ProbeHub:
    """Worker 侧探测会话注册表 + ProbeCommand 执行。

    session_factory 可注入（测试用 stub UiSession）；默认用真实 ui.UiSession，
    产物目录隔离在 {artifact_root}/probe/{session_id}/。
    """

    def __init__(self, artifact_root: Path | None = None,
                 max_sessions: int = 2,
                 session_factory: SessionFactory | None = None,
                 sandbox_factory: Callable[[dict], "ProbeSandbox"] | None = None):
        self._artifact_root = artifact_root or (ui.artifact_root() / "probe")
        self._max_sessions = max(1, int(max_sessions))
        self._sessions: dict[str, _Session] = {}
        self._sandboxes: dict[str, "ProbeSandbox"] = {}
        self._factory: SessionFactory = session_factory or self._default_factory
        self._sandbox_factory = sandbox_factory or (lambda ops: ProbeSandbox(extra_ops=ops))

    @staticmethod
    def _default_factory(session_id: str, base_url: str, record: bool) -> ui.UiSession:
        case_rel = f"probe/{session_id}"
        return ui.UiSession(
            base_url=base_url,
            case_dir=ui.artifact_root() / case_rel,
            case_rel=case_rel,
            render=lambda s: s,   # 探测不做 {{vars}} 渲染（用例运行期语义）
            record=record,
        )

    # ---- 入口：执行一条 ProbeCommand，恒返回 ProbeReply（失败也回执） ----

    async def handle(self, cmd: wpb.ProbeCommand) -> wpb.WorkerEvent:
        reply = await self._handle(cmd)
        return wpb.WorkerEvent(probe_reply=reply)

    async def _handle(self, cmd: wpb.ProbeCommand) -> wpb.ProbeReply:
        op = cmd.WhichOneof("op")
        timeout_s = max(cmd.timeout.ToSeconds() or 0, 0)
        try:
            return await asyncio.wait_for(self._dispatch(cmd, op),
                                          timeout=min(timeout_s or PROBE_HARD_TIMEOUT, PROBE_HARD_TIMEOUT))
        except asyncio.TimeoutError:
            log.warning("probe command timeout: session=%s op=%s", cmd.session_id, op)
            return self._failure(cmd, CODE_TIMEOUT, f"probe command timeout after {timeout_s or PROBE_HARD_TIMEOUT:g}s")
        except Exception as e:  # 防御：任何未预期错误都必须回执（Scheduler 侧 pending 依赖回执或超时）
            log.exception("probe command failed: session=%s op=%s", cmd.session_id, op)
            return self._failure(cmd, CODE_FAILED, f"{type(e).__name__}: {e}")

    async def _dispatch(self, cmd: wpb.ProbeCommand, op: str | None) -> wpb.ProbeReply:
        if op is None:
            return self._failure(cmd, CODE_FAILED, "probe command: no op set")
        if op == "close":
            return await self._close(cmd)
        if op == "open":
            return await self._open(cmd)   # open 自行 create-or-get（新会话不经 lookup）

        sess = self._lookup(cmd)
        if sess is None:
            return self._failure(cmd, CODE_SESSION_NOT_FOUND,
                                 f"probe session {cmd.session_id!r} not found (or tenant mismatch)")
        if op == "act":
            return await self._act(cmd, sess)
        if op == "snapshot":
            return await self._snapshot(cmd, sess)
        if op == "eval":
            return await self._eval(cmd, sess)
        if op == "run":
            return await self._run(cmd, sess)
        return self._failure(cmd, CODE_FAILED, f"unknown probe op: {op!r}")

    # ---- 各操作 ----

    def _lookup(self, cmd: wpb.ProbeCommand) -> _Session | None:
        s = self._sessions.get(cmd.session_id)
        # 归属二次校验：租户不符按不存在处理（不泄漏会话存在性）
        if s is None or (cmd.tenant_id and s.tenant_id != cmd.tenant_id):
            return None
        s.touch()
        return s

    async def _open(self, cmd: wpb.ProbeCommand) -> wpb.ProbeReply:
        s = self._sessions.get(cmd.session_id)
        if s is not None and cmd.tenant_id and s.tenant_id != cmd.tenant_id:
            return self._failure(cmd, CODE_SESSION_NOT_FOUND,
                                 f"probe session {cmd.session_id!r} not found (or tenant mismatch)")
        if s is None:
            if len(self._sessions) >= self._max_sessions:
                return self._failure(cmd, CODE_LIMIT,
                                     f"probe sessions at cap ({self._max_sessions}); close one first")
            s = _Session(id=cmd.session_id, tenant_id=cmd.tenant_id,
                         base_url=_origin_of(cmd.open.url))
            self._sessions[cmd.session_id] = s
        s.touch()
        s.base_url = _origin_of(cmd.open.url)
        if s.ui is None:
            s.ui = self._factory(cmd.session_id, s.base_url, cmd.open.record)
        await s.ui.ensure()
        logs: list[str] = []
        await s.ui.execute(pb.UI_ACTION_GOTO, cmd.open.url, "", logs)
        return await self._state_reply(cmd, s, cmd.open.snapshot_max_bytes)

    async def _act(self, cmd: wpb.ProbeCommand, sess: _Session) -> wpb.ProbeReply:
        if sess.ui is None:
            return self._failure(cmd, CODE_SESSION_NOT_FOUND, "probe session has no browser (open first)")
        act = cmd.act
        if act.action not in _ACT_WHITELIST:
            return self._failure(cmd, CODE_FAILED,
                                 f"probe action {pb.UiAction.Name(act.action) if act.action else 'UNSPECIFIED'} not allowed")
        logs: list[str] = []
        await sess.ui.execute(act.action, act.target, act.value, logs)
        # act 命令不携带快照上限，用绝对顶（Scheduler 权威截断在 RPC 响应层另做）
        return await self._state_reply(cmd, sess, SNAPSHOT_ABS_MAX)

    async def _snapshot(self, cmd: wpb.ProbeCommand, sess: _Session) -> wpb.ProbeReply:
        if sess.ui is None or sess.ui.page is None:
            return self._failure(cmd, CODE_SESSION_NOT_FOUND, "probe session has no page")
        if cmd.snapshot.ref:
            return self._failure(cmd, CODE_FAILED, "ref 子树聚焦为 v1.x 增强，当前仅支持全页快照")
        return await self._state_reply(cmd, sess, cmd.snapshot.snapshot_max_bytes)

    async def _eval(self, cmd: wpb.ProbeCommand, sess: _Session) -> wpb.ProbeReply:
        if sess.ui is None or sess.ui.page is None:
            return self._failure(cmd, CODE_SESSION_NOT_FOUND, "probe session has no page")
        max_bytes = min(max(cmd.eval.result_max_bytes, 0) or EVAL_ABS_MAX, EVAL_ABS_MAX)
        try:
            val = await asyncio.wait_for(sess.ui.page.evaluate(cmd.eval.expression),
                                         timeout=EVAL_HARD_TIMEOUT)
        except asyncio.TimeoutError:
            return self._failure(cmd, CODE_TIMEOUT, f"page.evaluate timeout after {EVAL_HARD_TIMEOUT:g}s")
        out = json.dumps(val, ensure_ascii=False, default=str)
        out, truncated = _truncate(out, max_bytes)
        return wpb.ProbeReply(request_id=cmd.request_id, session_id=cmd.session_id,
                              eval=wpb.ProbeEvalResult(result_json=out, result_truncated=truncated))

    async def _close(self, cmd: wpb.ProbeCommand) -> wpb.ProbeReply:
        sess = self._sessions.pop(cmd.session_id, None)
        if sess is None or (cmd.tenant_id and sess.tenant_id != cmd.tenant_id):
            # 关闭不存在的会话视为幂等成功（close 语义：确保已释放）
            return wpb.ProbeReply(request_id=cmd.request_id, session_id=cmd.session_id,
                                  ack=wpb.ProbeAck(session_id=cmd.session_id))
        if sess.ui is not None:
            try:
                await sess.ui.finish()
            except Exception:
                log.exception("probe session finish failed: %s", sess.id)
        sb = self._sandboxes.pop(sess.id, None)
        if sb is not None:
            await sb.close()
        return wpb.ProbeReply(request_id=cmd.request_id, session_id=cmd.session_id,
                              ack=wpb.ProbeAck(session_id=cmd.session_id))

    # ---- 快照与回执构造 ----

    async def _state_reply(self, cmd: wpb.ProbeCommand, sess: _Session, max_bytes: int) -> wpb.ProbeReply:
        """快照回执（open/act 后自动回快照，一步一反馈）。page 未就绪时返回失败。"""
        page = sess.ui.page if sess.ui else None
        if page is None:
            return self._failure(cmd, CODE_FAILED, "browser page unavailable after action")
        return wpb.ProbeReply(request_id=cmd.request_id, session_id=cmd.session_id,
                              state=await self._snapshot_state(page, max_bytes))

    @staticmethod
    async def _snapshot_state(page: Any, max_bytes: int) -> wpb.ProbeState:
        """ARIA 快照（async Playwright：aria_snapshot/title 均为协程）。
        快照异常时降级为仅 URL/title，不失败（探测的感知通道宁全勿断）。"""
        snap, truncated = "", False
        try:
            snap = await page.locator("html").aria_snapshot() or ""
            snap, truncated = _truncate(snap, min(max_bytes, SNAPSHOT_ABS_MAX) or SNAPSHOT_ABS_MAX)
        except Exception as e:
            log.warning("aria_snapshot failed: %s", e)
        title = ""
        try:
            title = await page.title()
        except Exception:
            pass
        return wpb.ProbeState(final_url=str(getattr(page, "url", "") or ""),
                              title=title, aria_snapshot=snap, snapshot_truncated=truncated)

    @staticmethod
    def _failure(cmd: wpb.ProbeCommand, code: str, message: str) -> wpb.ProbeReply:
        return wpb.ProbeReply(request_id=cmd.request_id, session_id=cmd.session_id,
                              failure=wpb.ProbeFailure(code=code, message=message))


    # ---- v2 run_py：常驻沙箱（用户代码永不在 Worker 进程内执行）----

    async def _run(self, cmd: wpb.ProbeCommand, sess: _Session) -> wpb.ProbeReply:
        src = cmd.run.source
        if not src.strip():
            return self._failure(cmd, CODE_FAILED, "probe run: source is required")
        if len(src.encode("utf-8")) > RUN_ABS_MAX:
            return self._failure(cmd, CODE_LIMIT,
                                 f"probe run source exceeds {RUN_ABS_MAX} bytes")
        now = time.time()
        if now - sess.last_run < RUN_MIN_INTERVAL:
            return self._failure(cmd, CODE_LIMIT,
                                 f"probe run rate limited ({RUN_MIN_INTERVAL:g}s per session)")
        sess.last_run = now

        sb = self._sandboxes.get(sess.id)
        if sb is None:
            sb = self._sandbox_factory({
                "ui_action": ui.bridge_ui_handler(lambda: sess.ui),
                "ui_call": bridge_ui_call_handler(lambda: sess.ui),
            })
            self._sandboxes[sess.id] = sb

        # 沙箱 exec 超时略小于命令 pending 上限：保证超时错误以回执形式返回
        cmd_timeout = cmd.timeout.ToSeconds() or PROBE_HARD_TIMEOUT
        exec_timeout = max(min(cmd_timeout - 5, PROBE_HARD_TIMEOUT - 5), 10)
        out = await sb.exec(src, timeout_s=exec_timeout)
        if not out.get("ok"):
            code = CODE_TIMEOUT if "timeout" in str(out.get("error", "")) else CODE_FAILED
            return self._failure(cmd, code, str(out.get("error", "probe run failed")))

        # result_max_bytes 缺省回退 8KB（0 会让 _truncate 只剩尾注）
        repr_text, truncated = _truncate(str(out.get("repr", "")),
                                         cmd.run.result_max_bytes or EVAL_ABS_MAX)
        logs = [ln[:2000] for ln in out.get("logs", [])][:RUN_MAX_LOG_LINES]
        return wpb.ProbeReply(request_id=cmd.request_id, session_id=cmd.session_id,
                              run_result=wpb.ProbeRunResult(repr=repr_text, logs=logs,
                                                            truncated=truncated))


# ---------------------------------------------------------------------------
# v2 run_py：通用页面调用桥 + 会话常驻沙箱
# ---------------------------------------------------------------------------

# ui_call 白名单：method → 参数个数（-1 = 可变；动作类操作走既有 ui_action op）
_UI_CALL_METHODS: dict[str, int] = {
    "evaluate": 1, "content": 0, "title": 0, "url": 0,
    "wait_for_selector": 2, "aria_snapshot": 0,
}


def _json_safe(v: Any) -> Any:
    """桥结果必须 JSON 可序列化；不可序列化降级为 repr。"""
    try:
        json.dumps(v)
        return v
    except (TypeError, ValueError):
        return str(v)


def bridge_ui_call_handler(get_session: Callable[[], ui.UiSession | None]) -> Callable[[dict], Awaitable[dict]]:
    """op=ui_call 桥处理器：白名单方法转发 Playwright page（v2 run_py 逃生舱）。

    JS 运行在被测页面 origin 内，等价页面自身脚本能力，不触及 Worker 凭据；
    安全边界=白名单 + 审批 + 审计 + 体积/超时护栏。"""

    async def handle(args: dict) -> dict:
        method = str(args.get("method") or "")
        if method not in _UI_CALL_METHODS:
            raise ValueError(f"ui_call method not allowed: {method!r}")
        arrgs = list(args.get("args") or [])
        arity = _UI_CALL_METHODS[method]
        if arity >= 0 and len(arrgs) != arity:
            raise ValueError(f"ui_call {method}: expected {arity} args, got {len(arrgs)}")
        sess = get_session()
        if sess is None:
            raise ValueError("probe session has no browser (open first)")
        await sess.ensure()
        page = sess.page
        if method == "url":
            result: Any = page.url
        else:
            result = await getattr(page, method)(*arrgs)
        return {"ok": True, "result": _json_safe(result)}

    return handle


class ProbeSandbox:
    """会话常驻沙箱（v2 run_py）：spawn `testpilot_sdk.entry --mode repl`。

    复用 lowcode 沙箱加固件（rlimit/env scrub/净隔离/进程组强杀/行长上限），
    用户代码永不进 Worker 进程。单 exec 超时=整进程组强杀（Python 无法打断
    任意用户代码），下次 exec 自动重启；浏览器在 UiSession 不受影响。
    """

    def __init__(self, extra_ops: dict[str, Callable[[dict], Awaitable[dict]]],
                 limits: SandboxLimits | None = None):
        self._ops = extra_ops
        self._limits = limits or SandboxLimits()
        self._proc: asyncio.subprocess.Process | None = None
        self._scratch: str = ""
        self._seq = 0
        self._pending: dict[int, asyncio.Future] = {}
        self._crashed = False
        self._stderr_tail: deque[str] = deque(maxlen=50)
        self._control_task: asyncio.Task | None = None

    async def _ensure(self) -> None:
        if self._proc is not None and self._proc.returncode is None and not self._crashed:
            return
        await self._spawn()

    async def _spawn(self) -> None:
        import os as _os
        import shutil
        import sys

        self._scratch = tempfile.mkdtemp(prefix="tp-probe-sandbox-")
        sdk_root = str(Path(__file__).resolve().parent.parent)  # worker/src
        payload_path = Path(self._scratch, "payload.json")
        payload_path.write_text("{}", encoding="utf-8")  # repl 模式不读；占位保持 env 契约
        cmd = [sys.executable, "-m", "testpilot_sdk.entry", "--mode", "repl"]
        if self._limits.net_deny:
            import platform
            if platform.system() == "Darwin" and shutil.which("sandbox-exec") \
                    and _sandbox_exec_ok_cached() is None:
                from .sandbox import _probe_sandbox_exec
                await _probe_sandbox_exec()
            cmd, isolated = _net_deny_wrapper(cmd, self._scratch)
            if not isolated and self._limits.require_isolation:
                raise RuntimeError(
                    "sandbox isolation required but no OS sandbox tool available")
        env = _scrub_env(self._scratch, sdk_root, str(payload_path),
                         limits_env_json(self._limits))
        self._proc = await asyncio.create_subprocess_exec(
            *cmd, stdin=asyncio.subprocess.PIPE, stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE, cwd=self._scratch, env=env,
            start_new_session=True)
        self._crashed = False
        self._control_task = asyncio.create_task(self._control_loop(self._proc))
        asyncio.create_task(self._drain_stderr(self._proc))

    async def _drain_stderr(self, proc) -> None:
        reader = _LineReader(proc.stderr, 1 << 20)
        while True:
            line = await reader.readline()
            if not line:
                break
            self._stderr_tail.append(line.decode("utf-8", "ignore")[:2000])

    async def _control_loop(self, proc) -> None:
        reader = _LineReader(proc.stdout, 1 << 20)
        while True:
            line = await reader.readline()
            if not line:
                break  # EOF：进程退出
            try:
                msg = json.loads(line)
            except ValueError:
                continue
            mtype = msg.get("type")
            if mtype == "done":
                fut = self._pending.pop(int(msg.get("id", -1)), None)
                if fut is not None and not fut.done():
                    fut.set_result(msg)
                continue
            if mtype == "op":
                call_id = msg.get("id")
                handler = self._ops.get(str(msg.get("op") or ""))
                try:
                    if handler is None:
                        raise ValueError(f"unknown bridge op: {msg.get('op')!r}")
                    result = await handler(msg.get("args") or {})
                    resp: dict[str, Any] = {"id": call_id, "ok": True, "result": result}
                except Exception as e:
                    resp = {"id": call_id, "ok": False, "error": f"{type(e).__name__}: {e}"}
                try:
                    proc.stdin.write(json.dumps(resp, ensure_ascii=False).encode() + b"\n")
                    await proc.stdin.drain()
                except (BrokenPipeError, ConnectionResetError, RuntimeError):
                    break
        # 崩溃/退出：唤醒全部在途 exec（以失败收场），标记 crashed
        self._crashed = True
        for fut in self._pending.values():
            if not fut.done():
                fut.set_result({"ok": False, "error": "probe sandbox crashed: " +
                                " | ".join(list(self._stderr_tail)[-3:])})
        self._pending.clear()

    async def exec(self, source: str, timeout_s: float) -> dict:
        """执行一段 Python（约定 async def run(ctx)）；返回 done 帧内容。"""
        await self._ensure()
        if self._proc is None or self._proc.stdin is None:
            return {"ok": False, "error": "probe sandbox unavailable"}
        self._seq += 1
        cid = self._seq
        fut: asyncio.Future = asyncio.get_running_loop().create_future()
        self._pending[cid] = fut
        try:
            self._proc.stdin.write(
                json.dumps({"type": "exec", "id": cid, "source": source},
                           ensure_ascii=False).encode() + b"\n")
            await self._proc.stdin.drain()
        except (BrokenPipeError, ConnectionResetError, RuntimeError) as e:
            self._pending.pop(cid, None)
            self._crashed = True
            return {"ok": False, "error": f"probe sandbox stdin broken: {e}"}
        try:
            msg = await asyncio.wait_for(fut, timeout=timeout_s)
        except asyncio.TimeoutError:
            self._pending.pop(cid, None)
            await self.kill()  # 强杀：Python 无法打断任意用户代码
            return {"ok": False,
                    "error": f"probe exec timeout after {timeout_s:g}s (sandbox restarted)"}
        msg.pop("id", None)
        return msg

    async def kill(self) -> None:
        proc = self._proc
        if proc is not None and proc.returncode is None:
            _kill_process_group(proc)
            try:  # 回收退出状态，避免 transport 泄漏告警
                # RuntimeError：跨事件循环时 wait 的 future 属于已关闭的旧循环（测试形态）
                await asyncio.wait_for(proc.wait(), timeout=5)
            except (asyncio.TimeoutError, RuntimeError):
                pass
        self._crashed = True

    async def close(self) -> None:
        await self.kill()
        if self._control_task is not None:
            self._control_task.cancel()
        import shutil
        if self._scratch:
            shutil.rmtree(self._scratch, ignore_errors=True)
        self._proc = None


def _sandbox_exec_ok_cached():
    """sandbox.py 的探测缓存读取（None=未探测；避免每个沙箱重复 5s 探测）。"""
    from . import sandbox as _sb
    return _sb._sandbox_exec_ok

