"""数据模型工具：REST 通路（_rest_json）+ JSON→Schema 推断。

用 httpx.MockTransport 模拟 Scheduler REST；CopilotDeps.sched 置 None
（本组工具不走 gRPC），token/项目上下文由 deps 直填。
"""

import asyncio
import json

import httpx
import pytest

from testpilot_copilot import tools
from testpilot_copilot.jsonschema_gen import json_to_schema
from testpilot_copilot.tools import CopilotDeps


class _FakeRunContext:
    """工具实现只读 ctx.deps（REST 助手不再走 gRPC stub）。"""

    def __init__(self, deps: CopilotDeps):
        self.deps = deps


def _deps(handler, project_id: str = "1001") -> CopilotDeps:
    http = httpx.AsyncClient(transport=httpx.MockTransport(handler), base_url="http://sched.test")
    return CopilotDeps(sched=None, tenant_id=1, user_id="u1", http=http,
                       token="tok-123", ui_project_id=project_id)


# ---- json_to_schema 推断 ----

def test_json_to_schema_types():
    assert json_to_schema(None) == {"type": "any"}
    assert json_to_schema(True) == {"type": "boolean"}  # bool 须先于 int 判定
    assert json_to_schema(3) == {"type": "integer"}
    assert json_to_schema(3.5) == {"type": "number"}
    assert json_to_schema("x") == {"type": "string"}


def test_json_to_schema_nested_and_array_merge():
    s = json_to_schema({"code": 0, "msg": "ok", "data": {"id": 1},
                        "tags": ["a", "b"], "mixed": [1, "x"], "score": 0.5})
    assert s["type"] == "object"
    props = s["properties"]
    assert props["code"] == {"type": "integer"}
    assert props["data"] == {"type": "object", "properties": {"id": {"type": "integer"}}}
    assert props["tags"] == {"type": "array", "items": {"type": "string"}}
    assert props["mixed"] == {"type": "array", "items": {"type": "any"}}  # 类型不一致兜底
    assert props["score"] == {"type": "number"}


def test_json_to_schema_object_array_deep_merge():
    s = json_to_schema([{"a": 1}, {"a": "x", "b": 2}])
    # 数组元素均为 object：properties 并集，同名字段类型不一致兜底 any
    items = s["items"]
    assert items["type"] == "object"
    assert items["properties"]["b"] == {"type": "integer"}
    assert items["properties"]["a"] == {"type": "any"}
    assert json_to_schema([]) == {"type": "array", "items": {"type": "any"}}


# ---- REST 工具 ----

def test_create_data_model_from_json_infers_schema():
    seen = {}

    def handler(request: httpx.Request) -> httpx.Response:
        seen["path"] = request.url.path
        seen["auth"] = request.headers.get("Authorization")
        seen["body"] = json.loads(request.content)
        return httpx.Response(200, json={"id": "9001", "name": "Response"})

    deps = _deps(handler)
    out = asyncio.run(tools.create_data_model(
        _FakeRunContext(deps), name="Response",
        json={"code": 0, "msg": "ok"},
        description="通用响应"))
    assert out == {"id": "9001", "name": "Response"}
    assert seen["path"] == "/api/v1/models"
    assert seen["auth"] == "Bearer tok-123"  # 透传用户 token
    body = seen["body"]
    assert body["project_id"] == "1001"
    assert body["name"] == "Response"
    # schema 由示例 JSON 推断
    assert body["schema"] == {"type": "object",
                              "properties": {"code": {"type": "integer"},
                                             "msg": {"type": "string"}}}


def test_create_data_model_direct_schema_wins():
    seen = {}

    def handler(request: httpx.Request) -> httpx.Response:
        seen["body"] = json.loads(request.content)
        return httpx.Response(200, json={"id": "9002"})

    deps = _deps(handler)
    schema = {"type": "object", "properties": {"code": {"type": "integer"}}, "required": ["code"]}
    asyncio.run(tools.create_data_model(
        _FakeRunContext(deps), name="R", json_schema=schema, json={"ignored": 1}))
    assert seen["body"]["schema"] == schema  # 显式 json_schema 优先于 json


def test_update_data_model_partial_fields_only():
    seen = {}

    def handler(request: httpx.Request) -> httpx.Response:
        seen["method"] = request.method
        seen["path"] = request.url.path
        seen["body"] = json.loads(request.content)
        return httpx.Response(200, json={"id": "9001"})

    deps = _deps(handler)
    asyncio.run(tools.update_data_model(_FakeRunContext(deps), "9001", name="M2"))
    assert seen["method"] == "PUT"
    assert seen["path"] == "/api/v1/models/9001"
    assert seen["body"] == {"name": "M2"}  # 未提供的字段不出现在请求体


def test_update_data_model_json_replaces_schema():
    seen = {}

    def handler(request: httpx.Request) -> httpx.Response:
        seen["body"] = json.loads(request.content)
        return httpx.Response(200, json={"id": "9001"})

    deps = _deps(handler)
    asyncio.run(tools.update_data_model(_FakeRunContext(deps), "9001", json={"ok": True}))
    assert seen["body"] == {"schema": {"type": "object", "properties": {"ok": {"type": "boolean"}}}}


def test_update_data_model_without_fields_raises():
    deps = _deps(lambda request: httpx.Response(200, json={}))
    with pytest.raises(ValueError, match="no fields to update"):
        asyncio.run(tools.update_data_model(_FakeRunContext(deps), "9001"))


def test_list_data_models_filters_by_query():
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, json={"items": [
            {"id": "1", "name": "Response", "description": "通用响应"},
            {"id": "2", "name": "PageRequest", "description": ""},
        ], "total": 2})

    deps = _deps(handler)
    out = asyncio.run(tools.list_data_models(_FakeRunContext(deps), query="page"))
    assert [m["id"] for m in out] == ["2"]
    # 摘要不含 schema（控制 LLM 上下文体积）
    full = asyncio.run(tools.list_data_models(_FakeRunContext(deps)))
    assert all("schema" not in m for m in full)


def test_get_and_delete_data_model_paths():
    seen = []

    def handler(request: httpx.Request) -> httpx.Response:
        seen.append((request.method, request.url.path))
        return httpx.Response(200, json={"id": "9001", "schema": {"type": "object"}})

    deps = _deps(handler)
    got = asyncio.run(tools.get_data_model(_FakeRunContext(deps), "9001"))
    assert got["schema"] == {"type": "object"}
    asyncio.run(tools.delete_data_model(_FakeRunContext(deps), "9001"))
    assert seen == [("GET", "/api/v1/models/9001"), ("DELETE", "/api/v1/models/9001")]


def test_rest_error_surfaces_backend_message():
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(409, json={"error": {"code": "CODE_CONFLICT",
                                                   "message": "name duplicated"}})

    deps = _deps(handler)
    with pytest.raises(ValueError, match=r"HTTP 409.*name duplicated"):
        asyncio.run(tools.create_data_model(_FakeRunContext(deps), name="dup"))
