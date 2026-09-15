"""Copilot FastAPI 入口：/api/chat（Vercel AI SSE）+ 健康检查。

- 鉴权：透传用户 Bearer token → Scheduler /api/v1/me 解析租户/用户
- 会话：X-Session-Id 头；缺失则创建新会话（X-Session-Id 响应头返回）
- 持久化：on_complete 将本轮新消息经 Scheduler REST 落库
- 流控：model_timeout 限制 LLM 首 token/流式 token 间读超时；
  stream_idle_timeout 对整条 SSE 做空闲兜底（超时发 error + done）
"""

from __future__ import annotations

import asyncio
import json
import logging
import time
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
    ThinkingPart,
    ToolCallPart,
    ToolReturnPart,
    UserPromptPart,
)
from pydantic_ai.ui.vercel_ai import VercelAIAdapter

from . import metrics, tracing
from .agent import build_agent
from .config import apply_environ as config_apply
from .config import load
from .scheduler_client import SchedulerClient
from .tools import CopilotDeps

log = logging.getLogger("testpilot.copilot")

# chat 请求体上限（SSE 消息体通常 <100KB；防大 body 占用内存）
_MAX_CHAT_BODY = 1 << 20


def _context_id_header(value: str | None) -> str:
    """规范化前端页面上下文头（X-TP-Project-Id / X-TP-Env-Id）。

    只接受数字 ID（平台 ID 均为 snowflake 数值）；非法值直接忽略，
    避免把任意字符串带进 gRPC 请求构造。
    """
    v = (value or "").strip()
    if not v:
        return ""
    if not v.isdigit() or len(v) > 32:
        log.warning("ignore invalid copilot context header value: %r", v[:64])
        return ""
    return v


@asynccontextmanager
async def lifespan(app: FastAPI):
    settings = load()
    settings.validate()
    app.state.settings = settings
    app.state.agent = build_agent(settings)
    app.state.sched = SchedulerClient(settings.scheduler_grpc)
    app.state.http = httpx.AsyncClient(base_url=settings.scheduler_rest,
                                       timeout=settings.http_timeout)
    log.info("copilot ready: provider=%s model=%s", settings.provider, settings.model)
    yield
    await asyncio.gather(app.state.sched.close(), app.state.http.aclose())
    metrics.shutdown()  # 冲刷未导出的 OTLP 指标（未初始化时 no-op）


app = FastAPI(title="TestPilot Copilot", lifespan=lifespan)


@app.get("/api/healthz")
async def healthz() -> dict:
    s = app.state.settings
    return {"ok": True, "provider": s.provider, "model": s.model}


def attach_auth_stream(response, token: str) -> None:
    """把 JWT 认证上下文转交流式响应：迭代窗口内 set/reset（P0 修复）。

    async generator 执行时使用的是消费方的 context 而非创建时的 context——
    handler 返回前 set/reset 对 agent 执行期（流消费时）完全无效。
    """
    it = getattr(response, "body_iterator", None)
    if it is None:
        return
    from .scheduler_client import auth_token as _auth_var

    async def _resume(inner, tok):
        ctx_tok = _auth_var.set(tok)
        try:
            async for chunk in inner:
                yield chunk
        finally:
            _auth_var.reset(ctx_tok)

    response.body_iterator = _resume(it, token)


def _sse_json(obj: dict) -> bytes:
    """Vercel AI data-stream 协议的 SSE 事件帧（data: {json}\n\n）。"""
    return ("data: " + json.dumps(obj, ensure_ascii=False) + "\n\n").encode("utf-8")


def attach_idle_timeout_stream(response, timeout: float) -> None:
    """SSE 空闲兜底：任意环节（LLM 流/工具/持久化回调）超过 timeout 无输出，
    主动发 error + done 结束连接，避免浏览器一直停在 streaming 状态。"""
    if timeout <= 0:
        return
    it = getattr(response, "body_iterator", None)
    if it is None:
        return

    async def _guard(inner):
        while True:
            try:
                chunk = await asyncio.wait_for(inner.__anext__(), timeout)
            except StopAsyncIteration:
                return
            except asyncio.TimeoutError:
                log.warning("copilot sse idle timeout: no chunk for %gs", timeout)
                error_text = (
                    f"Copilot 超过 {timeout:g} 秒未产生新输出，服务端已主动结束本次请求。"
                    "请缩短输入或稍后重试。")
                yield _sse_json({"type": "error", "errorText": error_text})
                yield _sse_json({"type": "done"})
                return
            yield chunk

    response.body_iterator = _guard(it)


