"""assertions：worker 声明式断言评估 + SDK assert_that 链式断言。"""

import pytest
from testpilot.common.v1 import types_pb2 as pb

from testpilot_sdk.assertions import assert_that, records, reset_records
from testpilot_worker.assertions import _json_path, evaluate

RESP = {
    "status": 200,
    "headers": {"content-type": "application/json", "x-req-id": "r-123"},
    "text": "hello world",
    "json": {"data": {"items": [{"name": "foo"}], "total": 1}, "ok": True},
    "elapsed_ms": 42,
}
SCOPE = {"vars": {"want": "world"}, "want_status": 200}


def _assertion(target, op, path="", expected=""):
    return pb.Assertion(target=target, path=path, op=op, expected=expected)


# ---- _json_path ----

def test_json_path_nested_and_index():
    assert _json_path(RESP["json"], "$.data.items[0].name") == "foo"
    assert _json_path(RESP["json"], "$.data.total") == 1
    assert _json_path(RESP["json"], "$") == RESP["json"]


def test_json_path_missing_returns_none():
    assert _json_path(RESP["json"], "$.data.nope") is None
    assert _json_path(RESP["json"], "$.data.items[9]") is None
    assert _json_path(None, "$.data") is None


# ---- evaluate：通过路径 ----

def test_status_eq_pass():
    r = evaluate(_assertion(pb.ASSERTION_TARGET_STATUS, pb.ASSERTION_OP_EQ, expected="200"), RESP, SCOPE)
    assert r.passed


def test_jsonpath_eq_and_type_is():
    r = evaluate(_assertion(pb.ASSERTION_TARGET_JSONPATH, pb.ASSERTION_OP_EQ,
                            path="$.data.total", expected="1"), RESP, SCOPE)
    assert r.passed
    r = evaluate(_assertion(pb.ASSERTION_TARGET_JSONPATH, pb.ASSERTION_OP_TYPE_IS,
                            path="$.data.items", expected="array"), RESP, SCOPE)
    assert r.passed
    r = evaluate(_assertion(pb.ASSERTION_TARGET_JSONPATH, pb.ASSERTION_OP_TYPE_IS,
                            path="$.ok", expected="boolean"), RESP, SCOPE)
    assert r.passed


def test_header_lookup_case_insensitive_path():
    r = evaluate(_assertion(pb.ASSERTION_TARGET_HEADER, pb.ASSERTION_OP_EQ,
                            path="X-Req-Id", expected="r-123"), RESP, SCOPE)
    assert r.passed


def test_body_contains_and_matches():
    r = evaluate(_assertion(pb.ASSERTION_TARGET_BODY, pb.ASSERTION_OP_CONTAINS,
                            expected="world"), RESP, SCOPE)
    assert r.passed
    r = evaluate(_assertion(pb.ASSERTION_TARGET_BODY, pb.ASSERTION_OP_MATCHES,
                            expected=r"hello \w+"), RESP, SCOPE)
    assert r.passed


def test_numeric_comparisons_and_elapsed():
    for op, expected in [
        (pb.ASSERTION_OP_GT, "199"), (pb.ASSERTION_OP_GE, "200"),
        (pb.ASSERTION_OP_LT, "201"), (pb.ASSERTION_OP_LE, "200"),
    ]:
        r = evaluate(_assertion(pb.ASSERTION_TARGET_STATUS, op, expected=expected), RESP, SCOPE)
        assert r.passed, r.message
    r = evaluate(_assertion(pb.ASSERTION_TARGET_ELAPSED, pb.ASSERTION_OP_LT,
                            expected="1000"), RESP, SCOPE)
    assert r.passed


def test_ne_exists_not_exists():
    r = evaluate(_assertion(pb.ASSERTION_TARGET_STATUS, pb.ASSERTION_OP_NE,
                            expected="500"), RESP, SCOPE)
    assert r.passed
    r = evaluate(_assertion(pb.ASSERTION_TARGET_JSONPATH, pb.ASSERTION_OP_EXISTS,
                            path="$.data.total"), RESP, SCOPE)
    assert r.passed
    r = evaluate(_assertion(pb.ASSERTION_TARGET_JSONPATH, pb.ASSERTION_OP_NOT_EXISTS,
                            path="$.data.ghost"), RESP, SCOPE)
    assert r.passed


def test_expected_template_rendered_with_scope():
    r = evaluate(_assertion(pb.ASSERTION_TARGET_BODY, pb.ASSERTION_OP_CONTAINS,
                            expected="{{vars.want}}"), RESP, SCOPE)
    assert r.passed


def test_custom_target_expr():
    r = evaluate(_assertion(pb.ASSERTION_TARGET_CUSTOM, pb.ASSERTION_OP_EQ,
                            path="response['status'] - 100", expected="100"), RESP, SCOPE)
    assert r.passed


def test_jsonpath_path_template_rendered():
    a = _assertion(pb.ASSERTION_TARGET_JSONPATH, pb.ASSERTION_OP_EQ,
                   path="$.data.{{ 'total' }}", expected="1")
    assert evaluate(a, RESP, SCOPE).passed


# ---- evaluate：失败路径 ----

