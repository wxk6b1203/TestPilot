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

import httpx
from testpilot.common.v1 import types_pb2 as pb
from testpilot.worker.v1 import worker_pb2 as wpb

log = logging.getLogger("testpilot.worker.stress")

EmitMetric = Callable[[wpb.StressMetricBatch], Awaitable[None]]


def _killpg(proc) -> None:
    """SIGKILL 进程组（start_new_session 后为独立会话；防 Locust 子进程成孤儿）。"""
    import signal
    try:
        os.killpg(proc.pid, signal.SIGKILL)
    except (ProcessLookupError, PermissionError):
        try:
            proc.kill()
        except ProcessLookupError:
            pass

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
    """执行压测子任务：api 目标走 Locust 子进程；behavior_case 走进程内 asyncio 负载环。"""
    if task.stress.plan.behavior_case_id:
        return await _run_behavior(task, emit)
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
        start_new_session=True,  # 独立进程组：超时/取消时连 Locust 子进程一起杀
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
                ts = float(msg.get("ts") or 0)
                pt.ts.FromSeconds(int(ts))
                pt.ts.nanos = int((ts % 1) * 1e9)
                pt.rps = float(msg.get("rps") or 0)
                pt.latency_p50_ms = float(msg.get("p50") or 0)
                pt.latency_p95_ms = float(msg.get("p95") or 0)
                pt.latency_p99_ms = float(msg.get("p99") or 0)
                pt.error_rate = float(msg.get("error_rate") or 0)
                pt.concurrency = int(msg.get("concurrency") or 0)
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
        # 取消路径兜底：子进程不杀则 readers 卡在管道 EOF 上永不返回（任务悬挂、信号量泄漏）
        if proc.returncode is None:
            _killpg(proc)
            try:
                await asyncio.wait_for(proc.wait(), timeout=3)
            except (TimeoutError, ProcessLookupError):
                pass
        await readers
        os.unlink(spec_path)

    result.duration.FromTimedelta(timedelta(seconds=max(time.perf_counter() - started, 0.001)))
    log.info("stress task %s done: status=%s err=%s", task.task_id, result.status, result.error[:100])
    return result


def _scaled_ramp(plan: pb.StressTestPlan, assigned: int) -> list[dict]:
    """ramp target 是全局并发 → 按本 Worker 分摊值缩放。"""
    ramp = [
        {"at": s.at.ToTimedelta().total_seconds(), "target": s.target}
        for s in plan.load_profile.ramp
    ]
    max_target = max([r["target"] for r in ramp], default=0)
    if ramp and max_target > 0:
        scale = assigned / max_target
        for r in ramp:
            r["target"] = max(1, round(r["target"] * scale))
    return ramp


def _percentile(sorted_vals: list[float], p: float) -> float:
    if not sorted_vals:
        return 0.0
    k = (len(sorted_vals) - 1) * p
    lo, hi = int(k), min(int(k) + 1, len(sorted_vals) - 1)
    frac = k - lo
    return sorted_vals[lo] * (1 - frac) + sorted_vals[hi] * frac


