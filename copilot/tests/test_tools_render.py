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
    check_variable_refs,
    create_api,
    create_test_plan,
    get_current_context,
    json_format_parse,
    list_apis,
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


def _fake_deps(stub, ui_project_id: str = "", ui_env_id: str = "",
               ui_project: dict | None = None,
               ui_environment: dict | None = None) -> CopilotDeps:
    return CopilotDeps(sched=SimpleNamespace(stub=stub), tenant_id=7,
                       user_id="u1", http=None, token="",
                       ui_project_id=ui_project_id, ui_env_id=ui_env_id,
                       ui_project=ui_project, ui_environment=ui_environment)


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


# ---------------------------------------------------------------------------
# 页面上下文（左上角项目/环境选择）：缺省参数解析 + get_current_context
# ---------------------------------------------------------------------------


def test_create_api_defaults_to_ui_project_and_explicit_wins():
    stub = _RecordingStub(cpb.CreateApiResponse())
    deps = _fake_deps(stub, ui_project_id="p9", ui_env_id="e9")
    asyncio.run(create_api(SimpleNamespace(deps=deps), method="GET", uri="/x"))
    assert stub.requests[0][1].project_id == "p9"

    asyncio.run(create_api(SimpleNamespace(deps=deps), method="GET", uri="/y",
                           project_id="p2"))
    assert stub.requests[1][1].project_id == "p2"


def test_create_api_without_any_project_fails_before_rpc():
    stub = _RecordingStub(cpb.CreateApiResponse())
    with pytest.raises(ValueError, match="未选择项目"):
        asyncio.run(create_api(SimpleNamespace(deps=_fake_deps(stub)),
                               method="GET", uri="/x"))
    assert stub.requests == []


def test_list_apis_uses_ui_project_when_omitted():
    stub = _RecordingStub(cpb.ListApisResponse())
    asyncio.run(list_apis(SimpleNamespace(deps=_fake_deps(stub, ui_project_id="p3"))))
    assert stub.requests[0][0] == "ListApis"
    assert stub.requests[0][1].project_id == "p3"


def test_check_variable_refs_uses_ui_project_and_env():
    stub = _RecordingStub(cpb.CheckVariableRefsResponse())
    deps = _fake_deps(stub, ui_project_id="p4", ui_env_id="e4")
    asyncio.run(check_variable_refs(SimpleNamespace(deps=deps)))
    assert stub.requests[0][1].project_id == "p4"
    assert stub.requests[0][1].environment_id == "e4"


def test_create_test_plan_requires_env_from_ui():
    stub = _RecordingStub(cpb.CreateTestPlanResponse())
    deps = _fake_deps(stub, ui_project_id="p5", ui_env_id="e5")
    asyncio.run(create_test_plan(SimpleNamespace(deps=deps), name="plan",
                                 case_ids=["c1"]))
    req = stub.requests[0][1]
    assert req.project_id == "p5"
    assert req.plan.env_id == "e5"

    with pytest.raises(ValueError, match="未选择环境"):
        asyncio.run(create_test_plan(
            SimpleNamespace(deps=_fake_deps(stub, ui_project_id="p5")),
            name="plan", case_ids=["c1"]))


def test_list_runs_defaults_to_ui_project_but_empty_means_all():
    stub = _RecordingStub(cpb.ListRunsResponse())
    asyncio.run(list_runs(SimpleNamespace(deps=_fake_deps(stub, ui_project_id="p6"))))
    assert stub.requests[0][1].project_id == "p6"

    asyncio.run(list_runs(SimpleNamespace(deps=_fake_deps(stub, ui_project_id="p6")),
                          project_id=""))
    assert stub.requests[1][1].project_id == ""


class _FakeHTTP:
    """最小 httpx.AsyncClient 替身：path → (status, json) 映射。"""

    def __init__(self, responses: dict[str, tuple[int, dict]]):
        self.responses = responses
        self.calls: list[str] = []

    async def get(self, path, headers=None):
        self.calls.append(path)
        status, payload = self.responses[path]
        return SimpleNamespace(status_code=status, json=lambda: payload)