def test_status_eq_fail_message_has_values():
    r = evaluate(_assertion(pb.ASSERTION_TARGET_STATUS, pb.ASSERTION_OP_EQ,
                            expected="500"), RESP, SCOPE)
    assert not r.passed
    assert "200" in r.message and "500" in r.message
    assert r.actual == "200"


def test_numeric_not_comparable():
    r = evaluate(_assertion(pb.ASSERTION_TARGET_BODY, pb.ASSERTION_OP_GT,
                            expected="abc"), RESP, SCOPE)
    assert not r.passed
    assert "not comparable" in r.message


def test_invalid_regex_fails_cleanly():
    r = evaluate(_assertion(pb.ASSERTION_TARGET_BODY, pb.ASSERTION_OP_MATCHES,
                            expected="(["), RESP, SCOPE)
    assert not r.passed
    assert "invalid regex" in r.message


def test_exists_on_missing_path_fails():
    r = evaluate(_assertion(pb.ASSERTION_TARGET_JSONPATH, pb.ASSERTION_OP_EXISTS,
                            path="$.data.ghost"), RESP, SCOPE)
    assert not r.passed
    assert "exists" in r.message


def test_no_response_in_context():
    r = evaluate(_assertion(pb.ASSERTION_TARGET_STATUS, pb.ASSERTION_OP_EQ, expected="200"),
                 None, SCOPE)
    assert not r.passed
    assert r.message == "no response in context"


def test_custom_target_expr_error():
    r = evaluate(_assertion(pb.ASSERTION_TARGET_CUSTOM, pb.ASSERTION_OP_EQ,
                            path="ghost + 1", expected="1"), RESP, SCOPE)
    assert not r.passed
    assert "expr error" in r.message


def test_eq_deep_equal_json():
    a = _assertion(pb.ASSERTION_TARGET_JSONPATH, pb.ASSERTION_OP_EQ,
                   path="$.data.items", expected='[{"name": "foo"}]')
    assert evaluate(a, RESP, SCOPE).passed
    a.expected = "not json"
    r = evaluate(a, RESP, SCOPE)
    assert not r.passed
    assert "expected not valid json" in r.message


def test_unsupported_op():
    r = evaluate(_assertion(pb.ASSERTION_TARGET_STATUS, pb.ASSERTION_OP_UNSPECIFIED,
                            expected="200"), RESP, SCOPE)
    assert not r.passed
    assert "unsupported op" in r.message


# ---- SDK assert_that ----

@pytest.fixture(autouse=True)
def _clean_records():
    reset_records()
    yield
    reset_records()


def test_sdk_pass_chain_and_records():
    assert_that(200, "status").eq(200).ne(500).ge(200).lt(300)
    assert_that("hello world").contains("world").matches(r"hello \w+")
    assert_that({"a": 1}, "body").type_is("object").exists()
    assert_that(None).type_is("null")
    assert_that(3.14).type_is("number")
    assert len(records()) == 10
    assert all(r["passed"] for r in records())
    assert records()[0]["label"] == "status"


def test_sdk_eq_failure_raises_with_context():
    with pytest.raises(AssertionError, match=r"assert_that\(status\)\.eq\(500\) failed: actual=200"):
        assert_that(200, "status").eq(500)
    # 失败记录也已写入
    assert records()[-1]["passed"] is False


def test_sdk_numeric_compare_non_numeric_fails():
    with pytest.raises(AssertionError, match="gt"):
        assert_that("abc").gt(1)


def test_sdk_contains_on_dict_searches_json():
    assert_that({"name": "foo"}).contains("foo")
    with pytest.raises(AssertionError):
        assert_that({"name": "foo"}).contains("barbaz")


def test_sdk_exists_on_none_fails():
    with pytest.raises(AssertionError, match="exists"):
        assert_that(None).exists()


def test_sdk_type_is_mismatch_fails():
    with pytest.raises(AssertionError, match="type_is"):
        assert_that("text").type_is("number")


# ---- MATCHES ReDoS 防护 ----

def test_matches_nested_quantifier_rejected():
    a = _assertion(pb.ASSERTION_TARGET_BODY, pb.ASSERTION_OP_MATCHES, expected="(a+)+$")
    r = evaluate(a, {"text": "aaaa"}, {})
    assert not r.passed
    assert "ReDoS" in r.message, r.message


def test_matches_alternation_quantifier_rejected():
    a = _assertion(pb.ASSERTION_TARGET_BODY, pb.ASSERTION_OP_MATCHES, expected="(a|a)+")
    r = evaluate(a, {"text": "aaaa"}, {})
    assert not r.passed
    assert "ReDoS" in r.message


def test_matches_overlong_rejected():
    a = _assertion(pb.ASSERTION_TARGET_BODY, pb.ASSERTION_OP_MATCHES, expected="a" * 300)
    r = evaluate(a, {"text": "aaaa"}, {})
    assert not r.passed
    assert "too long" in r.message


def test_matches_normal_still_works():
    a = _assertion(pb.ASSERTION_TARGET_BODY, pb.ASSERTION_OP_MATCHES, expected=r"^a+$")
    r = evaluate(a, {"text": "aaaa"}, {})
    assert r.passed, r.message
    b = _assertion(pb.ASSERTION_TARGET_BODY, pb.ASSERTION_OP_MATCHES, expected=r"a{1,5}")
    assert evaluate(b, {"text": "aaa"}, {}).passed