class _RewrittenBodyRequest:
    """请求包装：body 返回净化后的字节，其余属性透传原始 Request。

    pydantic-ai 适配器的 dispatch_request → from_request 只消费
    `await request.body()` 与 `request.headers`；审批服务端锚定（P0）需要把
    解析并净化过的 JSON 再喂回去，故在 handler 内替换 request。
    """

    def __init__(self, inner: Request, payload: bytes):
        self._inner = inner
        self._payload = payload

    def __getattr__(self, name: str):
        return getattr(self._inner, name)

    async def body(self) -> bytes:
        return self._payload


@app.post("/api/chat")
async def chat(request: Request):
    # span 覆盖鉴权/会话/持久化 + 流式 agent 运行全程（body 迭代器收尾时 end）
    started = time.monotonic()
    span, token = tracing.begin_span(dict(request.headers))
    try:
        response = await _chat_inner(request)
    except asyncio.CancelledError as e:
        # handler 被取消（客户端断开等）：流未建立。span 与指标必须就地收尾，
        # 否则 span 泄漏、该轮在 chat_turns 上不可见
        tracing.end_with_error(span, e)
        metrics.observe_turn_failed(started, "cancelled")
        raise
    except Exception as e:
        # 未捕获异常（FastAPI 兜 500）：同上，异常详情进 span
        tracing.end_with_error(span, e)
        metrics.observe_turn_failed(started, "error")
        raise
    finally:
        tracing.detach(token)
    streaming = tracing.attach_stream_end(response, span)
    if not streaming:
        span.end()  # 非流式（错误/提前返回）
    # 指标：流式 → 活跃流 gauge +1、迭代收尾记整轮时长/结果；非流式 → result=rejected
    metrics.observe_turn(response, started)
    return response


