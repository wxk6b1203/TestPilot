"""UI 探测工具（tools.probe）：请求构造 / session 注入 / 快照截断 / 审批分类。"""

from __future__ import annotations

import asyncio
import os
from types import SimpleNamespace

from testpilot.common.v1 import types_pb2 as pb
from testpilot.copilot.v1 import copilot_pb2 as cpb

from testpilot_copilot.tools import (
    CopilotDeps, _clip_probe_snapshot, ui_probe_act, ui_probe_close,
    ui_probe_eval, ui_probe_open, ui_probe_snapshot,
)


def _deps(stub, probe_session_id="chat-1"):
    return CopilotDeps(sched=SimpleNamespace(stub=stub), tenant_id=7, user_id="u1",
                       http=None, token="t", probe_session_id=probe_session_id)


class _CaptureStub:
    """记录方法调用并按方法名回放预设响应。"""

    def __init__(self, responses: dict[str, object]):
        self.calls: list[tuple[str, object]] = []
        self.responses = responses

    def __getattr__(self, name):
        async def call(req):
            self.calls.append((name, req))
            return self.responses[name]
        return call


def _run(tool, deps, **kwargs):
    return asyncio.run(tool(SimpleNamespace(deps=deps), **kwargs))


def test_open_builds_request_and_injects_session():
    resp = cpb.OpenProbeResponse(session_id="chat-1", worker_id="w1",
                                 final_url="https://aut.test/login",
                                 title="Login", aria_snapshot='- button "Sign in"')
    stub = _CaptureStub({"OpenProbe": resp})
    out = _run(ui_probe_open, _deps(stub), url="/login", env_id="e1")
    name, req = stub.calls[0]
    assert name == "OpenProbe"
    assert req.ctx.tenant_id == 7 and req.ctx.user_id == "u1"
    assert req.session_id == "chat-1"  # session 由 deps 注入，非 LLM 参数
    assert req.url == "/login" and req.env_id == "e1"
    assert out["workerId"] == "w1"
    assert "Sign in" in out["ariaSnapshot"]


def test_open_without_session_id_rejected():
    stub = _CaptureStub({})
    try:
        _run(ui_probe_open, _deps(stub, probe_session_id=""), url="https://a.test/")
        raise AssertionError("must reject when probe_session_id missing")
    except ValueError as e:
        assert "probe_session_id" in str(e)


def test_act_maps_action_and_normalizes_wait():
    resp = cpb.ActProbeResponse(final_url="https://aut.test/x", aria_snapshot="- main")
    stub = _CaptureStub({"ActProbe": resp})
    _run(ui_probe_act, _deps(stub), action="wait", value="1000")
    _, req = stub.calls[0]
    # 声明式 WAIT 按秒解释：_decl_value 将毫秒转秒（与用例引擎一致）
    assert req.action.action == pb.UI_ACTION_WAIT
    assert req.action.value == "1.0"  # proto value 为 string；声明式 WAIT 按秒


def test_snapshot_and_eval_and_close():
    resp_s = cpb.GetProbeSnapshotResponse(final_url="u", title="t", aria_snapshot="- main")
    resp_e = cpb.EvalProbeResponse(result_json='"ok"', result_truncated=False)
    resp_c = cpb.CloseProbeResponse(ok=True)
    stub = _CaptureStub({"GetProbeSnapshot": resp_s, "EvalProbe": resp_e, "CloseProbe": resp_c})
    out = _run(ui_probe_snapshot, _deps(stub))
    assert stub.calls[0][0] == "GetProbeSnapshot" and out["title"] == "t"
    out = _run(ui_probe_eval, _deps(stub), expression="1+1")
    assert out['resultJson'] == '"ok"'
    out = _run(ui_probe_close, _deps(stub))
    assert out["ok"] is True


def test_clip_snapshot_truncates(monkeypatch):
    monkeypatch.setenv("TP_COPILOT_PROBE_SNAPSHOT_MAX_BYTES", "128")
    big = {"ariaSnapshot": "x" * 300, "snapshotTruncated": False}
    out = _clip_probe_snapshot(big)
    assert out["snapshotTruncated"] is True
    assert len(out["ariaSnapshot"].encode()) <= 128  # 字节精确预算
    small = {"ariaSnapshot": "- main"}
    assert _clip_probe_snapshot(dict(small))["ariaSnapshot"] == "- main"


def test_probe_tools_approval_classification():
    # open/act/eval 写类（requires_approval=True）；snapshot/close 只读
    from testpilot_copilot.tools import probe as probe_toolset
    approval = {name: tool.requires_approval for name, tool in probe_toolset.tools.items()}
    assert approval["ui_probe_open"] is True
    assert approval["ui_probe_act"] is True
    assert approval["ui_probe_eval"] is True
    assert not approval["ui_probe_snapshot"]
    assert not approval["ui_probe_close"]
