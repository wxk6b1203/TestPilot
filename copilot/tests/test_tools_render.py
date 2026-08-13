"""tools.py / main.py 纯逻辑单测：_render_rows 渲染、_short 截断、工具请求构造。

说明：_render_rows/_short 实际位于 main.py（会话持久化渲染），tools.py 中不依赖
gRPC/LLM 的逻辑为 CopilotDeps.ctx、json_format_parse 与工具函数的请求构造
（用记录型假 stub 直接调用原始函数，FunctionToolset.tool 装饰器原样返回函数）。
全程离线：无任何真实 gRPC/HTTP/LLM 调用。
"""

from __future__ import annotations

import asyncio
import json
from types import SimpleNamespace

import pytest
from pydantic_ai.messages import (
    ModelRequest,
    ModelResponse,
    TextPart,
    ToolCallPart,
    ToolReturnPart,
    UserPromptPart,
)

from testpilot.common.v1 import types_pb2 as pb
from testpilot.copilot.v1 import copilot_pb2 as cpb
from testpilot_copilot.main import _render_rows, _short
from testpilot_copilot.tools import (
    CopilotDeps,
    _METHODS,
    create_api,
    json_format_parse,
    list_runs,
)

# ---------------------------------------------------------------------------
# _short：截断与序列化
# ---------------------------------------------------------------------------


def test_short_str_passthrough():
    assert _short("abc") == "abc"
    assert _short("") == ""


def test_short_truncates_long_str():
    s = "x" * 5000
    out = _short(s)
    assert len(out) == 4000
    assert out == s[:4000]


def test_short_custom_limit():
    assert _short("abcdef", limit=3) == "abc"


def test_short_non_str_json_dumps():
    assert _short({"a": 1}) == '{"a": 1}'
    assert _short([1, "二"]) == '[1, "二"]'  # ensure_ascii=False 保留中文


def test_short_non_serializable_falls_back_to_str():
    class Weird:
        def __str__(self):
            return "<weird>"

    assert _short(Weird()) == '"<weird>"'  # default=str → JSON 字符串


def test_short_long_non_str_truncated():
    out = _short({"k": "v" * 5000})
    assert len(out) == 4000


# ---------------------------------------------------------------------------
# _render_rows：ModelMessage → CopilotMessage 行
# ---------------------------------------------------------------------------


def test_render_user_prompt():
    rows = _render_rows([ModelRequest(parts=[UserPromptPart(content="你好")])])
    assert rows == [{"role": 1, "content": "你好"}]


def test_render_user_prompt_non_str_content_skipped():
    part = UserPromptPart(content=["not-a-str"])
    assert _render_rows([ModelRequest(parts=[part])]) == []


def test_render_assistant_text_joined():
    resp = ModelResponse(parts=[TextPart(content="第一行"), TextPart(content="第二行")])
    rows = _render_rows([resp])
    assert rows == [{"role": 2, "content": "第一行\n第二行"}]


def test_render_assistant_tool_call():
    resp = ModelResponse(parts=[
        TextPart(content="我来查一下"),
        ToolCallPart(tool_name="list_projects", args={"query": "demo"}, tool_call_id="t1"),
    ])
    rows = _render_rows([resp])
    assert len(rows) == 1
    row = rows[0]
    assert row["role"] == 2
    assert row["content"] == "我来查一下"
    calls = json.loads(row["tool_calls"])
    assert calls == [{"name": "list_projects", "args": '{"query":"demo"}'}]


def test_render_assistant_tool_call_only_no_text():
    resp = ModelResponse(parts=[
        ToolCallPart(tool_name="get_run", args='{"run_id":"r1"}', tool_call_id="t2"),
    ])
    rows = _render_rows([resp])
    assert rows[0]["role"] == 2
    assert rows[0]["content"] == ""
    assert json.loads(rows[0]["tool_calls"])[0]["args"] == '{"run_id":"r1"}'


def test_render_empty_response_skipped():
    assert _render_rows([ModelResponse(parts=[])]) == []


def test_render_tool_return():
    req = ModelRequest(parts=[
        ToolReturnPart(tool_name="get_run", content={"status": "ok"}, tool_call_id="t1"),
    ])
    rows = _render_rows([req])
    assert len(rows) == 1
    row = rows[0]
    assert row["role"] == 3
    assert row["content"] == ""
    calls = json.loads(row["tool_calls"])
    assert calls[0]["name"] == "get_run"
    assert calls[0]["result"] == '{"status": "ok"}'


def test_render_tool_return_truncates_result():
    req = ModelRequest(parts=[
        ToolReturnPart(tool_name="get_run", content="y" * 9000, tool_call_id="t1"),
    ])
    result = json.loads(_render_rows([req])[0]["tool_calls"])[0]["result"]
    assert len(result) == 4000


def test_render_mixed_sequence_preserves_order():
    msgs = [
        ModelRequest(parts=[UserPromptPart(content="跑一下计划")]),
        ModelResponse(parts=[ToolCallPart(tool_name="trigger_run", args={}, tool_call_id="a")]),
        ModelRequest(parts=[ToolReturnPart(tool_name="trigger_run",
                                           content={"run_id": "r1"}, tool_call_id="a")]),
        ModelResponse(parts=[TextPart(content="已触发 r1")]),
    ]
    rows = _render_rows(msgs)
    assert [r["role"] for r in rows] == [1, 2, 3, 2]
    assert rows[0]["content"] == "跑一下计划"
    assert rows[3]["content"] == "已触发 r1"


