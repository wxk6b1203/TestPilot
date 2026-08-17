"""SDK 数据模型（pydantic v2）。

设计见 docs/lowcode-api-invocation.md：沙箱内模型是瘦客户端描述，真正执行
由 Worker 能力桥完成。`api_id` 存在时按接口目录快照执行（HttpAPI/GrpcAPI），
不存在时退回 raw 桥操作（HttpAPI 保持向后兼容）。
"""

from __future__ import annotations

from typing import TYPE_CHECKING, Any

from pydantic import BaseModel, Field

if TYPE_CHECKING:
    from .context import Context

_HTTP_FIELDS = {"method", "uri", "headers", "params", "cookies", "body", "binary_ref", "timeout"}
_GRPC_FIELDS = {"full_service", "method", "request", "metadata"}


class Response(BaseModel):
    """HTTP 调用结果（raw ctx.http 与按 ID 调用共用）。"""

    status: int = 0
    headers: dict[str, str] = Field(default_factory=dict)
    body: Any = None          # JSON 解析结果（非 JSON 时为原文字符串）
    text: str = ""
    elapsed_ms: int = 0
    api_id: str | None = None
    request: dict[str, Any] | None = None


class GrpcResponse(BaseModel):
    """gRPC unary 调用结果（声明式断言作用域为 $.json，故保留该访问名）。"""

    status: str = "OK"
    data: dict[str, Any] = Field(default_factory=dict, alias="json")
    request: dict[str, Any] | None = None
    elapsed_ms: int = 0
    api_id: str | None = None

    @property
    def json(self) -> dict[str, Any]:
        """与声明式 GRPC_CALL 响应作用域一致：resp.json 直接取返回体。"""
        return self.data


def _explicit_fields(model: BaseModel, extra: dict[str, Any]) -> dict[str, Any]:
    """仅取显式设置的字段：Pydantic model_fields_set + run() 的 kwargs。

    默认值不进入 override，避免覆盖接口目录快照（例如 timeout=None 不会把
    快照的 timeout 改掉）。
    """
    data = model.model_dump(exclude_unset=True)
    for k, v in extra.items():
        if k in type(model).model_fields:
            data[k] = v
    return data


class HttpAPI(BaseModel):
    """HTTP 接口描述。

    - `api_id` 存在：Worker 按派发时接口快照执行，实例字段/kwargs 只作为 override；
    - 无 `api_id`：兼容旧版 raw 调用，等价于 `ctx.http(...)`。
    """

    api_id: str | None = None
    method: str | None = None
    uri: str | None = None
    headers: dict[str, str] = Field(default_factory=dict)
    params: dict[str, str] = Field(default_factory=dict)
    cookies: dict[str, str] = Field(default_factory=dict)
    body: Any = None
    binary_ref: str | None = None
    timeout: float | None = None

    async def run(self, *, ctx: "Context | None" = None, **overrides: Any) -> Response:
        """发起调用。`overrides` 与显式实例字段一起作为接口快照的 override。"""
        from .bridge import current_bridge, get_current_context
        from .context import Context

        bridge = current_bridge()
        ctx = ctx if ctx is not None else get_current_context()
        if not isinstance(ctx, Context):
            ctx = None
        api_id = overrides.pop("api_id", None) or self.api_id
        data = _explicit_fields(self, overrides)
        if api_id:
            http_overrides = {k: data[k] for k in _HTTP_FIELDS if k in data}
            result = await bridge.call("api_request", {
                "kind": "http",
                "api_id": str(api_id),
                "vars": dict(ctx.vars._data) if ctx is not None else {},
                "overrides": http_overrides,
            })
            resp = Response(**result["response"])
            if ctx is not None and result.get("vars"):
                ctx.merge_vars(result["vars"])
            return resp

        # raw 兼容路径：无 api_id 时必须给 method/uri
        method = data.get("method") or self.method or "GET"
        uri = data.get("uri") or self.uri
        if not uri:
            raise ValueError("HttpAPI raw call requires uri (or set api_id)")
        result = await bridge.call("http_request", {
            "method": method.upper(),
            "uri": uri,
            "headers": data.get("headers") or self.headers or {},
            "params": data.get("params") or self.params or {},
            "body": data.get("body", self.body),
            "timeout": float(data.get("timeout") or self.timeout or 30),
        })
        return Response(**result)


class GrpcAPI(BaseModel):
    """gRPC 接口描述。

    - `api_id` 存在：按派发时 gRPC 接口快照执行，request 深合并、metadata 追加；
    - 无 `api_id`：raw reflection 调用（full_service/method 必填）。
    """

    api_id: str | None = None
    full_service: str | None = None
    method: str | None = None
    request: dict[str, Any] = Field(default_factory=dict)
    metadata: dict[str, str] = Field(default_factory=dict)

    async def run(self, *, ctx: "Context | None" = None, **overrides: Any) -> GrpcResponse:
        from .bridge import current_bridge, get_current_context
        from .context import Context

        bridge = current_bridge()
        ctx = ctx if ctx is not None else get_current_context()
        if not isinstance(ctx, Context):
            ctx = None
        api_id = overrides.pop("api_id", None) or self.api_id
        data = _explicit_fields(self, overrides)
        if api_id:
            grpc_overrides = {k: data[k] for k in _GRPC_FIELDS if k in data}
            result = await bridge.call("api_request", {
                "kind": "grpc",
                "api_id": str(api_id),
                "vars": dict(ctx.vars._data) if ctx is not None else {},
                "overrides": grpc_overrides,
            })
            resp = GrpcResponse(**result["response"])
            if ctx is not None and result.get("vars"):
                ctx.merge_vars(result["vars"])
            return resp

        service = data.get("full_service") or self.full_service
        method = data.get("method") or self.method
        if not service or not method:
            raise ValueError("GrpcAPI raw call requires full_service and method (or set api_id)")
        result = await bridge.call("grpc_request", {
            "full_service": service,
            "method": method,
            "request": data.get("request") or self.request or {},
            "metadata": data.get("metadata") or self.metadata or {},
        })
        return GrpcResponse(**result)
