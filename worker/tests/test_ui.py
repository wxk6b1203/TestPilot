"""ui.py：UiSession 生命周期——ensure 半初始化防护（无真实浏览器，stub playwright）。"""

import asyncio
import sys
import types

import pytest

from testpilot_worker.ui import UiSession


class _FakePage:
    def set_default_timeout(self, ms):
        pass


class _FakeTracing:
    async def start(self, **kwargs):
        pass


class _FakeContext:
    def __init__(self):
        self.tracing = _FakeTracing()
        self.closed = False

    async def new_page(self):
        return _FakePage()

    async def close(self):
        self.closed = True


class _FakePlaywright:
    """async_playwright() 替身：start → chromium.launch → new_context → new_page。

    launch_error 非 None 时模拟 launch 失败（ensure 半初始化路径）。
    """

    instances: list["_FakePlaywright"] = []
    launch_error: Exception | None = None

    def __init__(self):
        self.stopped = False
        _FakePlaywright.instances.append(self)

    @property
    def chromium(self):
        return self  # launch 入口（失败注入在此路径的 launch() 里生效）

    async def start(self):
        return self

    async def launch(self, **kwargs):
        if _FakePlaywright.launch_error is not None:
            raise _FakePlaywright.launch_error
        return _FakeBrowser()

    async def stop(self):
        self.stopped = True


class _FakeBrowser:
    async def new_context(self, **kwargs):
        return _FakeContext()

    async def close(self):
        pass


@pytest.fixture()
def fake_playwright(monkeypatch):
    _FakePlaywright.instances.clear()
    _FakePlaywright.launch_error = None
    # 同时替换父包与子模块：venv 装/不装 playwright 都走 stub（不起真实浏览器）
    parent = types.ModuleType("playwright")
    mod = types.ModuleType("playwright.async_api")
    mod.async_playwright = _FakePlaywright
    parent.async_api = mod
    monkeypatch.setitem(sys.modules, "playwright", parent)
    monkeypatch.setitem(sys.modules, "playwright.async_api", mod)
    return _FakePlaywright


def _session(tmp_path) -> UiSession:
    return UiSession(base_url="http://x", case_dir=tmp_path, case_rel="t",
                     render=lambda s: s)


def test_ensure_failure_cleans_partial_driver(fake_playwright, tmp_path):
    """回归：chromium.launch 失败时已启动的 playwright driver 必须被回收，
    且不得把半初始化资源挂到 self——否则重试 ensure() 整体覆盖 self._pw，
    旧 driver 进程永久泄漏。"""
    fake_playwright.launch_error = RuntimeError("launch boom")
    s = _session(tmp_path)
    with pytest.raises(RuntimeError, match="launch boom"):
        asyncio.run(s.ensure())
    assert s._pw is None and s.page is None          # self 保持干净
    assert fake_playwright.instances[0].stopped      # 已启动的 driver 已回收
    # 重试不因覆盖而泄漏：每个失败实例都被回收
    with pytest.raises(RuntimeError):
        asyncio.run(s.ensure())
    assert all(p.stopped for p in fake_playwright.instances)
    assert s._pw is None and s.page is None


def test_ensure_success_publishes_state(fake_playwright, tmp_path):
    """全部资源构建成功后才原子挂到 self。"""
    s = _session(tmp_path)
    asyncio.run(s.ensure())
    pw = fake_playwright.instances[0]
    assert s._pw is pw and s.page is not None
    assert not pw.stopped