def test_render_empty_input():
    assert _render_rows([]) == []


# ---------------------------------------------------------------------------
# tools.py 纯逻辑：CopilotDeps.ctx / _METHODS / json_format_parse
# ---------------------------------------------------------------------------


def _fake_deps(stub) -> CopilotDeps:
    return CopilotDeps(sched=SimpleNamespace(stub=stub), tenant_id=7,
                       user_id="u1", http=None, token="")


def test_deps_ctx_builds_request_context():
    ctx = _fake_deps(SimpleNamespace()).ctx()
    assert ctx.tenant_id == 7
    assert ctx.user_id == "u1"
    assert ctx.actor == "copilot"
    assert ctx.request_id == ""


def test_methods_table_covers_standard_verbs():
    for verb in ("GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"):
        assert verb in _METHODS
    assert _METHODS["POST"] == pb.HTTP_METHOD_POST


def test_json_format_parse_valid():
    dc = pb.DeclarativeCase()
    json_format_parse({"steps": []}, dc)
    assert len(dc.steps) == 0


def test_json_format_parse_strict_unknown_field():
    from google.protobuf.json_format import ParseError

    with pytest.raises(ParseError):
        json_format_parse({"noSuchField": 1}, pb.DeclarativeCase())


# ---------------------------------------------------------------------------
# 工具函数请求构造（记录型假 stub，不触网）
# ---------------------------------------------------------------------------


class _RecordingStub:
    def __init__(self, response):
        self.response = response
        self.requests = []

    def __getattr__(self, name):
        async def call(req):
            self.requests.append((name, req))
            return self.response
        return call


def test_create_api_builds_proto_request():
    stub = _RecordingStub(cpb.CreateApiResponse())
    out = asyncio.run(create_api(
        SimpleNamespace(deps=_fake_deps(stub)),
        project_id="p1", method="post", uri="/login",
        headers={"X-A": "1"}, params={"q": "2"}))
    assert out == {}
    name, req = stub.requests[0]
    assert name == "CreateApi"
    assert req.project_id == "p1"
    assert req.ctx.tenant_id == 7 and req.ctx.actor == "copilot"
    assert req.http.method == pb.HTTP_METHOD_POST  # 小写输入被 upper 归一
    assert req.http.uri == "/login"
    assert {h.key: h.value for h in req.http.headers} == {"X-A": "1"}
    assert {p.key: p.value for p in req.http.params} == {"q": "2"}


def test_create_api_with_body():
    """带 body 的 create_api：content_type 应为 BODY_CONTENT_TYPE_JSON 并发出 RPC。"""
    stub = _RecordingStub(cpb.CreateApiResponse())
    asyncio.run(create_api(SimpleNamespace(deps=_fake_deps(stub)),
                           project_id="p1", method="POST", uri="/x",
                           body='{"u":1}'))
    assert len(stub.requests) == 1
    name, req = stub.requests[0]
    assert name == "CreateApi"
    assert req.http.body.content_type == pb.BODY_CONTENT_TYPE_JSON
    assert req.http.body.raw == '{"u":1}'


def test_create_api_unknown_method_falls_back_to_get():
    stub = _RecordingStub(cpb.CreateApiResponse())
    asyncio.run(create_api(SimpleNamespace(deps=_fake_deps(stub)),
                           project_id="p1", method="teleport", uri="/x"))
    assert stub.requests[0][1].http.method == pb.HTTP_METHOD_GET


def test_create_api_empty_body_leaves_body_unset():
    stub = _RecordingStub(cpb.CreateApiResponse())
    asyncio.run(create_api(SimpleNamespace(deps=_fake_deps(stub)),
                           project_id="p1", method="GET", uri="/x"))
    req = stub.requests[0][1]
    assert req.http.body.content_type == pb.BODY_CONTENT_TYPE_UNSPECIFIED
    assert req.http.body.raw == ""
    assert list(req.http.headers) == [] and list(req.http.params) == []


def test_list_runs_status_parsing():
    stub = _RecordingStub(cpb.ListRunsResponse())
    out = asyncio.run(list_runs(SimpleNamespace(deps=_fake_deps(stub)),
                                status="RUN_STATUS_PASSED"))
    assert out == []
    assert stub.requests[0][1].status == pb.RUN_STATUS_PASSED

    asyncio.run(list_runs(SimpleNamespace(deps=_fake_deps(stub))))
    assert stub.requests[1][1].status == pb.RUN_STATUS_UNSPECIFIED


def test_list_runs_invalid_status_raises_before_rpc():
    stub = _RecordingStub(cpb.ListRunsResponse())
    with pytest.raises(ValueError):
        asyncio.run(list_runs(SimpleNamespace(deps=_fake_deps(stub)), status="BOGUS"))
    assert stub.requests == []  # 未发起 RPC
