"""stress_runner.py：done 帧 ok 判定（子进程级回归——runner 内 gevent monkey patch
不可进 pytest 进程，故以子进程 + 本地 HTTP 服务端到端驱动）。"""

import json
import os
import subprocess
import sys
import tempfile
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer


class _Handler(BaseHTTPRequestHandler):
    n = 0
    lock = threading.Lock()

    def _reply(self, code: int):
        self.send_response(code)
        self.send_header("Content-Length", "2")
        self.end_headers()
        self.wfile.write(b"{}")

    def do_GET(self):
        if self.path == "/fail":
            self._reply(500)                     # 100% 错误
        elif self.path == "/flip":
            with _Handler.lock:                  # 50% 错误（部分失败）
                _Handler.n += 1
                self._reply(200 if _Handler.n % 2 else 500)
        else:
            self._reply(200)

    def log_message(self, *args):
        pass


def _spec(addr: str, uri: str) -> dict:
    return {
        "method": "GET", "uri": uri, "base_url": f"http://{addr}",
        "headers": {}, "params": {}, "body": None,
        "concurrency": 2, "ramp": [], "duration_s": 2, "interval_ms": 500,
    }


def _run_runner(spec: dict) -> dict:
    """子进程跑 runner，解析 stdout 协议中的 done 帧。"""
    fd, path = tempfile.mkstemp(prefix="tp-stress-runner-test-", suffix=".json")
    with os.fdopen(fd, "w", encoding="utf-8") as f:
        json.dump(spec, f)
    try:
        proc = subprocess.run(
            [sys.executable, "-m", "testpilot_worker.stress_runner", path],
            capture_output=True, text=True, timeout=90)
    finally:
        os.unlink(path)
    assert proc.returncode == 0, proc.stderr[-2000:]
    done = None
    for line in proc.stdout.splitlines():
        try:
            msg = json.loads(line)
        except ValueError:
            continue
        if msg.get("type") == "done":
            done = msg
    assert done is not None, "no done frame on stdout"
    return done


def test_all_requests_failed_reports_failed():
    """回归：被测服务 100% 错误时必须判 FAILED（旧实现只看发压完成度 → 误判
    PASSED），error 带错误率。"""
    srv = HTTPServer(("127.0.0.1", 0), _Handler)
    t = threading.Thread(target=srv.serve_forever, daemon=True)
    t.start()
    try:
        done = _run_runner(_spec(f"127.0.0.1:{srv.server_port}", "/fail"))
    finally:
        srv.shutdown()
    assert done["ok"] is False
    assert "error rate 100%" in done["error"], done
    assert done["total"]["requests"] > 0      # 确实发出了压（非 0 请求路径）
    assert done["total"]["failures"] == done["total"]["requests"]


def test_partial_failures_still_passed():
    """语义：压测任务衡量发压是否完成——部分失败仍 PASSED（错误率经 metric
    流落库供告警，不在完成度判定里混入阈值）。"""
    srv = HTTPServer(("127.0.0.1", 0), _Handler)
    t = threading.Thread(target=srv.serve_forever, daemon=True)
    t.start()
    try:
        done = _run_runner(_spec(f"127.0.0.1:{srv.server_port}", "/flip"))
    finally:
        srv.shutdown()
    assert done["ok"] is True, done
    assert done["error"] == ""
    assert done["total"]["requests"] > 0
    assert done["total"]["failures"] > 0      # 确有失败但非全部
    assert done["total"]["failures"] < done["total"]["requests"]
