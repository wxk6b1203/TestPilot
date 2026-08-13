"""HTTP 请求构建与执行（httpx.AsyncClient）。"""

from __future__ import annotations

import json
import time
from typing import Any, Mapping

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


async def execute(client: httpx.AsyncClient, api: pb.HttpApi, base_url: str,
                  scope: Mapping[str, Any]) -> tuple[dict[str, Any], dict[str, Any], dict[str, Any]]:
    """执行请求，返回 (request_snapshot, response_snapshot, response_scope)。"""
    kwargs, req_snap = build_request(api, base_url, scope)
    egress.check_url(kwargs["url"])
    started = time.perf_counter()
    resp = await client.request(**kwargs)
    elapsed_ms = int((time.perf_counter() - started) * 1000)

    raw = resp.content[:_BODY_SNAPSHOT_LIMIT]
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
