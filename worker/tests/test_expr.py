"""expr.py：受限表达式求值 + {{...}} 模板渲染。"""

import pytest

from testpilot_worker.expr import ExprError, eval_expr, render, render_map

SCOPE = {
    "vars": {"token": "abc123", "limit": 3},
    "response": {
        "status": 200,
        "json": {"data": {"items": [{"name": "foo"}, {"name": "bar"}], "total": 2}},
        "headers": {"content-type": "application/json"},
    },
    "base_url": "http://example.com",
    "n": 7,
    "items": [10, 20, 30],
    "user": {"name": "alice", "admin": True},
}


# ---- eval_expr：取值 ----

def test_literal_constants():
    assert eval_expr("42", {}) == 42
    assert eval_expr("'hello'", {}) == "hello"
    assert eval_expr("True", {}) is True
    assert eval_expr("None", {}) is None


def test_name_lookup_from_scope():
    assert eval_expr("n", SCOPE) == 7
    assert eval_expr("base_url", SCOPE) == "http://example.com"


def test_attribute_on_mapping():
    assert eval_expr("user.name", SCOPE) == "alice"
    assert eval_expr("vars.token", SCOPE) == "abc123"
    assert eval_expr("response.status", SCOPE) == 200


def test_subscript_index_and_key():
    assert eval_expr("items[0]", SCOPE) == 10
    assert eval_expr("items[-1]", SCOPE) == 30
    assert eval_expr("user['name']", SCOPE) == "alice"


def test_nested_path():
    assert eval_expr("response.json.data.items[1].name", SCOPE) == "bar"


def test_missing_subscript_returns_none():
    assert eval_expr("user['nope']", SCOPE) is None
    assert eval_expr("items[99]", SCOPE) is None


def test_missing_attribute_on_mapping_returns_none():
    assert eval_expr("user.nope", SCOPE) is None


def test_collection_literals():
    assert eval_expr("[1, n, 3]", SCOPE) == [1, 7, 3]
    assert eval_expr("{'a': n}", SCOPE) == {"a": 7}
    assert eval_expr("(1, 2)", SCOPE) == (1, 2)


# ---- eval_expr：运算 ----

def test_arithmetic():
    assert eval_expr("n + 1", SCOPE) == 8
    assert eval_expr("n * 2 - 4", SCOPE) == 10
    assert eval_expr("n // 2", SCOPE) == 3
    assert eval_expr("n % 2", SCOPE) == 1
    assert eval_expr("-n", SCOPE) == -7


def test_comparison_operators():
    assert eval_expr("n > 5", SCOPE) is True
    assert eval_expr("n >= 7 and n <= 7", SCOPE) is True
    assert eval_expr("n == 8", SCOPE) is False
    assert eval_expr("n != 8", SCOPE) is True
    assert eval_expr("5 < n < 10", SCOPE) is True


def test_bool_ops_short_circuit_values():
    assert eval_expr("True and n", SCOPE) == 7
    assert eval_expr("False or n", SCOPE) == 7
    assert eval_expr("not user.admin", SCOPE) is False


def test_in_not_in():
    assert eval_expr("20 in items", SCOPE) is True
    assert eval_expr("99 not in items", SCOPE) is True
    assert eval_expr("'token' in vars", SCOPE) is True


def test_ternary_ifexp():
    assert eval_expr("'big' if n > 5 else 'small'", SCOPE) == "big"
    assert eval_expr("'big' if n > 50 else 'small'", SCOPE) == "small"


# ---- eval_expr：错误行为 ----

def test_undefined_name_raises():
    with pytest.raises(ExprError, match="undefined name: ghost"):
        eval_expr("ghost", SCOPE)


def test_syntax_error_raises():
    with pytest.raises(ExprError, match="syntax error"):
        eval_expr("1 +", SCOPE)


def test_empty_expression_raises():
    with pytest.raises(ExprError, match="empty expression"):
        eval_expr("   ", SCOPE)


def test_function_call_rejected():
    with pytest.raises(ExprError, match="unsupported expression node: Call"):
        eval_expr("len(items)", SCOPE)


def test_dunder_access_rejected():
    with pytest.raises(ExprError, match="dunder access not allowed"):
        eval_expr("user.__class__", SCOPE)


def test_unsupported_binop_rejected():
    with pytest.raises(ExprError, match="unsupported binary op"):
        eval_expr("2 ** 3", SCOPE)


def test_attribute_on_non_mapping_rejected():
    with pytest.raises(ExprError, match="attribute access on non-mapping"):
        eval_expr("n.real", SCOPE)


# ---- render：模板 ----

def test_render_full_template_preserves_native_type():
    assert render("{{ n }}", SCOPE) == 7
    assert render("{{ items }}", SCOPE) == [10, 20, 30]
    assert render("{{ user }}", SCOPE) == {"name": "alice", "admin": True}


def test_render_embedded_interpolation():
    assert render("status={{response.status}}", SCOPE) == "status=200"
    assert render("{{base_url}}/api/{{user.name}}/", SCOPE) == "http://example.com/api/alice/"


def test_render_none_becomes_empty_string():
    assert render("x{{user.nope}}y", SCOPE) == "xy"


def test_render_non_string_passthrough():
    assert render(123, SCOPE) == 123
    assert render(["{{n}}"], SCOPE) == ["{{n}}"]  # 列表不递归渲染


def test_render_full_template_error_propagates():
    with pytest.raises(ExprError, match="undefined name"):
        render("{{ ghost }}", SCOPE)


# ---- render_map ----

class _KV:
    def __init__(self, key, value):
        self.key, self.value = key, value


def test_render_map_from_dict():
    out = render_map({"X-Token": "{{vars.token}}", "X-Static": "v"}, SCOPE)
    assert out == {"X-Token": "abc123", "X-Static": "v"}


def test_render_map_from_kv_list():
    out = render_map([_KV("A", "{{n}}"), _KV("", "skip"), _KV("B", None)], SCOPE)
    assert out == {"A": "7", "B": ""}


def test_render_map_none_is_empty():
    assert render_map(None, SCOPE) == {}


def test_render_multi_segment_with_template_at_both_ends():
    # 回归：多片段模板首尾恰为 {{ }} 时不得误判为单表达式
    scope = {"base_url": "http://h", "user": {"name": "bob"}}
    assert render("{{base_url}}/api/{{user.name}}", scope) == "http://h/api/bob"
    # 单表达式仍返回原生类型
    assert render("{{ user }}", scope) == {"name": "bob"}
