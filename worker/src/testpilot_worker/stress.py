"""压测任务编排（Worker asyncio 侧）：拉起 stress_runner 子进程，流式回传指标。"""

from __future__ import annotations

import asyncio
import json
import logging
import os
import sys
import tempfile
import time
from datetime import timedelta
from typing import Awaitable, Callable

from testpilot.common.v1 import types_pb2 as pb
from testpilot.worker.v1 import worker_pb2 as wpb

log = logging.getLogger("testpilot.worker.stress")

EmitMetric = Callable[[wpb.StressMetricBatch], Awaitable[None]]

_METHODS = {
    pb.HTTP_METHOD_GET: "GET", pb.HTTP_METHOD_POST: "POST", pb.HTTP_METHOD_PUT: "PUT",
    pb.HTTP_METHOD_DELETE: "DELETE", pb.HTTP_METHOD_PATCH: "PATCH",
    pb.HTTP_METHOD_HEAD: "HEAD", pb.HTTP_METHOD_OPTIONS: "OPTIONS",
}


def _build_spec(task: wpb.TaskAssignment) -> dict:
    st = task.stress
    plan = st.plan
    api = st.inline_api
    if not api.uri:
        raise ValueError("stress task: inline_api 为空（仅支持单接口目标）")
    lp = plan.load_profile
    ramp = [
        {"at": s.at.ToTimedelta().total_seconds(), "target": s.target}
        for s in lp.ramp
    ]
    # ramp target 是全局并发 → 按比例缩放到本 Worker 分摊值
    max_target = max([r["target"] for r in ramp], default=0)
    assigned = max(st.assigned_concurrency, 1)
    if ramp and max_target > 0:
        scale = assigned / max_target
        for r in ramp:
            r["target"] = max(1, round(r["target"] * scale))
    duration = lp.duration.ToTimedelta().total_seconds() or 60
    body: str | None = None
    if api.HasField("body") and api.body.raw:
        body = api.body.raw
    return {
        "method": _METHODS.get(api.method, "GET"),
        "uri": api.uri,
        "base_url": task.env.base_url or task.env.environment.base_url,
        "headers": {kv.key: kv.value for kv in api.headers},
        "params": {kv.key: kv.value for kv in api.params},
        "body": body,
        "concurrency": assigned,
        "ramp": ramp,
        "duration_s": duration,
        "interval_ms": max(plan.metrics_interval.ToMilliseconds(), 200),
    }


async def run_stress(task: wpb.TaskAssignment, emit: EmitMetric) -> wpb.TaskResult:
    """执行压测子任务：子进程发压，stdout 指标点 → StressMetricBatch。"""
    started = time.perf_counter()
    result = wpb.TaskResult(task_id=task.task_id, run_id=task.run_id)
    try:
        spec = _build_spec(task)
    except Exception as e:
        result.status = pb.RUN_STATUS_FAILED
        result.error = f"stress spec error: {e}"
        return result

    fd, spec_path = tempfile.mkstemp(prefix="tp-stress-", suffix=".json")
    with os.fdopen(fd, "w", encoding="utf-8") as f:
        json.dump(spec, f)
    log.info("stress task %s: %s %s x%d for %ss",
             task.task_id, spec["method"], spec["uri"], spec["concurrency"], spec["duration_s"])

    timeout_s = (task.timeout.ToTimedelta().total_seconds()
                 or spec["duration_s"] + 120)
    proc = await asyncio.create_subprocess_exec(
        sys.executable, "-m", "testpilot_worker.stress_runner", spec_path,
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
    )

    done: dict | None = None
    stderr_lines: list[str] = []

    async def _read_metrics():
        nonlocal done
        while True:
            line = await proc.stdout.readline()
            if not line:
                return
            try:
                msg = json.loads(line)
            except ValueError:
                continue
            if msg.get("type") == "metric":
                batch = wpb.StressMetricBatch(task_id=task.task_id, run_id=task.run_id)
                pt = batch.points.add()
                pt.ts.FromSeconds(int(msg["ts"]))
                pt.ts.nanos = int((msg["ts"] % 1) * 1e9)
                pt.rps = msg["rps"]
                pt.latency_p50_ms = msg["p50"]
                pt.latency_p95_ms = msg["p95"]
                pt.latency_p99_ms = msg["p99"]
                pt.error_rate = msg["error_rate"]
                pt.concurrency = msg["concurrency"]
                await emit(batch)
            elif msg.get("type") == "done":
                done = msg

    async def _read_stderr():
        while True:
            line = await proc.stderr.readline()
            if not line:
                return
            stderr_lines.append(line.decode("utf-8", "replace").rstrip()[:500])

    readers = asyncio.gather(_read_metrics(), _read_stderr())
    try:
        await asyncio.wait_for(proc.wait(), timeout=timeout_s)
    except TimeoutError:
        proc.kill()
        await proc.wait()
        result.status = pb.RUN_STATUS_FAILED
        result.error = f"stress timeout after {timeout_s:.0f}s (killed)"
    else:
        if done and done.get("ok"):
            result.status = pb.RUN_STATUS_PASSED
        else:
            result.status = pb.RUN_STATUS_FAILED
            tail = stderr_lines[-1] if stderr_lines else ""
            result.error = (done or {}).get("error") or f"runner exited rc={proc.returncode}: {tail}"
    finally:
        await readers
        os.unlink(spec_path)

    result.duration.FromTimedelta(timedelta(seconds=max(time.perf_counter() - started, 0.001)))
    log.info("stress task %s done: status=%s err=%s", task.task_id, result.status, result.error[:100])
    return result
