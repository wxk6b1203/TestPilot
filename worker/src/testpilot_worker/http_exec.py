"""HTTP 请求构建与执行（httpx.AsyncClient）。

安全（P0 修复）：
- 重定向逐跳校验：每跳 target 都过 egress 出口策略（原实现只校验初始 URL，
  302 到 127.0.0.1/metadata 可直接穿透白名单/私网阻断）；
- 响应体流式限读（原实现先全量下载再截断快照，大响应可 OOM Worker）。
"""

from __future__ import annotations

import json
import time
from typing import Any, Mapping

import httpcore
import httpx

from testpilot.common.v1 import types_pb2 as pb

from . import egress
from .expr import render, render_map

_METHODS = {
    pb.HTTP_METHOD_GET: "GET",
    pb.HTTP_METHOD_POST: "POST",
    pb.HTTP_METHOD_PUT: "PUT",
    pb.HTTP_METHOD_DELETE: "DELETE",
    pb.HTTP_METHOD_PATCH: "PATCH",
    pb.HTTP_METHOD_HEAD: "HEAD",
    pb.HTTP_METHOD_OPTIONS: "OPTIONS",
}

_BODY_SNAPSHOT_LIMIT = 64 * 1024
_REDIRECT_CODES = {301, 302, 303, 307, 308}
_MAX_REDIRECTS = 10


class PinnedAsyncHTTPTransport(httpx.AsyncHTTPTransport):
    """httpx 0.28 的 AsyncHTTPTransport 不暴露 network_backend 注入点。

    复用其请求/响应协议，仅替换底层连接池的 network_backend 为出口策略后端
    （egress.EgressPinnedBackend），实现 DNS 解析与连接同一次完成。
    """

    def __init__(self, verify: bool = True):
        super().__init__(verify=verify)
        limits = httpx.Limits()
        self._pool = httpcore.AsyncConnectionPool(
            ssl_context=httpx.create_ssl_context(verify=verify),
            max_connections=limits.max_connections,
            max_keepalive_connections=limits.max_keepalive_connections,
            keepalive_expiry=limits.keepalive_expiry,
            network_backend=egress.EgressPinnedBackend(),
        )


def pinned_transport() -> httpx.AsyncHTTPTransport:
    """声明式/低代码共用出口：连接层解析并绑定允许 IP（防 DNS rebinding TOCTOU）。"""
    return PinnedAsyncHTTPTransport()



def _render_body(body: pb.BodySpec, scope: Mapping[str, Any]) -> dict[str, Any]:
    """BodySpec → httpx 请求关键字（content/data）。"""
    which = body.WhichOneof("content")
    if which == "raw":
        raw = render(body.raw, scope)
        if body.content_type == pb.BODY_CONTENT_TYPE_JSON:
            # 模板渲染后按 JSON 规整（校验 + 去空白），失败则按原文发送
            try:
                return {"content": json.dumps(json.loads(raw), ensure_ascii=False)}
            except (ValueError, TypeError):
                return {"content": raw}
        return {"content": raw}
    if which == "form":
        data = {}
        for f in body.form.fields:
            data[render(f.key, scope)] = render(f.value, scope)
        if body.content_type == pb.BODY_CONTENT_TYPE_X_WWW_FORM_URLENCODED:
            return {"data": data}
        return {"files": {k: (None, v) for k, v in data.items()}}  # multipart
    return {}


