"""Context：run(ctx) 注入的上下文 —— 变量、环境、HTTP 快捷调用、日志。"""

from __future__ import annotations

from typing import Any

from .bridge import Bridge
from .models import GrpcAPI, HttpAPI, Response
from .page import Page


class _Vars:
    """变量视图：读本地快照，写本地 + 同步桥（运行结束后由 Worker 合并回用例上下文）。"""

    def __init__(self, initial: dict[str, Any], bridge: Bridge):
        self._data = dict(initial)
        self._changed: dict[str, Any] = {}
        self._bridge = bridge

    def __getitem__(self, key: str) -> Any:
        return self._data[key]

    def get(self, key: str, default: Any = None) -> Any:
        return self._data.get(key, default)

    def __setitem__(self, key: str, value: Any) -> None:
        self._data[key] = value
        self._changed[key] = value

    def __contains__(self, key: str) -> bool:
        return key in self._data

    @property
    def changed(self) -> dict[str, Any]:
        return dict(self._changed)


class Context:
    def __init__(self, bridge: Bridge, payload: dict[str, Any]):
        self._bridge = bridge
        self.vars = _Vars(payload.get("vars") or {}, bridge)
        self.base_url: str = payload.get("base_url") or ""
        self.parameters: dict[str, Any] = payload.get("parameters") or {}
        self.tenant_id: int = payload.get("tenant_id") or 0
        # post 脚本上下文：上一个 api_call 的响应快照（status/headers/json/text/elapsed_ms）
        self.response: dict[str, Any] | None = payload.get("response")
        # 按 ID 调用的可用接口清单（ctx.api 判别 http/grpc；错误信息更友好）
        self.http_api_ids: set[str] = set(payload.get("http_api_ids") or [])
        self.grpc_api_ids: set[str] = set(payload.get("grpc_api_ids") or [])
        self._page: Page | None = None

    @property
    def page(self) -> Page:
        """浏览器 Page（v2：经能力桥转发 Playwright 操作，沙箱零网络）。"""
        if self._page is None:
            self._page = Page(self._bridge)
        return self._page

    async def http(self, method: str, uri: str, *,
                   headers: dict[str, str] | None = None,
                   params: dict[str, str] | None = None,
                   body: Any = None, timeout: float = 30.0) -> Response:
        """快捷 HTTP 调用（不建 HttpAPI 类时）。"""
        result = await self._bridge.call("http_request", {
            "method": method.upper(), "uri": uri,
            "headers": headers or {}, "params": params or {},
            "body": body, "timeout": timeout,
        })
        return Response(**result)

    async def set_var(self, key: str, value: Any) -> None:
        self.vars[key] = value
        await self._bridge.call("set_var", {"key": key, "value": value})

    def merge_vars(self, changes: dict[str, Any]) -> None:
        """把桥侧（pre/post 脚本）写回的变量合并进当前上下文并标记为变更。"""
        for k, v in (changes or {}).items():
            self.vars[k] = v

    def http_api(self, api_id: str | int) -> HttpAPI:
        """按 HTTP 接口 ID 获得可执行封装（接口快照由 Scheduler 派发时解析）。"""
        sid = str(api_id)
        if sid not in self.http_api_ids:
            raise ValueError(f"http api {sid} not in this case's http_api_refs")
        return HttpAPI(api_id=sid)

    def grpc_api(self, api_id: str | int) -> GrpcAPI:
        """按 gRPC 接口 ID 获得可执行封装。"""
        sid = str(api_id)
        if sid not in self.grpc_api_ids:
            raise ValueError(f"grpc api {sid} not in this case's grpc_api_refs")
        return GrpcAPI(api_id=sid)

    def api(self, api_id: str | int) -> HttpAPI | GrpcAPI:
        """按接口 ID 获得封装；HTTP/gRPC 自动判别，同 ID 双类型时报歧义。"""
        sid = str(api_id)
        is_http = sid in self.http_api_ids
        is_grpc = sid in self.grpc_api_ids
        if is_http and is_grpc:
            raise ValueError(f"api {sid} is ambiguous: referenced as both http and grpc")
        if is_http:
            return HttpAPI(api_id=sid)
        if is_grpc:
            return GrpcAPI(api_id=sid)
        raise ValueError(
            f"api {sid} not in this case's api refs; declare it in "
            "http_api_refs/grpc_api_refs (dynamic IDs must be declared explicitly)")

    def log(self, message: str) -> None:
        self._bridge.emit({"type": "log", "message": str(message)})
