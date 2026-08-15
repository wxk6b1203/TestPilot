"""Scheduler CopilotToolService gRPC 客户端（Copilot 不直连 DB —— 全部经此）。

认证：Scheduler 侧 CopilotAuthUnary 校验 authorization: Bearer <JWT>，并要求请求
RequestContext 与 JWT claims 一致。本客户端从 contextvar 读取当前请求的用户 JWT
（chat 入口设置），经 interceptor 注入 gRPC metadata —— 不信任任何自报身份。
"""

from __future__ import annotations

import contextvars

import grpc
from google.protobuf import json_format

from testpilot.common.v1 import types_pb2 as pb
from testpilot.copilot.v1 import copilot_pb2 as cpb
from testpilot.copilot.v1 import copilot_pb2_grpc as cgrpc

from .tracing import TraceparentInterceptor

# 当前请求的用户 JWT（chat 协程设置；工具调用同任务链读取）
auth_token: contextvars.ContextVar[str] = contextvars.ContextVar("tp_sched_auth", default="")

# 工具 gRPC 调用默认 deadline：Scheduler 挂起（触发 run/导入等慢操作）时
# 无 deadline 的调用会让 chat 永久挂起（P1）。
_DEFAULT_DEADLINE = 30.0


class AuthInterceptor(grpc.aio.UnaryUnaryClientInterceptor):
    """给 Scheduler gRPC 调用注入 authorization: Bearer <JWT>。"""

    async def intercept_unary_unary(self, continuation, client_call_details, request):
        token = auth_token.get()
        if token:
            # 覆盖而非追加：避免调用方自带的 authorization 与注入值并存时
            # 服务端取到错误的一个
            md = [(k, v) for k, v in (client_call_details.metadata or [])
                  if k.lower() != "authorization"]
            md.append(("authorization", f"Bearer {token}"))
            client_call_details = client_call_details._replace(metadata=md)
        return await continuation(client_call_details, request)


class _DeadlineProxy:
    """给 stub 方法统一注入默认 deadline（grpc.aio 无 channel 级默认超时）。"""

    def __init__(self, stub):
        self._stub = stub

    def __getattr__(self, name):
        fn = getattr(self._stub, name)

        async def wrapper(*args, **kwargs):
            kwargs.setdefault("timeout", _DEFAULT_DEADLINE)
            return await fn(*args, **kwargs)

        return wrapper


class SchedulerClient:
    def __init__(self, addr: str):
        self._channel = grpc.aio.insecure_channel(
            addr, interceptors=[TraceparentInterceptor(), AuthInterceptor()])
        self.stub = _DeadlineProxy(cgrpc.CopilotToolServiceStub(self._channel))

    async def close(self) -> None:
        await self._channel.close()

    @staticmethod
    def ctx(tenant_id: int, user_id: str, request_id: str = "") -> pb.RequestContext:
        return pb.RequestContext(tenant_id=tenant_id, user_id=user_id,
                                 actor="copilot", request_id=request_id)


def to_dict(msg) -> dict:
    """proto → JSON dict（camelCase 键，ID 已字符串化）。"""
    return json_format.MessageToDict(
        msg, preserving_proto_field_name=False,
        use_integers_for_enums=False, always_print_fields_with_no_presence=False)


def parse_struct(d: dict):
    from google.protobuf import struct_pb2
    s = struct_pb2.Struct()
    if d:
        s.update(d)
    return s
