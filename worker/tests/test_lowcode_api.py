"""低代码按接口 ID 调用：SDK 路由 + 沙箱端到端（HTTP/gRPC/override/pre-post/错误路径）。"""

from __future__ import annotations

import asyncio
import gc
import json
import threading
from concurrent import futures
from http.server import BaseHTTPRequestHandler, HTTPServer

import grpc
import pytest
from grpc_reflection.v1alpha import reflection
from testpilot.common.v1 import types_pb2 as pb
from testpilot.worker.v1 import worker_pb2 as wpb

import testpilot_sdk.bridge as sdk_bridge
from testpilot_sdk import Context, GrpcAPI, HttpAPI
from testpilot_worker.engine import _run_lowcode


def run_coro(coro):
    async def _wrapper():
        out = await coro
        gc.collect()
        return out

    return asyncio.run(_wrapper())


# ---- SDK 模型层（FakeBridge，无子进程）----

class FakeBridge:
    def __init__(self, result=None):
        self.result = result
        self.calls: list[tuple[str, dict]] = []

    async def call(self, op, args, timeout=120):
        self.calls.append((op, args))
        return self.result or {}

    def emit(self, msg):
        pass


def test_http_api_by_id_sends_only_explicit_overrides(monkeypatch):
    fake = FakeBridge({"response": {"status": 200, "headers": {}, "body": {"ok": True},
                                    "text": "{}", "elapsed_ms": 4, "api_id": "123"},
                      "vars": {}})
    monkeypatch.setattr(sdk_bridge, "current_bridge", lambda: fake)

    resp = run_coro(HttpAPI(api_id="123", body={"name": "neo"}).run())
    assert resp.body == {"ok": True}
    op, args = fake.calls[0]
    assert op == "api_request"
    assert args["kind"] == "http" and args["api_id"] == "123"
    assert args["overrides"] == {"body": {"name": "neo"}}  # 未设置的 method/uri 不下发

    # run(kwargs) 与实例字段合并
    fake.calls.clear()
    run_coro(HttpAPI(api_id="123").run(params={"page": 2}, headers={"X-T": "1"}))
    ov = fake.calls[0][1]["overrides"]
    assert ov == {"params": {"page": 2}, "headers": {"X-T": "1"}}


def test_http_api_raw_fallback(monkeypatch):
    fake = FakeBridge({"status": 200, "headers": {}, "body": None, "text": "ok",
                       "elapsed_ms": 1})
    monkeypatch.setattr(sdk_bridge, "current_bridge", lambda: fake)
    run_coro(HttpAPI(method="post", uri="/echo", body={"a": 1}).run())
    op, args = fake.calls[0]
    assert op == "http_request"
    assert args["method"] == "POST" and args["uri"] == "/echo"


def test_grpc_api_by_id_and_raw(monkeypatch):
    fake = FakeBridge({"response": {"status": "OK", "json": {"a": 1}, "elapsed_ms": 2,
                                    "api_id": "9"}})
    monkeypatch.setattr(sdk_bridge, "current_bridge", lambda: fake)
    r = run_coro(GrpcAPI(api_id="9").run(request={"message": "hi"}, metadata={"trace": "t"}))
    assert r.json == {"a": 1}
    op, args = fake.calls[0]
    assert op == "api_request" and args["kind"] == "grpc"
    assert args["overrides"] == {"request": {"message": "hi"}, "metadata": {"trace": "t"}}

    fake.result = {"status": "OK", "json": {}, "request": {}, "elapsed_ms": 1}
    fake.calls.clear()
    run_coro(GrpcAPI(full_service="s.Echo", method="Echo").run())
    op, args = fake.calls[0]
    assert op == "grpc_request"
    assert args["full_service"] == "s.Echo" and args["method"] == "Echo"


def test_context_api_helpers_and_vars_merge(monkeypatch):
    fake = FakeBridge({"response": {"status": 200, "headers": {}, "body": {}, "text": "",
                                    "elapsed_ms": 1, "api_id": "1"},
                       "vars": {"pre": "x", "post": "y"}})
    monkeypatch.setattr(sdk_bridge, "current_bridge", lambda: fake)
    ctx = Context(fake, {"vars": {"base": 1}, "http_api_ids": ["1", "3"],
                         "grpc_api_ids": ["2", "3"]})
    assert isinstance(ctx.api("1"), HttpAPI)
    assert isinstance(ctx.api("2"), GrpcAPI)
    with pytest.raises(ValueError):
        ctx.api("3")  # HTTP 与 gRPC 同时存在 → 歧义
    with pytest.raises(ValueError):
        ctx.api("4")  # 未声明
    sdk_bridge.set_current_context(ctx)
    try:
        resp = run_coro(ctx.http_api("1").run())
    finally:
        sdk_bridge.clear_current_context()
    assert resp.status == 200
    assert ctx.vars["pre"] == "x" and ctx.vars["post"] == "y"


