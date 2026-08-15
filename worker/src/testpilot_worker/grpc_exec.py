"""gRPC 反射调用执行：无编译桩动态解析 service/method → 构造消息 → unary 调用。

链路：Scheduler 派发前把步骤的 grpc_api_id 解析进 task.functional.grpc_apis 映射
（Worker 无 DB）；本模块按 GrpcApi.full_service/method 经 server reflection 动态
解析消息类型（descriptor_pool + message_factory），请求体 = request_message ⊕
request_override（深合并）。目标地址取环境 base_url（host[:port]）。
"""

from __future__ import annotations

import asyncio
import threading
from typing import Any, Mapping
from urllib.parse import urlparse

import grpc
from google.protobuf import descriptor_pb2, descriptor_pool, json_format, message_factory
from grpc_reflection.v1alpha import reflection_pb2, reflection_pb2_grpc

from testpilot.common.v1 import types_pb2 as pb


class GrpcCallError(Exception):
    """gRPC 调用失败（引擎层包装为 StepFailure）。"""


# 进程级缓存：channel 按 (target, secure) 复用；描述符按 (target, service, method) 复用
# ——缓存键必须含 target：不同服务器对同名 service/method 可能提供不同消息定义，
# 跨目标共享会导致序列化类串用、解析错乱（M2）。
_channels: dict[tuple[str, bool], grpc.Channel] = {}
_resolved: dict[tuple[str, str, str], tuple[Any, Any, Any]] = {}  # (target, service, method)
_lock = threading.Lock()


def channel_for(target: str, secure: bool) -> grpc.Channel:
    key = (target, secure)
    with _lock:
        ch = _channels.get(key)
        if ch is None:
            if secure:
                opts = None
                ch = grpc.secure_channel(target, grpc.ssl_channel_credentials(), options=opts)
            else:
                ch = grpc.insecure_channel(target)
            _channels[key] = ch
    return ch


def _resolve(channel: grpc.Channel, target: str, service: str, method: str):
    """反射解析出 (MethodDescriptor, 请求消息类, 响应消息类)。"""
    key = (target, service, method)
    with _lock:
        hit = _resolved.get(key)
    if hit is not None:
        return hit

    stub = reflection_pb2_grpc.ServerReflectionStub(channel)
    req = reflection_pb2.ServerReflectionRequest(file_containing_symbol=service)
    resp = next(stub.ServerReflectionInfo(iter([req])))
    if resp.HasField("error_response"):
        raise GrpcCallError(f"reflection error: {resp.error_response.error_message}")
    # grpc-reflection 新版返回序列化后的 FileDescriptorProto（bytes）
    fds = [descriptor_pb2.FileDescriptorProto.FromString(b)
           for b in resp.file_descriptor_response.file_descriptor_proto]
    pool = descriptor_pool.DescriptorPool()
    pending = list(fds)
    while pending:  # 依赖序添加（含 google.protobuf 依赖时循环至稳定）
        progressed = False
        for fd in list(pending):
            try:
                pool.Add(fd)
                pending.remove(fd)
                progressed = True
            except Exception:
                pass
        if not progressed:
            raise GrpcCallError(f"unresolvable descriptors: {[f.name for f in pending]}")

    try:
        svc = pool.FindServiceByName(service)
        m = svc.FindMethodByName(method)
    except KeyError as e:
        raise GrpcCallError(f"service/method not found: {e}") from e
    in_cls = message_factory.GetMessageClass(m.input_type)
    out_cls = message_factory.GetMessageClass(m.output_type)
    entry = (m, in_cls, out_cls)
    with _lock:
        _resolved[key] = entry
    return entry


def call(target: str, api: pb.GrpcApi, request_override: Mapping[str, Any] | None = None,
         metadata_override: list[tuple[str, str]] | None = None,
         timeout_s: float | None = None) -> tuple[dict, dict]:
    """阻塞执行一次 unary 调用，返回 (request_snapshot, response_scope)。"""
    secure = api.tls_settings.enabled
    channel = channel_for(target, secure)
    try:
        _, in_cls, out_cls = _resolve(channel, target, api.full_service, api.method)

        merged: dict[str, Any] = dict(api.request_message or {})
        if request_override:
            _deep_merge(merged, dict(request_override))
        req_msg = json_format.ParseDict(merged, in_cls(), ignore_unknown_fields=True)

        meta = [(kv.key, kv.value) for kv in api.metadata] or None
        if metadata_override:
            meta = (meta or []) + list(metadata_override)

        deadline = timeout_s
        if deadline is None and api.HasField("deadline"):
            deadline = api.deadline.ToTimedelta().total_seconds()
        if deadline is None:
            # 无 deadline 的阻塞调用会挂死 to_thread 线程池（默认 min(32, cpu+4)）——
            # 对黑盒服务器无限等待；强制 30s 默认上限
            deadline = 30.0

        call_fn = channel.unary_unary(
            f"/{api.full_service}/{api.method}",
            request_serializer=in_cls.SerializeToString,
            response_deserializer=out_cls.FromString,
        )
        resp_msg = call_fn(req_msg, metadata=meta, timeout=deadline)
        return (
            {"service": api.full_service, "method": api.method,
             "target": target, "request": merged},
            {"json": json_format.MessageToDict(resp_msg), "status": "OK"},
        )
    except GrpcCallError:
        raise
    except grpc.RpcError as e:
        code = e.code().name if e.code() is not None else "UNKNOWN"
        raise GrpcCallError(f"grpc {api.full_service}.{api.method}: {code}: {e.details()}") from e


async def call_async(target: str, api: pb.GrpcApi,
                     request_override: Mapping[str, Any] | None = None,
                     metadata_override: list[tuple[str, str]] | None = None,
                     timeout_s: float | None = None) -> tuple[dict, dict]:
    """gRPC 调用为阻塞 IO：经线程池执行，不阻塞事件循环。"""
    return await asyncio.to_thread(
        call, target, api, request_override, metadata_override, timeout_s)


def _deep_merge(base: dict, ov: Mapping) -> None:
    for k, v in ov.items():
        if isinstance(v, dict) and isinstance(base.get(k), dict):
            _deep_merge(base[k], v)
        else:
            base[k] = v


def target_from_base_url(base_url: str) -> str:
    """环境 base_url → gRPC 目标 host:port（scheme 剥除；带路径视为配置错误）。"""
    u = urlparse(base_url if "://" in base_url else "//" + base_url)
    if not u.hostname or u.path not in ("", "/"):
        raise GrpcCallError(
            f"grpc target requires env base_url host[:port], got {base_url!r}")
    port = u.port or (443 if u.scheme == "https" else 80)
    return f"{u.hostname}:{port}"
