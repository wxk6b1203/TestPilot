"""低代码按接口 ID 调用执行器（docs/lowcode-api-invocation.md）。

Worker 侧实现桥 op=api_request：
- 接口快照由 Scheduler 派发前解析进 FunctionalTask.http_apis / grpc_apis；
- override 与声明式 HttpOverride/GrpcCallStep 语义一致；
- HTTP 继承接口级 cookies/TLS/JSONC/binary_ref/pre/post 脚本；
- pre/post 脚本运行在独立沙箱（只开放 raw http_request，防 api→pre→api 递归）。
"""

from __future__ import annotations

import json
import time
from typing import Any, Mapping

import httpx
from google.protobuf import json_format

from testpilot.common.v1 import types_pb2 as pb

from . import grpc_exec, http_exec
from .sandbox import SubprocessBackend, bridge_http_handler

_MAX_BRIDGE_API_VARS = 4096  # 每次 api_request 携带的变量快照条目上限


class LowCodeApiError(Exception):
    """按 ID 调用的业务错误（经能力桥返回给脚本）。"""


_HTTP_METHODS = {
    "GET": pb.HTTP_METHOD_GET,
    "POST": pb.HTTP_METHOD_POST,
    "PUT": pb.HTTP_METHOD_PUT,
    "DELETE": pb.HTTP_METHOD_DELETE,
    "PATCH": pb.HTTP_METHOD_PATCH,
    "HEAD": pb.HTTP_METHOD_HEAD,
    "OPTIONS": pb.HTTP_METHOD_OPTIONS,
}


def _merge_kv_list(existing: Any, overrides: Mapping[str, Any], case_insensitive: bool) -> None:
    if not overrides:
        return
    keys = set()
    for kv in existing:
        keys.add(kv.key.lower() if case_insensitive else kv.key)
    for k, v in overrides.items():
        target = k.lower() if case_insensitive else k
        if target in keys:
            for kv in existing:
                if (kv.key.lower() if case_insensitive else kv.key) == target:
                    kv.value = str(v)
                    break
        else:
            existing.add(key=k, value=str(v))
            keys.add(target)


def _apply_http_override(api: pb.HttpApi, ov: Mapping[str, Any]) -> pb.HttpApi:
    """HttpAPI 快照 + SDK override（与声明式 HttpOverride 合并语义一致）。"""
    out = pb.HttpApi()
    out.CopyFrom(api)
    if not ov:
        return out
    if ov.get("method"):
        method = str(ov["method"]).upper()
        if method not in _HTTP_METHODS:
            raise LowCodeApiError(f"unsupported http method: {method}")
        out.method = _HTTP_METHODS[method]
    if ov.get("uri") is not None:
        out.uri = str(ov["uri"])
    _merge_kv_list(out.headers, ov.get("headers"), case_insensitive=True)
    _merge_kv_list(out.params, ov.get("params"), case_insensitive=False)
    _merge_kv_list(out.cookies, ov.get("cookies"), case_insensitive=False)

    if "binary_ref" in ov and ov.get("binary_ref") is not None:
        body = pb.BodySpec(content_type=pb.BODY_CONTENT_TYPE_BINARY)
        body.binary_ref = str(ov["binary_ref"])
        out.body.CopyFrom(body)
    elif "body" in ov and ov.get("body") is not None:
        value = ov["body"]
        old_type = out.body.content_type if out.HasField("body") else pb.BODY_CONTENT_TYPE_JSON
        if old_type == pb.BODY_CONTENT_TYPE_UNSPECIFIED:
            old_type = pb.BODY_CONTENT_TYPE_JSON
        raw = value if isinstance(value, str) else json.dumps(value, ensure_ascii=False)
        body = pb.BodySpec(content_type=old_type)
        body.raw = raw
        out.body.CopyFrom(body)

    if ov.get("timeout") is not None:
        timeout_ms = max(int(float(ov["timeout"]) * 1000), 100)
        if not out.HasField("settings"):
            out.settings.CopyFrom(pb.ApiSettings())
        out.settings.timeout.FromMilliseconds(timeout_ms)
    return out


