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
import time
from dataclasses import dataclass, field
from pathlib import Path
from urllib.parse import urlsplit
from typing import Any, Callable

from testpilot.common.v1 import types_pb2 as pb
from testpilot.worker.v1 import worker_pb2 as wpb

from . import ui

log = logging.getLogger("testpilot.worker.probe")

# 硬顶（防 DoS 常量，勿随意放宽；Scheduler 侧权威值只会更低）
PROBE_HARD_TIMEOUT = 90.0        # 单命令执行硬顶（秒）
EVAL_HARD_TIMEOUT = 30.0         # eval_js 执行硬顶（秒）
SNAPSHOT_ABS_MAX = 32 * 1024     # 快照体积绝对上限（字节）
EVAL_ABS_MAX = 8 * 1024          # eval 结果体积绝对上限（字节）

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
                 session_factory: SessionFactory | None = None):
        self._artifact_root = artifact_root or (ui.artifact_root() / "probe")
        self._max_sessions = max(1, int(max_sessions))
        self._sessions: dict[str, _Session] = {}
        self._factory: SessionFactory = session_factory or self._default_factory

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
