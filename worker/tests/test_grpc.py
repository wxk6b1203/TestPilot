"""grpc_exec：反射动态调用 + 引擎 GRPC_CALL 步骤（本地 echo 服务，无外网）。"""

import asyncio
from concurrent import futures

import grpc
import pytest
from grpc_reflection.v1alpha import reflection
from testpilot.common.v1 import types_pb2 as pb
from testpilot.worker.v1 import worker_pb2 as wpb
from testpilot.echo.v1 import echo_pb2, echo_pb2_grpc

from testpilot_worker import grpc_exec
from testpilot_worker.engine import CaseRunner, StepFailure


@pytest.fixture(scope="module")
def echo_addr():
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))

    class Echo(echo_pb2_grpc.EchoServiceServicer):
        def Echo(self, request, context):
            text = request.message * max(request.repeat, 1)
            return echo_pb2.EchoResponse(message=text, length=len(text))

        def Add(self, request, context):
            return echo_pb2.AddResponse(sum=request.a + request.b)

    echo_pb2_grpc.add_EchoServiceServicer_to_server(Echo(), server)
    names = (echo_pb2.DESCRIPTOR.services_by_name["EchoService"].full_name,
             reflection.SERVICE_NAME)
    reflection.enable_server_reflection(names, server)
    port = server.add_insecure_port("127.0.0.1:0")
    server.start()
    yield f"127.0.0.1:{port}"
    server.stop(grace=0.5)


def _api(**kwargs) -> pb.GrpcApi:
    api = pb.GrpcApi(full_service="testpilot.echo.v1.EchoService", method="Echo")
    for k, v in kwargs.items():
        setattr(api, k, v)
    return api


def test_call_reflection_roundtrip(echo_addr):
    api = _api()
    api.request_message.update({"message": "hi", "repeat": 2})
    api.deadline.FromSeconds(5)
    req, resp = asyncio.run(grpc_exec.call_async(echo_addr, api))
    assert resp == {"json": {"message": "hihi", "length": 4}, "status": "OK"}
    assert req["service"] == "testpilot.echo.v1.EchoService"
    assert req["request"] == {"message": "hi", "repeat": 2.0}


def test_call_request_override_deep_merge(echo_addr):
    api = _api()
    api.request_message.update({"message": "a", "repeat": 1})
    _, resp = asyncio.run(grpc_exec.call_async(echo_addr, api, {"repeat": 3}))
    assert resp["json"]["message"] == "aaa"


def test_call_error_surface(echo_addr):
    api = pb.GrpcApi(full_service="testpilot.echo.v1.EchoService", method="NoSuch")
    with pytest.raises(grpc_exec.GrpcCallError, match="NoSuch"):
        asyncio.run(grpc_exec.call_async(echo_addr, api))


def test_call_unknown_service(echo_addr):
    api = pb.GrpcApi(full_service="no.such.Service", method="M")
    with pytest.raises(grpc_exec.GrpcCallError):
        asyncio.run(grpc_exec.call_async(echo_addr, api))


def test_target_from_base_url():
    assert grpc_exec.target_from_base_url("https://g.example") == "g.example:443"
    assert grpc_exec.target_from_base_url("http://g.example:8080") == "g.example:8080"
    assert grpc_exec.target_from_base_url("127.0.0.1:19090") == "127.0.0.1:19090"
    with pytest.raises(grpc_exec.GrpcCallError):
        grpc_exec.target_from_base_url("https://g.example/with/path")
    with pytest.raises(grpc_exec.GrpcCallError):
        grpc_exec.target_from_base_url("")


# ---- 引擎层：GRPC_CALL 步骤 ----

def _task_with_grpc(addr: str, **request) -> wpb.TaskAssignment:
    task = wpb.TaskAssignment()
    task.env.base_url = addr
    api = task.functional.grpc_apis["42"]
    api.full_service = "testpilot.echo.v1.EchoService"
    api.method = "Echo"
    api.request_message.update(request)
    api.deadline.FromSeconds(5)
    case = task.functional.case
    step = case.declarative.steps.add(name="grpc echo")
    step.grpc_call.grpc_api_id = "42"
    return task


def test_engine_grpc_call_step(echo_addr):
    task = _task_with_grpc(echo_addr, message="x", repeat=2)
    r = CaseRunner(task)

    async def run():
        spec = task.functional.case.declarative.steps[0].grpc_call
        req, resp = await r._do_grpc_call(spec, [])
        assert resp["json"] == {"message": "xx", "length": 2}
        assert r.last_response == resp  # JSONPATH 断言可经 $.json.* 取值

    asyncio.run(run())


def test_engine_grpc_call_missing_resolution():
    task = wpb.TaskAssignment()  # 无 grpc_apis 映射
    task.env.base_url = "127.0.0.1:1"
    case = task.functional.case
    step = case.declarative.steps.add(name="grpc missing")
    step.grpc_call.grpc_api_id = "42"
    r = CaseRunner(task)

    async def run():
        with pytest.raises(StepFailure, match="must be resolved by scheduler"):
            await r._do_grpc_call(step.grpc_call, [])

    asyncio.run(run())