async def _run_behavior(task: wpb.TaskAssignment, emit: EmitMetric) -> wpb.TaskResult:
    """行为压测（target_type=2）：进程内 asyncio 负载环 + 沙箱常驻循环模式。

    - 预起 assigned_concurrency 个沙箱进程，每个以「循环模式」跑行为脚本；
    - 并发门控：沙箱每次迭代前经桥 op=iteration_gate 取额度（Worker 按 ramp 放行，
      duration 结束返回 go=false 令沙箱优雅退出）；
    - 迭代结果以 event 流回收，按 metrics_interval 采样 → StressMetricBatch
      （与 Locust 路径同协议，报告页/落库零改动）。
    """
    from .sandbox import SubprocessBackend, bridge_http_handler

    st = task.stress
    plan = st.plan
    started = time.perf_counter()
    result = wpb.TaskResult(task_id=task.task_id, run_id=task.run_id)

    assigned = max(st.assigned_concurrency, 1)
    ramp = _scaled_ramp(plan, assigned)
    duration = plan.load_profile.duration.ToTimedelta().total_seconds() or 60
    interval = max(plan.metrics_interval.ToMilliseconds(), 200) / 1000.0
    base_url = task.env.base_url or task.env.environment.base_url
    vars_init = {v.key: v.value for v in task.env.variables if not v.sensitive}
    payload = {
        "vars": vars_init, "base_url": base_url, "parameters": {},
        "tenant_id": task.tenant_id,
    }
    if not st.behavior_source.strip():
        result.status = pb.RUN_STATUS_FAILED
        result.error = "stress behavior_source 为空"
        return result

    # 共享门控状态 + 采样记录
    limit = assigned
    inflight = 0
    stopped = False
    cond = asyncio.Condition()
    records: list[tuple[float, float, bool]] = []  # (ts, elapsed_ms, ok)
    loop = asyncio.get_running_loop()

    async def gate(_args: dict) -> dict:
        nonlocal inflight
        async with cond:
            while not stopped and inflight >= limit:
                # 等待超时兜底：某沙箱在 gate→event 之间崩溃（进程死）时 on_iteration
                # 永不回调 → inflight 永不释放 → 其余沙箱永久等 cond（并发坍缩为 0）。
                # 超时放 go=false 令本沙箱优雅退出，避免死等到 duration 结束。
                try:
                    await asyncio.wait_for(cond.wait(), timeout=10)
                except TimeoutError:
                    return {"go": False}
            if stopped:
                return {"go": False}
            inflight += 1
            return {"go": True}

    async def on_iteration(msg: dict) -> None:
        nonlocal inflight
        async with cond:
            inflight = max(0, inflight - 1)
            cond.notify_all()
        records.append((time.perf_counter(), float(msg.get("elapsed_ms") or 0),
                        bool(msg.get("ok"))))

    async def ramp_and_stop():
        t0 = time.perf_counter()
        for r in sorted(ramp, key=lambda x: x["at"]):
            delay = r["at"] - (time.perf_counter() - t0)
            if delay > 0:
                await asyncio.sleep(delay)
            async with cond:
                nonlocal limit
                limit = r["target"]
        await asyncio.sleep(max(duration - (time.perf_counter() - t0), 0))
        async with cond:
            nonlocal stopped
            stopped = True
            cond.notify_all()

    finished = asyncio.Event()

    async def sampler():
        last = time.perf_counter()
        while not finished.is_set():
            try:
                await asyncio.wait_for(finished.wait(), timeout=interval)
            except TimeoutError:
                pass
            now = time.perf_counter()
            window = max(now - last, 1e-6)
            last = now
            recs, records[:] = records[:], []
            if not recs:
                continue
            lat = sorted(r[1] for r in recs)
            n = len(recs)
            errs = sum(1 for r in recs if not r[2])
            batch = wpb.StressMetricBatch(task_id=task.task_id, run_id=task.run_id)
            pt = batch.points.add()
            epoch = time.time()
            pt.ts.FromSeconds(int(epoch))
            pt.ts.nanos = int((epoch % 1) * 1e9)
            pt.rps = n / window
            pt.latency_p50_ms = _percentile(lat, 0.50)
            pt.latency_p95_ms = _percentile(lat, 0.95)
            pt.latency_p99_ms = _percentile(lat, 0.99)
            pt.error_rate = errs / n
            pt.concurrency = inflight
            await emit(batch)

    sampler_task = asyncio.create_task(sampler())
    pace_task = asyncio.create_task(ramp_and_stop())

    # 预起 K 个沙箱常驻进程（K = 本 Worker 分摊并发）
    auto_headers = {v.key: v.value for v in task.env.variables
                    if not v.sensitive and v.category == pb.VARIABLE_CATEGORY_HEADER}
    async with httpx.AsyncClient(verify=True) as client:
        backends = [
            SubprocessBackend(
                lambda args, _c=client: bridge_http_handler(_c, base_url, args, auto_headers),
                extra_ops={"iteration_gate": gate})
            for _ in range(assigned)
        ]
        for b in backends:
            b.set_loop_callback(on_iteration)
        # 沙箱超时：任务级 timeout 兜底（调度器下发的总宽限），与 duration+60 取小
        sandbox_timeout = duration + 60
        if task.timeout.ToSeconds():
            sandbox_timeout = min(sandbox_timeout, task.timeout.ToSeconds())
        runs = [b.run(st.behavior_source, st.behavior_entry or "run", payload,
                      timeout_s=sandbox_timeout, loop=True)
                for b in backends]
        outcomes = await asyncio.gather(*runs, return_exceptions=True)

    # 取消路径（worker 停机/断连）必须收尾采样与 pace 任务，否则它们成为孤儿
    # 协程继续向 outbox emit；finished.set() 也可能被跳过导致门控语义错乱
    try:
        finished.set()
        await asyncio.gather(sampler_task, pace_task, return_exceptions=True)
    except asyncio.CancelledError:
        for t in (sampler_task, pace_task):
            t.cancel()
        await asyncio.gather(sampler_task, pace_task, return_exceptions=True)
        raise
    finally:
        for b in backends:
            b.set_loop_callback(None)

    timed_out = any(isinstance(o, BaseException) or (hasattr(o, "timed_out") and o.timed_out)
                    for o in outcomes)
    spawned_fail = [o for o in outcomes if isinstance(o, BaseException)]
    if timed_out or spawned_fail:
        result.status = pb.RUN_STATUS_FAILED
        err = spawned_fail[0] if spawned_fail else "sandbox 超时未按门控退出"
        result.error = f"behavior stress failed: {err}"
    else:
        result.status = pb.RUN_STATUS_PASSED

    result.duration.FromTimedelta(timedelta(seconds=max(time.perf_counter() - started, 0.001)))
    log.info("behavior stress task %s done: concurrency=%d duration=%ss status=%s",
             task.task_id, assigned, duration, result.status)
    return result
