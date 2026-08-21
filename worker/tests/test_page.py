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
        # SDK 单位毫秒；桥协议无 target 的 WAIT 按秒解释，Page 内部换算
        ("ui_action", {"action": "wait", "target": "", "value": "0.25"}),
        ("ui_action", {"action": "screenshot", "target": "", "value": ""}),
    ]


def test_page_hidden_and_selector_wait_mapping():
    b = _StubBridge()
    p = Page(b)  # type: ignore[arg-type]

    async def run():
        await p.expect_hidden("#spinner")
        await p.wait_for_selector("#form", 1500)
        await p.download("#export", "report.csv")

    asyncio.run(run())
    assert b.calls == [
        ("ui_action", {"action": "expect_visible", "target": "#spinner",
                       "value": "hidden"}),
        # SDK 对用户统一毫秒；桥协议 selector wait 的 value 按秒解释，内部换算
        ("ui_action", {"action": "wait", "target": "#form", "value": "1.5"}),
        ("ui_action", {"action": "download", "target": "#export",
                       "value": "report.csv"}),
    ]


def test_page_wait_arg_validation():
    p = Page(_StubBridge())  # type: ignore[arg-type]

    async def bad_fixed():
        await p.wait_for(-1)

    async def bad_selector():
        await p.wait_for_selector("#x", 0)

    with pytest.raises(ValueError, match="must be >= 0"):
        asyncio.run(bad_fixed())
    with pytest.raises(ValueError, match="must be > 0"):
        asyncio.run(bad_selector())


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


# ---- goto 出口校验归一化（协议相对 URL 绕过回归） ----

def test_normalize_goto_absolute():
    from testpilot_worker.ui import _normalize_goto
    assert _normalize_goto("http://h", "http://x/y") == "http://x/y"
    assert _normalize_goto("https://h", "https://x/y") == "https://x/y"


def test_normalize_goto_relative():
    from testpilot_worker.ui import _normalize_goto
    assert _normalize_goto("http://h/", "/a/b") == "http://h/a/b"
    assert _normalize_goto("http://h", "a/b") == "http://h/a/b"


def test_normalize_goto_protocol_relative_rejected_as_absolute():
    # 回归：//127.0.0.1:8080/secret 曾按相对路径拼 base_url 检查（host=允许域），
    # 但浏览器内实际导航到 http://127.0.0.1:8080/secret —— SSRF 通道。
    # 归一化后必须产出带 scheme 的绝对 URL（供 egress 私网拦截）。
    from testpilot_worker.ui import _normalize_goto
    assert _normalize_goto("http://allowed.example", "//127.0.0.1:8080/secret") ==         "http://127.0.0.1:8080/secret"
    assert _normalize_goto("https://allowed.example", "//127.0.0.1:8080/secret") ==         "https://127.0.0.1:8080/secret"


def test_normalize_goto_scheme_whitelist():
    from testpilot_worker.ui import _normalize_goto
    import pytest
    for bad in ("file:///etc/passwd", "javascript:alert(1)", "ftp://x/y"):
        with pytest.raises(ValueError):
            _normalize_goto("http://h", bad)
