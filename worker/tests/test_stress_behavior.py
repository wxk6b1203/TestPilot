"""行为压测（target_type=2）：沙箱循环模式 + 并发门控 + 指标采样（本地 HTTP，无浏览器）。"""

import asyncio
import gc
import json
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer

import pytest
from testpilot.worker.v1 import worker_pb2 as wpb

from testpilot_worker.stress import run_stress

SRC_OK = '''from testpilot_sdk import Context, assert_that


async def run(ctx: Context):
    resp = await ctx.http("GET", "/json")
    assert_that(resp.status, "status").eq(200)
'''

SRC_FAIL = '''from testpilot_sdk import Context, assert_that


async def run(ctx: Context):
    resp = await ctx.http("GET", "/json")
    assert_that(resp.status, "status").eq(999)
'''


class _Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        body = json.dumps({"ok": True}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass


def run_coro(coro):
    """在 loop 关闭前回收 subprocess transport，避免 __del__ 触发 unraisable。"""
    async def _wrapper():
        out = await coro
        gc.collect()
        return out

    return asyncio.run(_wrapper())


@pytest.fixture(scope="module")
def http_addr():
    srv = HTTPServer(("127.0.0.1", 0), _Handler)
    t = threading.Thread(target=srv.serve_forever, daemon=True)
    t.start()
    yield f"127.0.0.1:{srv.server_port}"
    srv.shutdown()


def _task(addr: str, source: str, concurrency: int = 2, duration_s: float = 2.0) -> wpb.TaskAssignment:
    task = wpb.TaskAssignment()
    task.task_id = "t-behavior"
    task.run_id = "r-behavior"
    task.env.base_url = f"http://{addr}"
    st = task.stress
    st.assigned_concurrency = concurrency
    st.behavior_source = source
    st.behavior_entry = "run"
    st.plan.behavior_case_id = "case-1"
    st.plan.load_profile.duration.FromMilliseconds(int(duration_s * 1000))
    st.plan.metrics_interval.FromMilliseconds(300)
    return task


def test_behavior_stress_passes_and_emits_metrics(http_addr):
    task = _task(http_addr, SRC_OK)
    batches: list[wpb.StressMetricBatch] = []

    async def emit(b):
        batches.append(b)

    result = run_coro(asyncio.wait_for(run_stress(task, emit), timeout=90))
    assert result.status == 2, result.error  # PASSED
    assert len(batches) >= 2, f"batches={len(batches)}"
    points = [p for b in batches for p in b.points]
    total_rps = sum(p.rps for p in points)
    assert total_rps > 0, points
    assert all(p.error_rate == 0 for p in points), points
    # 并发不超分配值
    assert all(p.concurrency <= 2 for p in points), points


def test_behavior_stress_records_script_errors(http_addr):
    task = _task(http_addr, SRC_FAIL, concurrency=1, duration_s=1.5)
    batches: list[wpb.StressMetricBatch] = []

    async def emit(b):
        batches.append(b)

    result = run_coro(asyncio.wait_for(run_stress(task, emit), timeout=90))
    # 负载环本身正常收尾（PASSED），错误体现在 error_rate 指标
    assert result.status == 2, result.error
    points = [p for b in batches for p in b.points]
    assert points and any(p.error_rate > 0 for p in points), points


def test_behavior_stress_empty_source_fails():
    task = wpb.TaskAssignment()
    task.task_id = "t-empty"
    task.run_id = "r-empty"
    task.stress.behavior_source = ""
    task.stress.plan.behavior_case_id = "case-1"
    result = run_coro(run_stress(task, lambda b: None))
    assert result.status == 3  # FAILED
    assert "behavior_source" in result.error


def test_percentile():
    from testpilot_worker.stress import _percentile
    vals = [1.0, 2.0, 3.0, 4.0]
    assert _percentile(vals, 0.50) == 2.5
    assert _percentile(vals, 0.95) == pytest.approx(3.85)
    assert _percentile([], 0.5) == 0.0
