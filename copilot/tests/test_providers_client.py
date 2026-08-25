"""providers.py / scheduler_client.py 不触网构造逻辑单测。

build_model：仅构造 OpenAI 兼容客户端对象（客户端创建不发请求），断言
provider 选择、base_url 推导、model 覆盖与未知 provider 报错。
scheduler_client：ctx/to_dict/parse_struct 为纯 proto 转换，全程离线。
"""

from __future__ import annotations

import pytest

from testpilot.common.v1 import types_pb2 as pb
from testpilot_copilot.config import Settings
from testpilot_copilot.providers import build_model
from testpilot_copilot.scheduler_client import SchedulerClient, parse_struct, to_dict

# ---------------------------------------------------------------------------
# build_model
# ---------------------------------------------------------------------------


def test_deepseek_default_endpoint():
    m = build_model(Settings(api_key="test-key"))
    assert m.model_name == "deepseek-v4-flash"
    assert str(m.client.base_url).rstrip("/") == "https://api.deepseek.com"


def test_model_timeout_applied_to_openai_client():
    """流式 token 卡住时，OpenAI 客户端 read 超时会把异常冒泡成 SSE error。"""
    m = build_model(Settings(api_key="test-key", model_timeout=45))
    t = m.client.timeout
    assert t.as_dict()["read"] == 45
    assert t.as_dict()["connect"] == 10  # 建连超时不随 model_timeout 放大

    m2 = build_model(Settings(api_key="test-key", model_timeout=0))
    assert m2.client.timeout is None  # 0=禁用模型读超时


def test_deepseek_base_url_override():
    m = build_model(Settings(api_key="test-key", base_url="http://gw.local:9000/v1"))
    assert str(m.client.base_url).rstrip("/") == "http://gw.local:9000/v1"


def test_openai_compatible_with_base_url():
    s = Settings(api_key="k", provider="openai_compatible",
                 base_url="http://oneapi.local/v1", model="qwen3")
    m = build_model(s)
    assert m.model_name == "qwen3"
    assert str(m.client.base_url).rstrip("/") == "http://oneapi.local/v1"


def test_openai_compatible_without_base_url_uses_openai_default():
    s = Settings(api_key="k", provider="openai_compatible", model="gpt-x")
    m = build_model(s)
    assert str(m.client.base_url).startswith("https://api.openai.com")


def test_model_override_does_not_mutate_settings():
    s = Settings(api_key="test-key", model="deepseek-v4-flash")
    m = build_model(s, model="cheaper-model")
    assert m.model_name == "cheaper-model"
    assert s.model == "deepseek-v4-flash"  # frozen dataclass 原值不变


def test_unknown_provider_raises():
    with pytest.raises(ValueError, match="unknown provider 'nope'.*deepseek"):
        build_model(Settings(api_key="k", provider="nope"))


# ---------------------------------------------------------------------------
# SchedulerClient.ctx（静态，纯构造）
# ---------------------------------------------------------------------------


def test_ctx_fields_and_defaults():
    c = SchedulerClient.ctx(42, "user-1")
    assert c.tenant_id == 42
    assert c.user_id == "user-1"
    assert c.actor == "copilot"
    assert c.request_id == ""
    c2 = SchedulerClient.ctx(1, "u", request_id="req-9")
    assert c2.request_id == "req-9"


# ---------------------------------------------------------------------------
# to_dict / parse_struct（proto ↔ dict 纯转换）
# ---------------------------------------------------------------------------


def test_to_dict_camel_case_and_int64_stringified():
    d = to_dict(pb.RequestContext(tenant_id=7, user_id="u1", actor="copilot"))
    assert d == {"tenantId": "7", "userId": "u1", "actor": "copilot"}


def test_to_dict_omits_unset_scalar_fields():
    d = to_dict(pb.RequestContext(tenant_id=0, user_id=""))
    assert d == {}  # proto3 默认零值不输出


def test_to_dict_enum_rendered_as_name():
    api = pb.HttpApi(method=pb.HTTP_METHOD_POST, uri="/x")
    d = to_dict(api)
    assert d["method"] == "HTTP_METHOD_POST"
    assert d["uri"] == "/x"


def test_parse_struct_roundtrip():
    s = parse_struct({"a": 1, "b": "x", "nested": {"c": True}})
    assert dict(s) == {"a": 1.0, "b": "x", "nested": {"c": True}}


def test_parse_struct_empty_and_none():
    assert len(parse_struct({}).fields) == 0
    assert len(parse_struct(None).fields) == 0