# ---- 端到端沙箱（真实子进程 + 本地服务器）----

class _HTTPHandler(BaseHTTPRequestHandler):
    def _reply(self, payload):
        body = json.dumps(payload).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        length = int(self.headers.get("Content-Length") or 0)
        raw = json.loads(self.rfile.read(length) or b"{}")
        self._reply({"method": self.command, "path": self.path,
                     "headers": {k.lower(): v for k, v in self.headers.items()},
                     "body": raw})

    do_GET = do_POST

    def log_message(self, *args):
        pass


@pytest.fixture(scope="module")
def http_addr():
    srv = HTTPServer(("127.0.0.1", 0), _HTTPHandler)
    t = threading.Thread(target=srv.serve_forever, daemon=True)
    t.start()
    yield f"127.0.0.1:{srv.server_port}"
    srv.shutdown()
    t.join(timeout=5)


@pytest.fixture(scope="module")
def grpc_addr():
    from testpilot.echo.v1 import echo_pb2, echo_pb2_grpc

    server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))

    class Echo(echo_pb2_grpc.EchoServiceServicer):
        def Echo(self, request, context):
            text = request.message * max(request.repeat, 1)
            return echo_pb2.EchoResponse(message=text, length=len(text))

    echo_pb2_grpc.add_EchoServiceServicer_to_server(Echo(), server)
    names = (echo_pb2.DESCRIPTOR.services_by_name["EchoService"].full_name,
             reflection.SERVICE_NAME)
    reflection.enable_server_reflection(names, server)
    port = server.add_insecure_port("127.0.0.1:0")
    server.start()
    yield f"127.0.0.1:{port}"
    server.stop(grace=0.5)


def _low_task(addr: str, source: str, *, http_apis: dict[str, pb.HttpApi] | None = None,
              grpc_apis: dict[str, pb.GrpcApi] | None = None,
              wrappers: str = "", timeout_s: float = 25.0) -> wpb.TaskAssignment:
    task = wpb.TaskAssignment(task_id="t-lc", run_id="r-lc", tenant_id=1)
    task.timeout.FromSeconds(int(timeout_s))
    if http_apis:
        task.env.base_url = f"http://{addr}"
    else:
        task.env.base_url = addr
    task.env.variables.add(key="name", value="neo")
    task.env.variables.add(key="token", value="t-123", category=pb.VARIABLE_CATEGORY_HEADER)
    ft = task.functional
    ft.case.id = "c-lc"
    ft.case.type = pb.TEST_CASE_TYPE_LOWCODE
    ft.case.name = "lc-api"
    ft.case.lowcode.source = source
    ft.case.lowcode.entry = "run"
    ft.case_result_id = "cr-lc"
    ft.api_wrappers_source = wrappers
    for k, v in (http_apis or {}).items():
        ft.http_apis[k].CopyFrom(v)
    for k, v in (grpc_apis or {}).items():
        ft.grpc_apis[k].CopyFrom(v)
    return task


WRAP_HTTP = '''# auto-generated
from testpilot_sdk import HttpAPI


class Api123(HttpAPI):
    """Create User · POST /users"""
    api_id: str = "123"
'''

WRAP_GRPC = '''# auto-generated
from testpilot_sdk import GrpcAPI


class Api456(GrpcAPI):
    """echo · testpilot.echo.v1.EchoService/Echo"""
    api_id: str = "456"
'''


def test_lowcode_http_by_id_wrapper_and_ctx_api(http_addr):
    api = pb.HttpApi(id="123", method=pb.HTTP_METHOD_POST, uri="/echo")
    src = '''from tp_api_wrappers import Api123


async def run(ctx):
    a = await Api123().run(body={"name": ctx.vars["name"]})
    assert a.status == 200
    assert a.body["body"]["name"] == "neo"
    b = await ctx.http_api("123").run(body={"again": True})
    assert b.body["body"]["again"] is True
    await ctx.set_var("done", True)
'''
    task = _low_task(http_addr, src, http_apis={"123": api}, wrappers=WRAP_HTTP)
    status, error, _elapsed, steps = run_coro(_run_lowcode(task))
    assert status == pb.CASE_STATUS_PASSED, f"{error}\n{steps[0].logs if steps else ''}"
    assert steps[0].logs and any("POST " in x and "-> 200" in x for x in steps[0].logs), steps[0].logs


