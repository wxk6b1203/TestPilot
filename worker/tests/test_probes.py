"""probes.py：UI 探测会话状态机 / 护栏 / 回执契约（无浏览器，stub UiSession + fake page）。"""

from __future__ import annotations

import asyncio

import pytest
from testpilot.common.v1 import types_pb2 as pb
from testpilot.worker.v1 import worker_pb2 as wpb

from testpilot_worker.probes import (
    CODE_FAILED, CODE_LIMIT, CODE_SESSION_NOT_FOUND, CODE_TIMEOUT,
    ProbeHub, SNAPSHOT_ABS_MAX,
)


# ---- 测试替身 -----------------------------------------------------------------

class _FakePage:
    """async Playwright page 最小替身：aria_snapshot/title/url/evaluate。"""

    def __init__(self, snapshot: str = "- button \"登录\"", title: str = "Test Page",
                 url: str = "https://aut.test/login", eval_result=None,
                 eval_fail: Exception | None = None, eval_delay: float = 0.0):
        self._snapshot, self._title, self.url = snapshot, title, url
        self._eval_result, self._eval_fail, self._eval_delay = eval_result, eval_fail, eval_delay

    def locator(self, _sel: str):
        return self

    async def aria_snapshot(self) -> str:
        return self._snapshot

    async def title(self) -> str:
        return self._title

    async def evaluate(self, expression: str):
        if self._eval_delay:
            await asyncio.sleep(self._eval_delay)
        if self._eval_fail is not None:
            raise self._eval_fail
        return self._eval_result


class _FakeUiSession:
    """无浏览器 UiSession：记录 ensure/goto/execute 调用，record 透传可断言。"""

    instances: list["_FakeUiSession"] = []

    def __init__(self, base_url: str, case_dir, case_rel: str, render, record: bool = True):
        self.base_url, self.case_dir, self.case_rel, self.render = base_url, case_dir, case_rel, render
        self.record = record
        self.page: _FakePage | None = None
        self.calls: list[tuple[int, str, str]] = []
        self.ensured = False
        self.finished = False
        _FakeUiSession.instances.append(self)

    async def ensure(self):
        self.ensured = True
        if self.page is None:
            self.page = _FakePage(url=self.base_url + "/login")

    async def execute(self, action: int, target: str, value: str, logs: list[str]):
        self.calls.append((action, target, value))
        logs.append(f"fake ui {action} {target}".rstrip())

    async def finish(self):
        self.finished = True


def _hub(max_sessions: int = 2) -> ProbeHub:
    _FakeUiSession.instances.clear()
    return ProbeHub(artifact_root=None, max_sessions=max_sessions,
                    session_factory=lambda sid, base, record: _FakeUiSession(base, None, f"probe/{sid}", lambda s: s, record))


def _cmd(session_id: str = "s1", tenant_id: int = 7, **op_kwargs) -> wpb.ProbeCommand:
    return wpb.ProbeCommand(request_id="req-1", session_id=session_id, tenant_id=tenant_id, **op_kwargs)


def _open_cmd(url: str = "https://aut.test/login", **kw) -> wpb.ProbeCommand:
    return _cmd(open=wpb.ProbeOpen(url=url, snapshot_max_bytes=SNAPSHOT_ABS_MAX), **kw)


def _reply_payload(cmd: wpb.ProbeCommand) -> wpb.ProbeReply:
    hub = _hub()
    return asyncio.run(hub.handle(cmd)).probe_reply


# ---- 会话生命周期 ---------------------------------------------------------------

def test_open_act_snapshot_close_lifecycle():
    hub = _hub()

    async def run():
        ev = await hub.handle(_open_cmd())
        assert ev.probe_reply.state.title == "Test Page"
        assert "登录" in ev.probe_reply.state.aria_snapshot
        assert ev.probe_reply.state.final_url.endswith("/login")

        act = wpb.ProbeCommand(request_id="req-2", session_id="s1", tenant_id=7,
                               act=pb.UiActionStep(action=pb.UI_ACTION_CLICK, target="#login-btn"))
        ev2 = await hub.handle(act)
        assert ev2.probe_reply.state.aria_snapshot  # act 后自动回快照（一步一反馈）

        snap = await hub.handle(_cmd(snapshot=wpb.ProbeSnapshot(snapshot_max_bytes=SNAPSHOT_ABS_MAX)))
        assert snap.probe_reply.state.aria_snapshot

        close = await hub.handle(_cmd(close=wpb.ProbeClose(reason="user")))
        assert close.probe_reply.WhichOneof("payload") == "ack"
        assert _FakeUiSession.instances[0].finished  # 浏览器确实被关闭

        # close 后再 act：会话已死
        gone = await hub.handle(act)
        assert gone.probe_reply.failure.code == CODE_SESSION_NOT_FOUND

    asyncio.run(run())


def test_open_unknown_tenant_on_existing_session_rejected():
    hub = _hub()

    async def run():
        p1 = (await hub.handle(_open_cmd(tenant_id=7))).probe_reply
        assert p1.WhichOneof("payload") == "state"
        p2 = (await hub.handle(_open_cmd(tenant_id=8))).probe_reply
        assert p2.failure.code == CODE_SESSION_NOT_FOUND  # 不泄漏会话存在性

    asyncio.run(run())


def test_act_before_open_rejected():
    act = wpb.ProbeCommand(request_id="r", session_id="nope", tenant_id=7,
                           act=pb.UiActionStep(action=pb.UI_ACTION_CLICK, target="#x"))
    assert _reply_payload(act).failure.code == CODE_SESSION_NOT_FOUND