def _ctx_deps(stub=SimpleNamespace(), project_id="", env_id="",
              project=None, environment=None, http=None):
    return CopilotDeps(sched=SimpleNamespace(stub=stub), tenant_id=7,
                       user_id="u1", http=http, token="jwt-ctx",
                       ui_project_id=project_id, ui_env_id=env_id,
                       ui_project=project, ui_environment=environment)


def test_hydrate_ui_context_keeps_valid_project_and_env():
    http = _FakeHTTP({
        "/api/v1/projects/p1": (200, {"id": "p1", "name": "Demo",
                                      "tenant_id": 7}),
        "/api/v1/environments?project_id=p1&page_size=200": (
            200, {"items": [
                {"id": "e1", "project_id": "p1", "name": "测试环境",
                 "base_url": "http://echo:18080"},
                {"id": "e2", "project_id": "p1", "name": "其他环境"},
            ]}),
    })
    deps = _ctx_deps(project_id="p1", env_id="e1", http=http)
    asyncio.run(deps.hydrate_ui_context())
    assert deps.ui_project_id == "p1"
    assert deps.ui_project["name"] == "Demo"
    assert deps.ui_env_id == "e1"
    assert deps.ui_environment["base_url"] == "http://echo:18080"
    assert len(http.calls) == 2


def test_hydrate_ui_context_clears_stale_or_mismatched_ids():
    # 项目失效 → 项目和环境下发 ID 都清空，且不再请求环境列表
    http = _FakeHTTP({"/api/v1/projects/p-gone": (404, {})})
    deps = _ctx_deps(project_id="p-gone", env_id="e1", http=http)
    asyncio.run(deps.hydrate_ui_context())
    assert deps.ui_project_id == "" and deps.ui_project is None
    assert deps.ui_env_id == "" and deps.ui_environment is None
    assert http.calls == ["/api/v1/projects/p-gone"]

    # 项目有效但环境不属于该项目 → 仅清环境
    http = _FakeHTTP({
        "/api/v1/projects/p1": (200, {"id": "p1", "name": "Demo"}),
        "/api/v1/environments?project_id=p1&page_size=200": (
            200, {"items": [{"id": "e-other", "project_id": "p1"}]}),
    })
    deps = _ctx_deps(project_id="p1", env_id="e1", http=http)
    asyncio.run(deps.hydrate_ui_context())
    assert deps.ui_project_id == "p1" and deps.ui_project is not None
    assert deps.ui_env_id == "" and deps.ui_environment is None


def test_get_current_context_no_selection():
    out = asyncio.run(get_current_context(SimpleNamespace(deps=_ctx_deps())))
    assert out["project_selected"] is False
    assert out["project"] is None
    assert out["environment_selected"] is False
    assert out["environment"] is None
    assert "未选择项目" in out["hint"]


def test_get_current_context_returns_authoritative_details():
    deps = _ctx_deps(
        project_id="p1", env_id="e1",
        project={"id": "p1", "tenant_id": 7, "name": "Demo",
                 "created_at": "2026-08-18T00:00:00Z"},
        environment={"id": "e1", "project_id": "p1", "name": "测试环境",
                     "base_url": "http://echo:18080"})
    out = asyncio.run(get_current_context(SimpleNamespace(deps=deps)))
    assert out["project_selected"] is True
    assert out["project"]["name"] == "Demo"
    assert out["project"]["tenantId"] == 7
    assert out["environment_selected"] is True
    assert out["environment"]["baseUrl"] == "http://echo:18080"


def test_get_current_context_hydrates_when_details_missing():
    http = _FakeHTTP({
        "/api/v1/projects/p1": (200, {"id": "p1", "name": "Demo"}),
        "/api/v1/environments?project_id=p1&page_size=200": (
            200, {"items": [{"id": "e1", "project_id": "p1",
                             "name": "测试环境", "base_url": "http://echo:18080"}]}),
    })
    deps = _ctx_deps(project_id="p1", env_id="e1", http=http)
    out = asyncio.run(get_current_context(SimpleNamespace(deps=deps)))
    assert out["project"]["name"] == "Demo"
    assert out["environment"]["baseUrl"] == "http://echo:18080"
    assert len(http.calls) == 2
