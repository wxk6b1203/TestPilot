"""低代码 Page 运行结果：UI 动作日志 / 截图产物 / 失败现场截图回传步骤结果。"""

from __future__ import annotations

import asyncio

from testpilot.common.v1 import types_pb2 as pb
from testpilot.worker.v1 import worker_pb2 as wpb

from testpilot_worker import ui
from testpilot_worker.engine import _run_lowcode


class _FakeUiSession:
    """无浏览器 UiSession：验证 _run_lowcode 的结果回传路径。"""

    fail_on: int | None = None

    def __init__(self, base_url: str, case_dir, case_rel: str, render):
        self.base_url = base_url
        self.case_dir = case_dir
        self.case_rel = case_rel
        self.render = render
        self.started = False

    async def execute(self, action: int, target: str, value: str, logs: list[str]):
        if action == self.fail_on:
            raise RuntimeError("fake browser click failed")
        logs.append(f"fake ui {action} {target} {value}".rstrip())
        if action == pb.UI_ACTION_SCREENSHOT:
            return [ui.UiArtifact(kind="screenshot",
                                  uri=f"{self.case_rel}/shot-1.png", size=11)]
        return []

    async def failure_screenshot(self):
        return [ui.UiArtifact(kind="screenshot",
                              uri=f"{self.case_rel}/failure-1.png", size=22)]

    async def finish(self):
        return [
            ui.UiArtifact(kind="trace", uri=f"{self.case_rel}/trace.zip", size=33),
            ui.UiArtifact(kind="har", uri=f"{self.case_rel}/network.har", size=44),
        ]


def _task(source: str) -> wpb.TaskAssignment:
    task = wpb.TaskAssignment(task_id="t-ui", run_id="r-ui", tenant_id=1)
    task.timeout.FromSeconds(25)
    task.env.base_url = "http://example.com"
    ft = task.functional
    ft.case.id = "c-ui"
    ft.case.type = pb.TEST_CASE_TYPE_LOWCODE
    ft.case.name = "lc-ui"
    ft.case.lowcode.source = source
    ft.case.lowcode.entry = "run"
    ft.case_result_id = "cr-ui"
    return task


def _run(task):
    return asyncio.run(_run_lowcode(task))


def test_lowcode_page_logs_and_screenshot_artifacts_attached(monkeypatch):
    monkeypatch.setattr(ui, "UiSession", _FakeUiSession)
    src = """from testpilot_sdk import Context


async def run(ctx):
    await ctx.page.screenshot()
    await ctx.page.click("#go")
"""
    status, error, _elapsed, steps = _run(_task(src))
    assert status == pb.CASE_STATUS_PASSED, error
    sr = steps[0]
    assert any("fake ui" in line for line in sr.logs)
    assert [a.kind for a in sr.artifacts] == ["screenshot", "trace", "har"]
    assert sr.artifacts[0].uri == "r-ui/cr-ui/shot-1.png"


def test_lowcode_page_failure_captures_failure_screenshot(monkeypatch):
    monkeypatch.setattr(ui, "UiSession", _FakeUiSession)
    monkeypatch.setattr(_FakeUiSession, "fail_on", pb.UI_ACTION_CLICK)
    src = """from testpilot_sdk import Context


async def run(ctx):
    await ctx.page.click("#broken")
"""
    status, error, _elapsed, steps = _run(_task(src))
    assert status == pb.CASE_STATUS_FAILED
    assert "fake browser click failed" in error
    sr = steps[0]
    assert sr.status == pb.STEP_STATUS_FAILED
    assert [a.kind for a in sr.artifacts] == ["screenshot", "trace", "har"]
    assert sr.artifacts[0].uri == "r-ui/cr-ui/failure-1.png"