def test_lowcode_http_override_headers_params_and_auto_header(http_addr):
    api = pb.HttpApi(id="123", method=pb.HTTP_METHOD_GET, uri="/echo")
    api.headers.add(key="X-A", value="old")
    src = '''async def run(ctx):
    r = await ctx.api("123").run(params={"page": 2}, headers={"X-A": "new"})
    assert r.body["path"] == "/echo?page=2"
    assert r.body["headers"]["x-a"] == "new"
    assert r.body["headers"]["token"] == "t-123"
'''
    task = _low_task(http_addr, src, http_apis={"123": api})
    status, error, _, _ = run_coro(_run_lowcode(task))
    assert status == pb.CASE_STATUS_PASSED, error


def test_lowcode_http_pre_post_scripts_and_template_vars(http_addr):
    api = pb.HttpApi(id="123", method=pb.HTTP_METHOD_POST, uri="/echo?x={{greeting}}")
    pre = api.pre_scripts.add(id="pre", lang="python", enabled=True)
    pre.source = '''async def run(ctx):
    await ctx.set_var("greeting", "hi")
'''
    post = api.post_scripts.add(id="post", lang="python", enabled=True)
    post.source = '''async def run(ctx):
    await ctx.set_var("post_status", ctx.response["status"])
'''
    src = '''async def run(ctx):
    r = await ctx.http_api("123").run(body={"v": 1})
    assert "x=hi" in r.body["path"]
    assert ctx.vars["greeting"] == "hi"
    assert ctx.vars["post_status"] == 200
'''
    task = _low_task(http_addr, src, http_apis={"123": api})
    status, error, _, steps = run_coro(_run_lowcode(task))
    assert status == pb.CASE_STATUS_PASSED, f"{error}\n{steps[0].logs if steps else ''}"


def test_lowcode_grpc_by_id(grpc_addr):
    api = pb.GrpcApi(id="456", full_service="testpilot.echo.v1.EchoService",
                     method="Echo")
    src = '''from tp_api_wrappers import Api456


async def run(ctx):
    r = await Api456().run(request={"message": "hi", "repeat": 2})
    assert r.json["message"] == "hihi"
    g = await ctx.grpc_api("456").run(request={"message": "x", "repeat": 1})
    assert g.json["message"] == "x"
'''
    task = _low_task(grpc_addr, src, grpc_apis={"456": api}, wrappers=WRAP_GRPC)
    status, error, _, steps = run_coro(_run_lowcode(task))
    assert status == pb.CASE_STATUS_PASSED, f"{error}\n{steps[0].logs if steps else ''}"
    assert any("grpc testpilot.echo.v1.EchoService.Echo" in x for x in steps[0].logs)


def test_lowcode_http_by_id_missing_snapshot_fails_clearly(http_addr):
    src = '''from testpilot_sdk import HttpAPI


async def run(ctx):
    await HttpAPI(api_id="999").run()
'''
    task = _low_task(http_addr, src)
    status, error, _, _ = run_coro(_run_lowcode(task))
    assert status == pb.CASE_STATUS_FAILED
    assert "http api 999 not in task.http_apis" in error, error


def test_lowcode_ctx_api_undeclared_fails_clearly(http_addr):
    src = '''async def run(ctx):
    await ctx.api("123").run()
'''
    task = _low_task(http_addr, src)
    status, error, _, _ = run_coro(_run_lowcode(task))
    assert status == pb.CASE_STATUS_FAILED
    assert "not in this case's api refs" in error, error


# ---- 行为压测循环模式同样支持按 ID 调用 ----

def test_stress_behavior_by_id_wrapper(http_addr):
    from testpilot_worker.stress import run_stress

    api = pb.HttpApi(id="123", method=pb.HTTP_METHOD_POST, uri="/echo")
    src = '''from tp_api_wrappers import Api123


async def run(ctx):
    r = await Api123().run(body={"n": 1})
    assert r.status == 200
'''
    task = wpb.TaskAssignment(task_id="t-stress", run_id="r-stress", tenant_id=1)
    task.timeout.FromSeconds(30)
    task.env.base_url = f"http://{http_addr}"
    st = task.stress
    st.assigned_concurrency = 2
    st.behavior_source = src
    st.behavior_entry = "run"
    st.api_wrappers_source = WRAP_HTTP
    st.http_apis["123"].CopyFrom(api)
    st.plan.behavior_case_id = "case-1"
    st.plan.load_profile.duration.FromMilliseconds(1500)
    st.plan.metrics_interval.FromMilliseconds(300)
    batches: list[wpb.StressMetricBatch] = []

    async def emit(b):
        batches.append(b)

    result = run_coro(asyncio.wait_for(run_stress(task, emit), timeout=30))
    assert result.status == pb.RUN_STATUS_PASSED, result.error
    points = [p for b in batches for p in b.points]
    assert points and sum(p.rps for p in points) > 0, points