async def _chat_inner(request: Request):
    auth = request.headers.get("authorization", "")
    if not auth.lower().startswith("bearer "):
        return JSONResponse({"error": "missing bearer token"}, status_code=401)
    token = auth[7:].strip()
    http: httpx.AsyncClient = app.state.http

    me = await http.get("/api/v1/me",
                         headers=tracing.inject_headers({"Authorization": f"Bearer {token}"}))
    if me.status_code != 200:
        return JSONResponse({"error": "invalid scheduler token"}, status_code=401)
    info = me.json()
    tenant_id = int(info["tenant_id"])
    user_id = str(info["user"]["id"])

    # body 解析前置：非法 JSON → 400（此前抛 JSONDecodeError → 500）；
    # 超大 body 拒绝（防内存占用）
    raw = await request.body()
    if len(raw) > _MAX_CHAT_BODY:
        return JSONResponse({"error": f"body too large (> {_MAX_CHAT_BODY} bytes)"},
                            status_code=413)
    try:
        # 最大 1MB 的 JSON 解析放线程池，避免长 body 阻塞 SSE 事件循环
        body = await asyncio.to_thread(json.loads, raw) if raw else {}
    except ValueError:
        return JSONResponse({"error": "invalid json body"}, status_code=400)

    sid = request.headers.get("x-session-id", "")
    if sid:
        session_id = sid
    else:
        # 仅在有效请求上建 session（此前任何带合法 token 的请求都会先建，
        # 产生垃圾行）
        r = await http.post("/api/v1/copilot/sessions", json={"title": ""},
                            headers=tracing.inject_headers({"Authorization": f"Bearer {token}"}))
        if r.status_code != 200:
            return JSONResponse({"error": f"create session: {r.text}"}, status_code=502)
        session_id = str(r.json()["id"])

    # 审批服务端锚定（P0）：拉一次历史，三处复用（锚定校验 / 用户消息去重 /
    # 未来服务端重建）。拉取失败必须 fail-closed：没有锚定就放行客户端历史，
    # 等于允许伪造"tool call + 已批准回执"零审批执行写工具。
    r = await http.get(f"/api/v1/copilot/sessions/{session_id}/messages",
                       headers=tracing.inject_headers({"Authorization": f"Bearer {token}"}))
    if r.status_code != 200:
        return JSONResponse({"error": f"fetch session history: {r.text}"}, status_code=502)
    history = r.json().get("items", [])

    deps = CopilotDeps(sched=app.state.sched, tenant_id=tenant_id, user_id=user_id,
                       http=http, token=token,
                       probe_session_id=f"chat-{session_id}",
                       ui_project_id=_context_id_header(
                           request.headers.get("x-tp-project-id")),
                       ui_env_id=_context_id_header(
                           request.headers.get("x-tp-env-id")))
    # 校验页面上下文与用户消息去重落库互不依赖：并行执行，
    # 每个续聊请求少等一次「GET 历史消息」的 RTT。
    await asyncio.gather(
        deps.hydrate_ui_context(),
        _persist_incoming_user(app, session_id, token, body, history=history),
    )

    # 工具调用 args / 审批回执一律以服务端落库为准（详见 _sanitize_client_messages）
    _sanitize_client_messages(body, _anchor_map_from_rows(history))
    # 适配器 dispatch_request 会自读 request.body()（不走我们已解析的 dict），
    # 用包装 Request 替换 body 为净化后的字节，其余属性全部透传。
    request = _RewrittenBodyRequest(
        request, json.dumps(body, ensure_ascii=False).encode("utf-8"))

    # 取消路径兜底持久化：idle 超时/服务端取消时 on_complete 不执行，本轮
    # 已完成的工具调用（可能含写副作用）不落库会导致刷新后审批卡重现、
    # 再次批准重复执行。本轮新增消息 = all_messages() 尾部超出 run 输入历史
    # 的部分（pydantic-ai 保留传入 history 前缀，切片对齐）。
    run_history_len = len(VercelAIAdapter.load_messages(
        VercelAIAdapter.build_run_input(
            json.dumps(body, ensure_ascii=False).encode("utf-8")).messages))

    async def on_complete(result):
        await _persist_turn(app, session_id, token, result)

    async def on_cancel(cancelled):
        try:
            await _persist_model_messages(app, session_id, token,
                                          cancelled.all_messages()[run_history_len:])
        except Exception:
            log.exception("persist cancelled turn failed")

    response = await VercelAIAdapter.dispatch_request(
        request,
        agent=app.state.agent,
        sdk_version=7,
        deps=deps,
        on_complete=on_complete,
        on_cancel=on_cancel,
    )
    # gRPC 认证上下文：工具调用经 scheduler_client 注入当前用户的 JWT
    # （Scheduler CopilotAuthUnary 校验 Bearer + RequestContext 一致性）。
    # 必须在流窗口内 set：dispatch_request 返回的是惰性 StreamingResponse，
    # agent 在 body 迭代时才执行（async generator 不继承创建时的 contextvar），
    # handler 内 set/reset 会让工具调用读不到 token → 401。
    attach_auth_stream(response, token)
    attach_idle_timeout_stream(response, app.state.settings.stream_idle_timeout)
    response.headers["X-Session-Id"] = session_id
    return response


async def _persist_incoming_user(app: FastAPI, session_id: str, token: str, body: dict,
                                 history: list[dict] | None = None) -> None:
    """落库用户消息。审批回执会整体重发（trigger 同为 submit-message），按内容去重。

    去重只针对"库中最后一行就是同内容的用户消息"（请求超时重发场景）：
    上一轮已有回复后再发相同文本是用户的真实意图（如连续两条"好的"），
    不能吞。持久化网络异常只记日志——落库失败不应 500 杀死整个 chat。
    """
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
    h = tracing.inject_headers({"Authorization": f"Bearer {token}"})
    if history is None:
        try:
            r = await http.get(f"/api/v1/copilot/sessions/{session_id}/messages", headers=h)
            history = r.json().get("items", []) if r.status_code == 200 else []
        except httpx.HTTPError as e:
            log.warning("persist incoming user: fetch history failed: %s", e)
            history = []
    if history and history[-1].get("role") == 1 and history[-1].get("content") == text:
        return
    try:
        await http.post(f"/api/v1/copilot/sessions/{session_id}/messages",
                        json={"role": 1, "content": text}, headers=h)
    except httpx.HTTPError as e:
        log.warning("persist incoming user failed: %s", e)