def build_request(api: pb.HttpApi, base_url: str, scope: Mapping[str, Any]) -> tuple[dict[str, Any], dict[str, Any]]:
    """构建 httpx 请求参数与请求快照。返回 (httpx_kwargs, snapshot)。"""
    method = _METHODS.get(api.method, "GET")
    uri = render(api.uri, scope)
    if uri.startswith("http://") or uri.startswith("https://"):
        url = uri
    else:
        url = base_url.rstrip("/") + "/" + uri.lstrip("/")

    params = render_map(api.params, scope)
    headers = render_map(api.headers, scope)
    kwargs: dict[str, Any] = {"method": method, "url": url}
    if params:
        kwargs["params"] = params
    if headers:
        kwargs["headers"] = headers
    if api.HasField("body"):
        kwargs.update(_render_body(api.body, scope))
        if api.body.WhichOneof("content") == "raw" and api.body.content_type == pb.BODY_CONTENT_TYPE_JSON:
            kwargs.setdefault("headers", {})
            headers_lower = {k.lower() for k in kwargs.get("headers", {})}
            if "content-type" not in headers_lower:
                kwargs["headers"]["Content-Type"] = "application/json"

    timeout = 30.0
    follow = True
    if api.HasField("settings"):
        st = api.settings
        if st.HasField("timeout"):
            timeout = max(st.timeout.ToSeconds() or 30, 1)
        follow = bool(st.follow_redirects)
    kwargs["timeout"] = timeout
    kwargs["follow_redirects"] = follow

    snapshot = {
        "method": method,
        "url": str(httpx.URL(url, params=params)) if params else url,
        "headers": headers,
        "body": kwargs.get("content") or kwargs.get("data") or kwargs.get("files") or "",
    }
    return kwargs, snapshot


async def request_limited(client: httpx.AsyncClient, kwargs: dict[str, Any],
                          body_limit: int) -> tuple[httpx.Response, bytes]:
    """发起请求并按 body_limit 流式限读响应体（防大响应 OOM）。

    follow_redirects 由 kwargs 控制；返回 (response, body_bytes)。
    """
    kwargs = dict(kwargs)
    follow = bool(kwargs.pop("follow_redirects", False))
    url = str(kwargs["url"])
    method = str(kwargs.get("method") or "GET")
    for _hop in range(_MAX_REDIRECTS + 1):
        await egress.acheck_url(url)  # 每跳出口校验（重定向目标不可绕过）
        hop_kwargs = {**kwargs, "url": url, "method": method, "follow_redirects": False}
        async with client.stream(**hop_kwargs) as resp:
            if not (follow and resp.status_code in _REDIRECT_CODES):
                chunks: list[bytes] = []
                total = 0
                async for chunk in resp.aiter_bytes():
                    if total >= body_limit:
                        break
                    take = chunk[: body_limit - total]
                    chunks.append(take)
                    total += len(take)
                return resp, b"".join(chunks)
            loc = resp.headers.get("location")
            if not loc:
                return resp, b""
        url = str(httpx.URL(url).join(loc))
        # 301/302/303 对非 GET/HEAD：转为 GET 并丢弃 body（与 httpx 语义一致）
        if resp.status_code in (301, 302, 303) and method not in ("GET", "HEAD"):
            method = "GET"
            for k in ("content", "data", "files"):
                kwargs.pop(k, None)
    raise httpx.TooManyRedirects("exceeded redirect limit")


async def execute(client: httpx.AsyncClient, api: pb.HttpApi, base_url: str,
                  scope: Mapping[str, Any]) -> tuple[dict[str, Any], dict[str, Any], dict[str, Any]]:
    """执行请求，返回 (request_snapshot, response_snapshot, response_scope)。"""
    kwargs, req_snap = build_request(api, base_url, scope)
    started = time.perf_counter()
    resp, raw = await request_limited(client, kwargs, _BODY_SNAPSHOT_LIMIT)
    elapsed_ms = int((time.perf_counter() - started) * 1000)

    try:
        body_text = raw.decode(resp.encoding or "utf-8", errors="replace")
    except LookupError:
        body_text = raw.decode("utf-8", errors="replace")

    parsed: Any = None
    if body_text:
        try:
            parsed = json.loads(body_text)
        except ValueError:
            parsed = None

    resp_snap = {
        "status": resp.status_code,
        "headers": dict(resp.headers),
        "body": body_text,
        "elapsed_ms": elapsed_ms,
    }
    resp_scope = {
        "status": resp.status_code,
        "headers": {k.lower(): v for k, v in resp.headers.items()},
        "body": parsed if parsed is not None else body_text,
        "json": parsed,
        "text": body_text,
        "elapsed_ms": elapsed_ms,
    }
    return req_snap, resp_snap, resp_scope
