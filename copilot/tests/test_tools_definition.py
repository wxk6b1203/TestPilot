# 工具入参定义对象的包装键剥离（strip_def_wrappers）回归测试。
# 背景：LLM 回传 definition/api 时常照抄 get_* 返回的 JSON 形状，把定义
# 对象包进 oneof 包装键（lowcode/declarative/http/grpc），protojson 拒绝
# 未知字段直接报错（LowCodeCase has no field named "lowcode"）。
import pytest
from google.protobuf import json_format

from testpilot_copilot.tools import json_format_parse, strip_def_wrappers


def test_reported_lowcode_wrapper_is_stripped():
    """线上报错形状：LLM 传 definition={"lowcode": {...}}（照抄 get 形状）"""
    merged = {"source": "def run(ctx):\n    pass", "entry": "run"}
    definition = {"lowcode": {"entry": "main", "httpApiRefs": ["123"]}}
    definition = strip_def_wrappers(definition, ("lowcode", "declarative", "case"))
    merged_def = {**merged, **definition}
    lc = __import__("testpilot.common.v1.types_pb2", fromlist=["LowCodeCase"]).LowCodeCase()
    json_format_parse(merged_def, lc)  # 修复前这里抛 ParseError: no field named "lowcode"
    assert lc.entry == "main"
    assert list(lc.http_api_refs) == ["123"]
    assert lc.source == "def run(ctx):\n    pass"


def test_double_wrapper_case_lowcode():
    d = strip_def_wrappers({"case": {"lowcode": {"source": "x"}}},
                           ("lowcode", "declarative", "case"))
    assert d == {"source": "x"}


def test_declarative_and_api_wrappers():
    d = strip_def_wrappers({"declarative": {"steps": []}}, ("lowcode", "declarative", "case"))
    assert d == {"steps": []}
    a = strip_def_wrappers({"http": {"uri": "/v2"}}, ("http", "grpc", "api"))
    assert a == {"uri": "/v2"}


def test_no_wrapper_passthrough():
    d = {"source": "x", "entry": "run", "parameters": {"a": 1}}
    assert strip_def_wrappers(dict(d), ("lowcode", "declarative", "case")) == d


def test_non_dict_input_returns_empty_dict():
    assert strip_def_wrappers(None, ("lowcode",)) == {}
    assert strip_def_wrappers("nope", ("lowcode",)) == {}


def test_flat_wrapper_value_passthrough():
    # 包装键的值不是 dict（畸形入参）时不剥、原样返回，交给 protojson 报错
    d = {"lowcode": "flat-string"}
    assert strip_def_wrappers(d, ("lowcode",)) == d


def test_deep_wrap_capped_at_three_levels():
    d = {"case": {"case": {"case": {"case": {"source": "x"}}}}}
    out = strip_def_wrappers(d, ("case",))
    assert out == {"case": {"source": "x"}}


def test_unwrapped_parse_still_rejects_unknown_fields():
    """剥包装之外，真正未知字段仍被 protojson 拒绝（fail-closed 不变）"""
    with pytest.raises(json_format.ParseError):
        json_format_parse({"no_such_field": 1},
                          __import__("testpilot.common.v1.types_pb2",
                                     fromlist=["LowCodeCase"]).LowCodeCase())