def _anchor_map_from_rows(items: list[dict]) -> dict[str, dict[str, Any]]:
    """落库行 → {tool_call_id: {name, args, result}} 审批锚定表。

    兼容两种 tool_calls 形状（_render_rows 同款）：数组（旧/role=3 结果行）|
    {reasoning, calls}（新）。args 落库为 JSON 字符串，此处还原为对象。
    """
    anchors: dict[str, dict[str, Any]] = {}
    for m in items or []:
        raw = m.get("tool_calls")
        if not raw:
            continue
        try:
            parsed = json.loads(raw)
        except (ValueError, TypeError):
            continue
        calls = parsed if isinstance(parsed, list) else (parsed or {}).get("calls") or []
        for tc in calls:
            if not isinstance(tc, dict):
                continue
            tc_id = str(tc.get("tool_call_id") or "")
            if not tc_id:
                continue
            entry = anchors.setdefault(tc_id, {"name": None, "args": None, "result": None})
            if entry["name"] is None and tc.get("name"):
                entry["name"] = str(tc["name"])
            if entry["args"] is None and tc.get("args") is not None:
                args = tc["args"]
                if isinstance(args, str):
                    try:
                        args = json.loads(args)
                    except ValueError:
                        args = None
                entry["args"] = args
            if entry["result"] is None and tc.get("result") is not None:
                entry["result"] = tc["result"]
    return anchors


def _sanitize_client_messages(body: dict, anchors: dict[str, dict[str, Any]]) -> None:
    """审批服务端锚定（P0，原地修改 body["messages"]）。

    合法流程中，模型发起的每个 tool call 都已在 _persist_turn 落库（含
    tool_call_id + args）；而审批回执来自客户端请求体、pydantic-ai 适配器
    按 tool_call_id 配对并直接执行——不锚定的话，任何持 token 者可伪造
    "tool call + approval-responded" 单请求零审批执行写工具，或篡改 args
    （审批卡显示 X、实际执行 Y）。规则：

    - 服务端无记录的工具 part（含审批态）→ 整体丢弃（凭空即伪造）；
    - 有记录 → args/toolName 以落库为准覆写（防 TOCTOU 篡改）；
    - 落库已有 result → 回填 output-available 并摘除 approval（防止
      已执行的调用经重发审批回执被二次执行）。
    """
    for msg in body.get("messages") or []:
        if not isinstance(msg, dict) or msg.get("role") != "assistant":
            continue
        parts = msg.get("parts")
        if not isinstance(parts, list):
            parts = []
        kept: list[Any] = []
        for p in parts:
            if not isinstance(p, dict):
                continue
            ptype = p.get("type")
            if ptype == "dynamic-tool" or (isinstance(ptype, str) and ptype.startswith("tool-")):
                rec = anchors.get(str(p.get("toolCallId") or ""))
                if rec is None:
                    log.warning("dropped unanchored client tool part (toolCallId=%r)",
                                str(p.get("toolCallId"))[:64])
                    continue
                # 名称与参数以服务端为准：类型串里的工具名/动态 toolName 同样强制改写
                if rec["name"]:
                    if ptype == "dynamic-tool":
                        p["toolName"] = rec["name"]
                    else:
                        p["type"] = f"tool-{rec['name']}"
                if rec["args"] is not None:
                    p["input"] = rec["args"]
                if rec["result"] is not None:
                    p["state"] = "output-available"
                    p["output"] = rec["result"]
                    p.pop("approval", None)
            kept.append(p)
        msg["parts"] = kept
    # 伪造 part 全被丢弃的 assistant 消息只剩空 parts，直接整条剔除
    # （pydantic-ai 的 ModelResponse 不接受零 part）
    cleaned: list[Any] = []
    for m in body.get("messages") or []:
        if isinstance(m, dict) and m.get("role") == "assistant" and not m.get("parts"):
            continue
        cleaned.append(m)
    body["messages"] = cleaned