def test_reopen_existing_session_reuses_browser():
    hub = _hub()

    async def run():
        await hub.handle(_open_cmd("https://aut.test/login"))
        n = len(_FakeUiSession.instances)
        await hub.handle(_open_cmd("https://aut.test/other"))
        assert len(_FakeUiSession.instances) == n  # 复用，不新建浏览器

    asyncio.run(run())


# ---- 护栏 -----------------------------------------------------------------------

def test_session_cap_enforced():
    hub = _hub(max_sessions=2)

    async def run():
        await hub.handle(_open_cmd(url="https://a.test/x"))
        await hub.handle(_cmd(session_id="s2", open=wpb.ProbeOpen(url="https://b.test/x")))
        third = await hub.handle(_cmd(session_id="s3", open=wpb.ProbeOpen(url="https://c.test/x")))
        assert third.probe_reply.failure.code == CODE_LIMIT

    asyncio.run(run())


def test_disallowed_action_rejected():
    hub = _hub()

    async def run():
        await hub.handle(_open_cmd())
        bad = wpb.ProbeCommand(request_id="r", session_id="s1", tenant_id=7,
                               act=pb.UiActionStep(action=pb.UI_ACTION_SCREENSHOT, target=""))
        rep = await hub.handle(bad)
        assert rep.probe_reply.failure.code == CODE_FAILED

    asyncio.run(run())


def test_snapshot_ref_not_supported_in_v1():
    hub = _hub()

    async def run():
        await hub.handle(_open_cmd())
        rep = await hub.handle(_cmd(snapshot=wpb.ProbeSnapshot(ref="main / form")))
        assert rep.probe_reply.failure.code == CODE_FAILED
        assert "v1.x" in rep.probe_reply.failure.message

    asyncio.run(run())


def test_unknown_op_rejected():
    hub = _hub()

    async def run():
        await hub.handle(_open_cmd())
        cmd = _cmd(eval=wpb.ProbeEval(expression="1+1"))
        cmd.ClearField("eval")  # 清空 oneof → no op
        rep = await hub.handle(cmd)
        assert rep.probe_reply.failure.code == CODE_FAILED

    asyncio.run(run())


# ---- 快照截断 -------------------------------------------------------------------

def test_snapshot_truncated_at_byte_budget():
    big = "\n".join(f'- text "行{i}一些中文内容让字节膨胀"' for i in range(400))
    hub = _hub()

    async def run():
        await hub.handle(_open_cmd())
        hub._sessions["s1"].ui.page = _FakePage(snapshot=big)
        small = 2048
        rep = await hub.handle(_cmd(snapshot=wpb.ProbeSnapshot(snapshot_max_bytes=small)))
        st = rep.probe_reply.state
        assert st.snapshot_truncated
        assert len(st.aria_snapshot.encode("utf-8")) <= small + 128  # 预算 + 尾注余量
        assert "已截断" in st.aria_snapshot

    asyncio.run(run())


# ---- eval_js --------------------------------------------------------------------

def test_eval_success_and_truncation():
    hub = _hub()

    async def run():
        await hub.handle(_open_cmd())
        hub._sessions["s1"].ui.page = _FakePage(eval_result=[1, 2, 3])
        rep = await hub.handle(_cmd(eval=wpb.ProbeEval(expression="() => [1,2,3]", result_max_bytes=0)))
        assert rep.probe_reply.eval.result_json == "[1, 2, 3]"
        assert not rep.probe_reply.eval.result_truncated

    asyncio.run(run())


def test_eval_exception_returns_error_text():
    hub = _hub()

    async def run():
        await hub.handle(_open_cmd())
        hub._sessions["s1"].ui.page = _FakePage(eval_fail=RuntimeError("boom at line 1"))
        rep = await hub.handle(_cmd(eval=wpb.ProbeEval(expression="bad +", result_max_bytes=0)))
        assert rep.probe_reply.failure.code == CODE_FAILED
        assert "boom at line 1" in rep.probe_reply.failure.message

    asyncio.run(run())


def test_eval_timeout(monkeypatch):
    from testpilot_worker import probes
    monkeypatch.setattr(probes, "EVAL_HARD_TIMEOUT", 0.1)
    hub = _hub()

    async def run():
        await hub.handle(_open_cmd())
        hub._sessions["s1"].ui.page = _FakePage(eval_delay=0.5)
        rep = await hub.handle(_cmd(eval=wpb.ProbeEval(expression="1", result_max_bytes=0)))
        assert rep.probe_reply.failure.code == CODE_TIMEOUT

    asyncio.run(run())


# ---- record=False（探测模式不录 HAR/tracing） ---------------------------------------

def test_probe_sessions_created_with_record_false():
    hub = _hub()

    async def run():
        await hub.handle(_open_cmd())
        assert _FakeUiSession.instances[0].record is False
        assert not _FakeUiSession.instances[0].record  # 语义自检

    asyncio.run(run())


# ---- client 接线 ----------------------------------------------------------------

def test_client_probe_command_emits_reply_via_outbox():
    from testpilot_worker.client import WorkerClient

    c = WorkerClient("127.0.0.1:1", [], 1, [1])
    cmd = _open_cmd()

    async def run():
        await c._handle_probe(cmd)

    asyncio.run(run())
    assert c.outbox.qsize() == 1
    ev = c.outbox.get_nowait()
    assert ev.WhichOneof("event") == "probe_reply"
    assert ev.probe_reply.request_id == "req-1"


def test_client_probe_reply_dropped_after_disconnect():
    from testpilot_worker.client import WorkerClient

    c = WorkerClient("127.0.0.1:1", [], 1, [1])
    cmd = _open_cmd()

    async def run():
        c._dead = True  # 断连窗口：旧会话回执不得发出
        await c._handle_probe(cmd)

    asyncio.run(run())
    assert c.outbox.qsize() == 0
