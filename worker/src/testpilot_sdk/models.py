"""SDK 数据模型（pydantic v2）。

设计见 docs/design.md §6.1：pydantic 提供字段验证，ABC 提供方法契约。
进程内真正的执行由能力桥完成（见 bridge.py），模型只负责描述请求。
"""

from __future__ import annotations

from typing import Any

from pydantic import BaseModel, Field


class Response(BaseModel):
    """HTTP 调用结果。"""

    status: int
    headers: dict[str, str] = Field(default_factory=dict)
    body: Any = None          # JSON 解析结果（非 JSON 时为原文字符串）
    text: str = ""
    elapsed_ms: int = 0


class HttpAPI(BaseModel):
    """HTTP 接口描述；子类固化 method/uri，实例传参后 run()。"""

    method: str = "GET"
    uri: str
    headers: dict[str, str] = Field(default_factory=dict)
    params: dict[str, str] = Field(default_factory=dict)
    body: Any = None          # dict/list → JSON；str → 原文
    timeout: float = 30.0

    async def run(self) -> Response:
        """经能力桥发起调用（Worker 侧真正执行）。"""
        from .bridge import current_bridge

        bridge = current_bridge()
        result = await bridge.call("http_request", {
            "method": self.method.upper(),
            "uri": self.uri,
            "headers": self.headers,
            "params": self.params,
            "body": self.body,
            "timeout": self.timeout,
        })
        return Response(**result)
