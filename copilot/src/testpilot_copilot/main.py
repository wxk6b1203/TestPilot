"""Copilot FastAPI 入口：/api/chat（Vercel AI SSE）+ 健康检查。

- 鉴权：透传用户 Bearer token → Scheduler /api/v1/me 解析租户/用户
- 会话：X-Session-Id 头；缺失则创建新会话（X-Session-Id 响应头返回）
- 持久化：on_complete 将本轮新消息经 Scheduler REST 落库
"""

from __future__ import annotations

import json
import logging
from contextlib import asynccontextmanager
from typing import Any

import httpx
import uvicorn
from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse
from pydantic_ai.messages import (
    ModelMessage,
    ModelRequest,
    ModelResponse,
    TextPart,
    ToolCallPart,
    ToolReturnPart,
    UserPromptPart,
)
from pydantic_ai.ui.vercel_ai import VercelAIAdapter

from . import tracing
from .agent import build_agent
from .config import load
from .scheduler_client import SchedulerClient
from .tools import CopilotDeps

log = logging.getLogger("testpilot.copilot")


@asynccontextmanager
async def lifespan(app: FastAPI):
    settings = load()
    settings.validate()
    app.state.settings = settings
    app.state.agent = build_agent(settings)
    app.state.sched = SchedulerClient(settings.scheduler_grpc)
    app.state.http = httpx.AsyncClient(base_url=settings.scheduler_rest, timeout=15.0)
    log.info("copilot ready: provider=%s model=%s", settings.provider, settings.model)
    yield
    await app.state.sched.close()
    await app.state.http.aclose()


app = FastAPI(title="TestPilot Copilot", lifespan=lifespan)


@app.get("/api/healthz")
async def healthz() -> dict:
    s = app.state.settings
    return {"ok": True, "provider": s.provider, "model": s.model}


@app.post("/api/chat")
async def chat(request: Request):
    # span 覆盖鉴权/会话/持久化 + 流式 agent 运行全程（body 迭代器收尾时 end）
    span, token = tracing.begin_span(dict(request.headers))
    try:
        response = await _chat_inner(request)
    finally:
        tracing.detach(token)
    if not tracing.attach_stream_end(response, span):
        span.end()  # 非流式（错误/提前返回）
    return response


async def _chat_inner(request: Request):
    auth = request.headers.get("authorization", "")
    if not auth.lower().startswith("bearer "):
        return JSONResponse({"error": "missing bearer token"}, status_code=401)
    token = auth[7:].strip()
    http: httpx.AsyncClient = app.state.http

    me = await http.get("/api/v1/me", headers={"Authorization": f"Bearer {token}"})
    if me.status_code != 200:
        return JSONResponse({"error": "invalid scheduler token"}, status_code=401)
    info = me.json()
    tenant_id = int(info["tenant_id"])
    user_id = str(info["user"]["id"])

    sid = request.headers.get("x-session-id", "")
    if sid:
        session_id = sid
    else:
        r = await http.post("/api/v1/copilot/sessions", json={"title": ""},
                            headers={"Authorization": f"Bearer {token}"})
        if r.status_code != 200:
            return JSONResponse({"error": f"create session: {r.text}"}, status_code=502)
        session_id = str(r.json()["id"])

    deps = CopilotDeps(sched=app.state.sched, tenant_id=tenant_id, user_id=user_id,
                       http=http, token=token)

    body = await request.json()
    await _persist_incoming_user(app, session_id, token, body)

    async def on_complete(result):
        await _persist_turn(app, session_id, token, result)

    response = await VercelAIAdapter.dispatch_request(
        request,
        agent=app.state.agent,
        sdk_version=6,
        deps=deps,
        on_complete=on_complete,
    )
    response.headers["X-Session-Id"] = session_id
    return response


async def _persist_incoming_user(app: FastAPI, session_id: str, token: str, body: dict) -> None:
    """落库用户消息。审批回执会整体重发（trigger 同为 submit-message），按内容去重。"""
    if body.get("trigger") != "submit-message":
        return
    messages = body.get("messages") or []
    if not messages or messages[-1].get("role") != "user":
        return
    text = "".join(p.get("text", "") for p in messages[-1].get("parts", [])
                   if p.get("type") == "text")
    if not text.strip():
        return
    http: httpx.AsyncClient = app.state.http
    h = {"Authorization": f"Bearer {token}"}
    r = await http.get(f"/api/v1/copilot/sessions/{session_id}/messages", headers=h)
    if r.status_code == 200:
        existing = [m for m in r.json().get("items", []) if m.get("role") == 1]
        if existing and existing[-1].get("content") == text:
            return
    await http.post(f"/api/v1/copilot/sessions/{session_id}/messages",
                    json={"role": 1, "content": text}, headers=h)


async def _persist_turn(app: FastAPI, session_id: str, token: str, result) -> None:
    """把本轮新增消息落库（user / assistant / tool 三种角色）。"""
    try:
        rows = _render_rows(result.new_messages())
    except Exception:
        log.exception("render transcript failed")
        return
    if not rows:
        return
    http: httpx.AsyncClient = app.state.http
    h = {"Authorization": f"Bearer {token}"}
    for row in rows:
        r = await http.post(f"/api/v1/copilot/sessions/{session_id}/messages", json=row, headers=h)
        if r.status_code != 200:
            log.warning("persist message failed: %s %s", r.status_code, r.text[:200])


def _render_rows(messages: list[ModelMessage]) -> list[dict[str, Any]]:
    """ModelMessage → CopilotMessage 行（role: 1=user 2=assistant 3=tool）。"""
    rows: list[dict[str, Any]] = []
    for m in messages:
        if isinstance(m, ModelRequest):
            for part in m.parts:
                if isinstance(part, UserPromptPart) and isinstance(part.content, str):
                    rows.append({"role": 1, "content": part.content})
                elif isinstance(part, ToolReturnPart):
                    rows.append({"role": 3, "content": "", "tool_calls": json.dumps(
                        [{"name": part.tool_name, "result": _short(part.content)}],
                        ensure_ascii=False)})
        elif isinstance(m, ModelResponse):
            text: list[str] = []
            calls: list[dict[str, Any]] = []
            for part in m.parts:
                if isinstance(part, TextPart):
                    text.append(part.content)
                elif isinstance(part, ToolCallPart):
                    calls.append({"name": part.tool_name, "args": part.args_as_json_str()})
            if text or calls:
                row: dict[str, Any] = {"role": 2, "content": "\n".join(text)}
                if calls:
                    row["tool_calls"] = json.dumps(calls, ensure_ascii=False)
                rows.append(row)
    return rows


def _short(content: Any, limit: int = 4000) -> str:
    s = content if isinstance(content, str) else json.dumps(content, ensure_ascii=False, default=str)
    return s[:limit]


def entry() -> None:
    settings = load()
    host, _, port = settings.http_addr.partition(":")
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s %(levelname)s %(name)s [%(trace_id)s] %(message)s",
    )
    tracing.init()  # TP_OTEL_EXPORTER 控制；默认关闭
    tracing.attach_log_filter()
    uvicorn.run("testpilot_copilot.main:app", host=host or "0.0.0.0",
                port=int(port or 8100), log_level="info")


if __name__ == "__main__":
    entry()
