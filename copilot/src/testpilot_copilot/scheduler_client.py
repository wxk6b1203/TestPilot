"""Scheduler CopilotToolService gRPC 客户端（Copilot 不直连 DB —— 全部经此）。"""

from __future__ import annotations

import grpc
from google.protobuf import json_format

from testpilot.common.v1 import types_pb2 as pb
from testpilot.copilot.v1 import copilot_pb2 as cpb
from testpilot.copilot.v1 import copilot_pb2_grpc as cgrpc

from .tracing import TraceparentInterceptor


class SchedulerClient:
    def __init__(self, addr: str):
        self._channel = grpc.aio.insecure_channel(addr, interceptors=[TraceparentInterceptor()])
        self.stub = cgrpc.CopilotToolServiceStub(self._channel)

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