class LowCodeApiCaller:
    """持有低代码任务按 ID 调用所需的全部快照/客户端/脚本后端。

    HTTP 与 gRPC 共用一个实例；functional 与 stress behavior 任务都能构造。
    """

    def __init__(self, base_url: str, tenant_id: int, auto_headers: Mapping[str, str],
                 http_apis: Mapping[str, pb.HttpApi] | None = None,
                 grpc_apis: Mapping[str, pb.GrpcApi] | None = None,
                 inline_files: Mapping[str, bytes] | None = None,
                 parameters: Mapping[str, Any] | None = None,
                 timeout_s: float = 120.0):
        self.base_url = base_url
        self.tenant_id = tenant_id
        self.auto_headers = {k: v for k, v in auto_headers.items()}
        self.http_apis = dict(http_apis or {})
        self.grpc_apis = dict(grpc_apis or {})
        self.inline_files = dict(inline_files or {})
        self.parameters = dict(parameters or {})
        self.timeout_s = max(timeout_s, 1)
        self.logs: list[str] = []
        self.last_response: dict[str, Any] | None = None
        self.client = httpx.AsyncClient(verify=True, transport=http_exec.pinned_transport())
        self.insecure_client = httpx.AsyncClient(verify=False, transport=http_exec.pinned_transport())
        self._script_backend = SubprocessBackend(
            lambda args: bridge_http_handler(self.client, self.base_url, args, self.auto_headers))

    async def close(self) -> None:
        await self.client.aclose()
        await self.insecure_client.aclose()

    def _client_for(self, api: pb.HttpApi) -> httpx.AsyncClient:
        if (api.HasField("settings") and api.settings.HasField("tls_verify")
                and not api.settings.tls_verify):
            return self.insecure_client
        return self.client

    def _script_timeout(self) -> float:
        return min(self.timeout_s, 300.0)

    def _task_vars(self, vars_in: Mapping[str, Any]) -> dict[str, Any]:
        if len(vars_in) > _MAX_BRIDGE_API_VARS:
            raise LowCodeApiError(
                f"api_request vars exceed {_MAX_BRIDGE_API_VARS} entries")
        return {str(k): v for k, v in vars_in.items()}

    async def _run_scripts(self, scripts: Any, phase: str, vars_in: Mapping[str, Any],
                           response: Mapping[str, Any] | None = None) -> dict[str, Any]:
        """运行接口级 pre/post 脚本，返回写入的变量（跨脚本按序可见）。"""
        changed: dict[str, Any] = {}
        current = self._task_vars(vars_in)
        for sc in scripts:
            if not sc.source.strip() or not sc.enabled:
                continue
            payload: dict[str, Any] = {
                "vars": current,
                "base_url": self.base_url,
                "parameters": self.parameters,
                "tenant_id": self.tenant_id,
            }
            if response is not None:
                payload["response"] = dict(response)
            res = await self._script_backend.run(
                sc.source, "run", payload, timeout_s=self._script_timeout())
            self.logs.extend(res.logs[-50:])
            if res.vars:
                current.update(res.vars)
                changed.update(res.vars)
            if not res.ok:
                tail = res.error.strip().splitlines()
                raise LowCodeApiError(
                    f"{phase} script failed: {tail[-1] if tail else 'unknown'}")
        return changed

    def _inject_auto_headers(self, api: pb.HttpApi) -> None:
        existing = {kv.key.lower() for kv in api.headers}
        for k, v in self.auto_headers.items():
            if k.lower() not in existing:
                api.headers.add(key=k, value=v)

    async def call_http(self, api_id: str, overrides: Mapping[str, Any],
                        vars_in: Mapping[str, Any]) -> dict[str, Any]:
        api = self.http_apis.get(str(api_id))
        if api is None:
            raise LowCodeApiError(
                f"http api {api_id} not in task.http_apis; declare it in http_api_refs")
        api = _apply_http_override(api, overrides or {})

        vars_now = self._task_vars(vars_in)
        changed: dict[str, Any] = {}
        changed.update(await self._run_scripts(api.pre_scripts, "pre", vars_now))
        vars_now.update(changed)

        self._inject_auto_headers(api)
        scope: dict[str, Any] = {**vars_now, "vars": vars_now}
        if self.last_response is not None:
            scope["response"] = self.last_response

        req_snap, resp_snap, resp_scope = await http_exec.execute(
            self._client_for(api), api, self.base_url, scope, self.inline_files)
        self.last_response = resp_scope
        self.logs.append(
            f"{req_snap['method']} {req_snap['url']} -> {resp_snap['status']} "
            f"({resp_snap['elapsed_ms']}ms)")

        changed.update(await self._run_scripts(api.post_scripts, "post", vars_now,
                                               response=resp_scope))
        return {
            "kind": "http",
            "response": {
                "status": resp_snap["status"],
                "headers": resp_snap["headers"],
                "body": resp_scope.get("body"),
                "text": resp_snap["body"],
                "elapsed_ms": resp_snap["elapsed_ms"],
                "api_id": str(api_id),
                "request": req_snap,
            },
            "vars": changed,
        }

    async def call_grpc(self, api_id: str, overrides: Mapping[str, Any],
                        _vars_in: Mapping[str, Any]) -> dict[str, Any]:
        api = self.grpc_apis.get(str(api_id))
        if api is None:
            raise LowCodeApiError(
                f"grpc api {api_id} not in task.grpc_apis; declare it in grpc_api_refs")
        ov = overrides or {}
        request_override = ov.get("request") if isinstance(ov.get("request"), dict) else None
        metadata_override = None
        if isinstance(ov.get("metadata"), dict):
            metadata_override = [(str(k), str(v)) for k, v in ov["metadata"].items()]
        target = grpc_exec.target_from_base_url(self.base_url)
        timeout = min(max(self.timeout_s, 5), 30.0)
        started = time.perf_counter()
        try:
            req_snap, resp_scope = await grpc_exec.call_async(
                target, api, request_override=request_override,
                metadata_override=metadata_override, timeout_s=timeout)
        except grpc_exec.GrpcCallError as e:
            raise LowCodeApiError(f"grpc call {api_id}: {e}") from e
        elapsed_ms = int((time.perf_counter() - started) * 1000)
        self.last_response = resp_scope
        self.logs.append(f"grpc {api.full_service}.{api.method} -> {target}")
        return {
            "kind": "grpc",
            "response": {
                "status": resp_scope.get("status", "OK"),
                "json": resp_scope.get("json", {}),
                "request": req_snap.get("request", req_snap),
                "elapsed_ms": elapsed_ms,
                "api_id": str(api_id),
            },
            "vars": {},
        }

    async def raw_grpc(self, args: dict[str, Any]) -> dict[str, Any]:
        """raw 桥 op=grpc_request（无 api_id 的 GrpcAPI 调用）。"""
        api = pb.GrpcApi(full_service=str(args.get("full_service") or ""),
                         method=str(args.get("method") or ""))
        if not api.full_service or not api.method:
            raise LowCodeApiError("grpc_request requires full_service and method")
        if isinstance(args.get("request"), dict):
            api.request_message.update(args["request"])
        if isinstance(args.get("metadata"), dict):
            for k, v in args["metadata"].items():
                api.metadata.add(key=str(k), value=str(v))
        target = grpc_exec.target_from_base_url(self.base_url)
        timeout = min(max(self.timeout_s, 5), 30.0)
        started = time.perf_counter()
        try:
            req_snap, resp_scope = await grpc_exec.call_async(target, api, timeout_s=timeout)
        except grpc_exec.GrpcCallError as e:
            raise LowCodeApiError(f"grpc request: {e}") from e
        elapsed_ms = int((time.perf_counter() - started) * 1000)
        self.logs.append(f"grpc {api.full_service}.{api.method} -> {target}")
        return {
            "status": resp_scope.get("status", "OK"),
            "json": resp_scope.get("json", {}),
            "request": req_snap.get("request", req_snap),
            "elapsed_ms": elapsed_ms,
        }

    async def handle(self, args: dict[str, Any], merged_vars: dict[str, Any],
                     _payload: dict[str, Any]) -> dict[str, Any]:
        """SubprocessBackend.api_handler：op=api_request。"""
        kind = str(args.get("kind") or "http").lower()
        api_id = str(args.get("api_id") or "")
        if not api_id:
            raise LowCodeApiError("api_request requires api_id")
        vars_in = args.get("vars") if isinstance(args.get("vars"), dict) else merged_vars
        overrides = args.get("overrides") if isinstance(args.get("overrides"), dict) else {}
        if kind == "http":
            return await self.call_http(api_id, overrides, vars_in)
        if kind == "grpc":
            return await self.call_grpc(api_id, overrides, vars_in)
        raise LowCodeApiError(f"unsupported api_request kind: {kind}")


def parameters_to_dict(lc: pb.LowCodeCase) -> dict[str, Any]:
    if lc.HasField("parameters"):
        return json_format.MessageToDict(lc.parameters) or {}
    return {}
