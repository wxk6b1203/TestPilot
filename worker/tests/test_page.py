"""低代码 Page 模型：SDK 操作映射 + 桥 handler 路由（无浏览器，stub UiSession）。"""

import asyncio

import pytest
from testpilot.common.v1 import types_pb2 as pb

from testpilot_sdk.bridge import BridgeError
from testpilot_sdk.page import Page
from testpilot_worker import ui


class _StubBridge:
    def __init__(self):
        self.calls: list[tuple[str, dict]] = []
        self.fail_next: Exception | None = None

    async def call(self, op: str, args: dict):
        self.calls.append((op, args))
        if self.fail_next is not None:
            raise self.fail_next
        return {"ok": True}


class _StubSession:
    def __init__(self):
        self.calls: list[tuple[int, str, str]] = []
        self.fail: Exception | None = None

    async def execute(self, action: int, target: str, value: str, logs: list[str]):
        self.calls.append((action, target, value))
        if self.fail is not None:
            raise self.fail
        return [ui.UiArtifact(kind="screenshot", uri="r/1/shot.png", size=10)]


def test_page_ops_mapping():
    b = _StubBridge()
    p = Page(b)  # type: ignore[arg-type]

    async def run():
        await p.goto("/form")
        await p.fill("#u", "neo")
        await p.check("#c")
        await p.uncheck("#c")
        await p.expect_text("#r", "hi")
        await p.wait_for(250)
        await p.screenshot(full_page=False)

    asyncio.run(run())
    assert b.calls == [
        ("ui_action", {"action": "goto", "target": "/form", "value": ""}),
        ("ui_action", {"action": "fill", "target": "#u", "value": "neo"}),
        ("ui_action", {"action": "check", "target": "#c", "value": "true"}),
        ("ui_action", {"action": "check", "target": "#c", "value": "false"}),
        ("ui_action", {"action": "expect_text", "target": "#r", "value": "hi"}),
        ("ui_action", {"action": "wait", "target": "", "value": "250"}),
        ("ui_action", {"action": "screenshot", "target": "", "value": ""}),
    ]


def test_page_propagates_bridge_error():
    b = _StubBridge()
    b.fail_next = BridgeError("expect failed")
    p = Page(b)  # type: ignore[arg-type]

    async def run():
        await p.expect_text("#x", "absent")

    with pytest.raises(BridgeError):
        asyncio.run(run())


def test_bridge_ui_handler_routes_and_returns_artifacts():
    sess = _StubSession()
    handle = ui.bridge_ui_handler(lambda: sess)

    async def run():
        return await handle({"action": "fill", "target": "#u", "value": "v"})

    out = asyncio.run(run())
    assert sess.calls == [(pb.UI_ACTION_FILL, "#u", "v")]
    assert out["artifacts"] == [{"kind": "screenshot", "uri": "r/1/shot.png", "size": 10}]


def test_bridge_ui_handler_unknown_action_and_session_error():
    sess = _StubSession()
    handle = ui.bridge_ui_handler(lambda: sess)

    async def bad_action():
        return await handle({"action": "fly", "target": "x"})

    with pytest.raises(ValueError, match="unknown ui action"):
        asyncio.run(bad_action())

    sess.fail = RuntimeError("browser down")
    with pytest.raises(RuntimeError):
        asyncio.run(handle({"action": "click", "target": "#x"}))
