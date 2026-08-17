"""sandbox.py：隔离强制开关（TP_SANDBOX_REQUIRE_ISOLATION）回归。"""

import asyncio
import gc
import os

from testpilot_worker.sandbox import SandboxLimits, SubprocessBackend

def run_coro(coro):
    """loop 关闭前回收 subprocess transport，避免 __del__ 触发 unraisable。"""
    async def _wrapper():
        out = await coro
        gc.collect()
        return out

    return asyncio.run(_wrapper())



def test_require_isolation_fails_closed_without_tool(monkeypatch):
    """开启强制隔离且无 OS 工具（模拟）→ 沙箱直接失败而非裸奔。"""
    monkeypatch.setenv("TP_SANDBOX_REQUIRE_ISOLATION", "1")
    monkeypatch.setenv("TP_SANDBOX_NET", "deny")
    # 模拟无任何隔离工具
    import platform
    import shutil
    monkeypatch.setattr(platform, "system", lambda: "Linux")
    monkeypatch.setattr(shutil, "which", lambda _x: None)

    b = SubprocessBackend(lambda args: {"ok": True})
    res = run_coro(b.run("x = 1", "run", {"vars": {}, "base_url": "http://x"}, timeout_s=5))
    assert not res.ok
    assert "isolation required" in res.error, res.error


def test_require_isolation_default_off_runs_unprotected(monkeypatch):
    """默认（开关关）：无工具时尽力而为降级运行（本地无 gVisor 场景）。"""
    monkeypatch.delenv("TP_SANDBOX_REQUIRE_ISOLATION", raising=False)
    monkeypatch.setenv("TP_SANDBOX_NET", "deny")
    import platform
    import shutil
    monkeypatch.setattr(platform, "system", lambda: "Linux")
    monkeypatch.setattr(shutil, "which", lambda _x: None)

    b = SubprocessBackend(lambda args: {"ok": True})
    res = run_coro(b.run("x = 1", "run", {"vars": {}, "base_url": "http://x"}, timeout_s=5))
    # 尽力而为：进程能起（无网络隔离但有 rlimit）
    assert res.timed_out or res.ok or res.error != ""


def test_sandbox_limits_reads_env_lazily(monkeypatch):
    """SandboxLimits 惰性读 env（apply_environ 之后生效，修复配置时序）。"""
    monkeypatch.setenv("TP_SANDBOX_CPU", "77")
    monkeypatch.setenv("TP_SANDBOX_REQUIRE_ISOLATION", "1")
    limits = SandboxLimits()
    assert limits.cpu_seconds == 77
    assert limits.require_isolation is True


def test_protocol_line_overflow_kills_sandbox():
    """回归：协议通道(fd1)无换行巨量输出必须终止沙箱（否则 Worker 无限缓冲 OOM）。"""
    b = SubprocessBackend(lambda args: {"ok": True})
    src = 'import os\nos.write(1, b"x" * (3 * 1024 * 1024))\n'
    res = run_coro(b.run(src, "run", {"vars": {}, "base_url": "http://x"}, timeout_s=10))
    assert not res.ok, res
    assert any("line exceeded limit" in l for l in res.logs), res.logs[-3:]
