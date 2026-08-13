"""Context：run(ctx) 注入的上下文 —— 变量、环境、HTTP 快捷调用、日志。"""

from __future__ import annotations

from typing import Any

from .bridge import Bridge
from .models import Response


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

    def log(self, message: str) -> None:
        self._bridge.emit({"type": "log", "message": str(message)})