async def _persist_turn(app: FastAPI, session_id: str, token: str, result) -> None:
    """把本轮新增消息落库（user / assistant / tool 三种角色）。"""
    try:
        rows = await asyncio.to_thread(_render_rows, result.new_messages())
    except Exception:
        log.exception("render transcript failed")
        return
    await _persist_rows(app, session_id, token, rows)


async def _persist_model_messages(app: FastAPI, session_id: str, token: str,
                                  messages: list[ModelMessage]) -> None:
    """on_cancel 路径：无 AgentRunResult 包装，直接持久化 ModelMessage 列表。"""
    rows = await asyncio.to_thread(_render_rows, messages)
    await _persist_rows(app, session_id, token, rows)


async def _persist_rows(app: FastAPI, session_id: str, token: str,
                        rows: list[dict[str, Any]]) -> None:
    if not rows:
        return
    http: httpx.AsyncClient = app.state.http
    h = {"Authorization": f"Bearer {token}"}
    # 逐条串行落库：消息行按插入顺序拿 snowflake ID，并发会打乱
    # user/tool-call/tool-result 的 transcript 顺序（前端历史加载按 ID 排序）。
    for row in rows:
        try:
            r = await http.post(f"/api/v1/copilot/sessions/{session_id}/messages", json=row,
                                headers=tracing.inject_headers(h))
        except httpx.HTTPError as e:
            # 持久化失败不应让已完成的流以 error 结尾——记录即可
            log.warning("persist message network error: %s", e)
            continue
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
                        [{"name": part.tool_name, "result": _short(part.content),
                          "tool_call_id": part.tool_call_id}],
                        ensure_ascii=False)})
        elif isinstance(m, ModelResponse):
            text: list[str] = []
            thinking: list[str] = []
            calls: list[dict[str, Any]] = []
            for part in m.parts:
                if isinstance(part, TextPart):
                    text.append(part.content)
                elif isinstance(part, ThinkingPart):
                    # thinking 模型（如 deepseek-v4-flash）要求历史 assistant 消息
                    # 回传 reasoning_content，否则续跑工具调用直接 400（
                    # "The `reasoning_content` in the thinking mode must be passed
                    # back to the API"）。刷新后历史从落库重建，丢了就永久卡死。
                    thinking.append(part.content)
                elif isinstance(part, ToolCallPart):
                    # 落库时保留 tool_call_id：前端重载历史后必须按同一 ID
                    # 重建 call/result 配对，否则多工具调用会被拍平成重复 ID
                    calls.append({"name": part.tool_name,
                                  "args": part.args_as_json_str(),
                                  "tool_call_id": part.tool_call_id})
            if text or calls or thinking:
                row: dict[str, Any] = {"role": 2, "content": "\n".join(text)}
                if calls or thinking:
                    # JSON 形状：数组 = 旧数据兼容；对象 = 新格式（reasoning + calls）。
                    # Scheduler 纯透传，消费方只有前端 openSession（已兼容两种）。
                    row["tool_calls"] = json.dumps(
                        {"reasoning": "\n".join(thinking), "calls": calls},
                        ensure_ascii=False)
                rows.append(row)
    return rows


def _short(content: Any, limit: int = 4000) -> str:
    s = content if isinstance(content, str) else json.dumps(content, ensure_ascii=False, default=str)
    return s[:limit]


def entry(argv: list[str] | None = None) -> None:
    settings = load(argv)
    config_apply(settings)  # CLI 覆盖提升为 env 层，lifespan 重解析得同值；tracing 亦读 env
    host, _, port = settings.http_addr.partition(":")
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s %(levelname)s %(name)s [%(trace_id)s] %(message)s",
    )
    tracing.init()  # otel_exporter（env 已回写）控制；默认关闭
    tracing.attach_log_filter()
    metrics.init()  # 同一套 otel_exporter 开关；默认关闭（no-op 打点）
    uvicorn.run("testpilot_copilot.main:app", host=host or "0.0.0.0",
                port=int(port or 8100), log_level="info")


if __name__ == "__main__":
    entry()
