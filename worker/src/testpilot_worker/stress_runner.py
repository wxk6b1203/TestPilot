"""压测运行器：独立子进程（gevent），Locust 库模式发压。

由 Worker stress.py 拉起：python -m testpilot_worker.stress_runner <spec.json>
设计约束（docs/design.md §8.2）：gevent 与 Worker asyncio 主循环不混用，故独立子进程。

stdout 协议（JSON Lines）：
  {"type":"metric", "ts", "rps", "p50", "p95", "p99", "error_rate", "concurrency"}
  {"type":"done", "ok", "error", "total": {"requests","failures","avg_ms","p95_ms"}}
stderr：locust 日志（由父进程采集为任务日志）
"""

from __future__ import annotations

import json
import sys
import time

from gevent import monkey

monkey.patch_all()  # 必须最先：Locust 依赖 gevent 协作式 IO

import gevent  # noqa: E402
from locust import HttpUser, constant, task  # noqa: E402
from locust.env import Environment  # noqa: E402


def _build_user(spec: dict):
    method = spec["method"]
    uri = spec["uri"]
    headers = spec.get("headers") or None
    params = spec.get("params") or None
    body = spec.get("body")
    think_s = max(float(spec.get("think_ms", 0)), 0) / 1000

    class StressUser(HttpUser):
        wait_time = constant(think_s)

        @task
        def hit(self):
            kwargs: dict = {"name": f"{method} {uri}", "headers": headers, "params": params}
            if isinstance(body, str) and body:
                kwargs["data"] = body
            self.client.request(method, uri, **kwargs)

    return StressUser


def _run(spec: dict) -> None:
    interval = max(int(spec.get("interval_ms", 1000)), 200) / 1000
    duration = max(float(spec.get("duration_s", 60)), 1)
    concurrency = max(int(spec.get("concurrency", 1)), 1)
    ramp = sorted(spec.get("ramp") or [], key=lambda s: float(s.get("at", 0)))

    user_cls = _build_user(spec)
    env = Environment(user_classes=[user_cls], host=spec["base_url"])
    runner = env.create_local_runner()

    runner.start(1, spawn_rate=1)
    started = time.monotonic()

    def controller():
        """阶梯加压：按 ramp 时间点调整用户数，末段后保持到 duration。"""
        for st in ramp:
            at = float(st.get("at", 0))
            target = max(int(st.get("target", concurrency)), 0)
            if target > concurrency:
                target = concurrency  # 本 Worker 分摊上限
            wait = at - (time.monotonic() - started)
            if wait > 0:
                gevent.sleep(wait)
            delta = target - runner.user_count
            if delta > 0:
                runner.spawn_users({user_cls.__name__: delta})
            elif delta < 0:
                runner.stop_users({user_cls.__name__: -delta})
        # 无 ramp 或 ramp 末段 < 分摊并发：顶到上限
        delta = concurrency - runner.user_count
        if delta > 0:
            runner.spawn_users({user_cls.__name__: delta})
        remaining = duration - (time.monotonic() - started)
        if remaining > 0:
            gevent.sleep(remaining)

    def sampler():
        total = env.stats.total
        last_n, last_f = 0, 0
        while True:
            gevent.sleep(interval)
            n, f = total.num_requests, total.num_failures
            dn, df = n - last_n, f - last_f
            point = {
                "type": "metric",
                "ts": time.time(),
                "rps": dn / interval,
                "p50": total.get_current_response_time_percentile(0.5) or 0,
                "p95": total.get_current_response_time_percentile(0.95) or 0,
                "p99": total.get_current_response_time_percentile(0.99) or 0,
                "error_rate": (df / dn) if dn else 0.0,
                "concurrency": runner.user_count,
            }
            print(json.dumps(point), flush=True)
            last_n, last_f = n, f

    ctrl = gevent.spawn(controller)
    samp = gevent.spawn(sampler)
    ctrl_exc: BaseException | None = None
    try:
        ctrl.get()  # 与 join 不同：controller 异常会被重新抛出，不静默吞掉
    except BaseException as e:  # noqa: BLE001 - 子进程边界必须转成 done 帧上报
        ctrl_exc = e
    runner.stop()
    samp.kill(block=False)
    gevent.sleep(0.2)  # 让在途请求统计落账

    total = env.stats.total
    ok = ctrl_exc is None and total.num_requests > 0  # 0 请求 = 发压器未真正工作
    error = ""
    if ctrl_exc is not None:
        error = f"controller failed: {type(ctrl_exc).__name__}: {ctrl_exc}"
    elif total.num_requests == 0:
        error = "stress runner made 0 requests"
    done = {
        "type": "done",
        "ok": ok,
        "error": error,
        "total": {
            "requests": total.num_requests,
            "failures": total.num_failures,
            "avg_ms": round(total.avg_response_time, 1),
            "p95_ms": total.get_response_time_percentile(0.95) or 0,
        },
    }
    print(json.dumps(done), flush=True)


def main() -> None:
    if len(sys.argv) < 2:
        print("usage: python -m testpilot_worker.stress_runner <spec.json>", file=sys.stderr)
        sys.exit(2)
    with open(sys.argv[1], encoding="utf-8") as f:
        spec = json.load(f)
    try:
        _run(spec)
    except Exception as e:
        print(json.dumps({"type": "done", "ok": False,
                          "error": f"{type(e).__name__}: {e}", "total": {}}), flush=True)
        sys.exit(1)


if __name__ == "__main__":
    main()
