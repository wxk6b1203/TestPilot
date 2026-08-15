"""统一断言模型评估：status / header / body / jsonpath / elapsed。"""

from __future__ import annotations

import json
import re
from typing import Any, Mapping

from testpilot.common.v1 import types_pb2 as pb

from .expr import ExprError, eval_expr, render

# ---- MATCHES 正则防护（ReDoS）----
# 租户可控正则 + 最多 64KB 响应体：灾难性回溯（(a+)+$、(a|a)+ 类）可冻结
# 事件循环数十秒（心跳/所有任务停摆）。Python re 无超时机制，用启发式拒绝：
# 1) 长度上限；2) 内层量词/交替被外层量词包裹的嵌套模式。
_MAX_REGEX_LEN = 200
_RE_DOS_PATTERN = re.compile(r"\([^()]*[+*|][^()]*\)[+*]")


def _regex_guard(pattern: str) -> str | None:
    """返回拒绝原因；None=放行。"""
    if len(pattern) > _MAX_REGEX_LEN:
        return f"regex too long (> {_MAX_REGEX_LEN} chars)"
    if _RE_DOS_PATTERN.search(pattern):
        return "regex with nested quantifiers rejected (ReDoS guard)"
    return None


def _json_path(doc: Any, path: str) -> Any:
    """极简 JSONPath：$.a.b[0].c；找不到返回 None。"""
    p = path.strip()
    if p.startswith("$."):
        p = p[2:]
    elif p == "$":
        return doc
    elif p.startswith("$"):
        p = p[1:]
    cur = doc
    for token in re.findall(r"[^.\[\]]+|\[\d+\]", p):
        if token.startswith("["):
            idx = int(token[1:-1])
            if isinstance(cur, list) and 0 <= idx < len(cur):
                cur = cur[idx]
            else:
                return None
        else:
            if isinstance(cur, Mapping) and token in cur:
                cur = cur[token]
            else:
                return None
    return cur


def _target_value(resp: Mapping[str, Any], assertion: pb.Assertion, scope: Mapping[str, Any]) -> Any:
    t = assertion.target
    if t == pb.ASSERTION_TARGET_STATUS:
        return resp.get("status")
    if t == pb.ASSERTION_TARGET_HEADER:
        headers = resp.get("headers") or {}
        return headers.get(assertion.path.lower())
    if t == pb.ASSERTION_TARGET_BODY:
        return resp.get("text", "")
    if t == pb.ASSERTION_TARGET_JSONPATH:
        return _json_path(resp.get("json"), render(assertion.path, scope))
    if t == pb.ASSERTION_TARGET_ELAPSED:
        return resp.get("elapsed_ms")
    if t == pb.ASSERTION_TARGET_CUSTOM:
        return eval_expr(assertion.path, {**scope, "response": resp})
    return None


def _compare(op: int, actual: Any, expected: str) -> tuple[bool, str]:
    """返回 (passed, message)。"""
    if op == pb.ASSERTION_OP_EXISTS:
        return actual is not None, f"exists -> {actual is not None}"
    if op == pb.ASSERTION_OP_NOT_EXISTS:
        return actual is None, f"not exists -> {actual is None}"

    exp: Any = expected
    # 数字比较时把 expected 转为数字
    if op in (pb.ASSERTION_OP_GT, pb.ASSERTION_OP_LT, pb.ASSERTION_OP_GE, pb.ASSERTION_OP_LE):
        try:
            exp = float(expected)
            act = float(actual)
        except (TypeError, ValueError):
            return False, f"not comparable: {actual!r} vs {expected!r}"
        if op == pb.ASSERTION_OP_GT:
            return act > exp, f"{act} > {exp}"
        if op == pb.ASSERTION_OP_LT:
            return act < exp, f"{act} < {exp}"
        if op == pb.ASSERTION_OP_GE:
            return act >= exp, f"{act} >= {exp}"
        return act <= exp, f"{act} <= {exp}"

    if op == pb.ASSERTION_OP_EQ:
        if isinstance(actual, (int, float)) and not isinstance(actual, bool):
            try:
                return actual == float(expected), f"{actual} == {expected}"
            except ValueError:
                pass
        if isinstance(actual, bool) or expected.lower() in ("true", "false"):
            return str(actual).lower() == expected.lower(), f"{actual} == {expected}"
        if isinstance(actual, (dict, list)):
            try:
                return actual == json.loads(expected), "deep equal"
            except ValueError:
                return False, "expected not valid json"
        return str(actual) == expected, f"{actual!r} == {expected!r}"
    if op == pb.ASSERTION_OP_NE:
        ok, msg = _compare(pb.ASSERTION_OP_EQ, actual, expected)
        return not ok, "not " + msg
    if op == pb.ASSERTION_OP_CONTAINS:
        if isinstance(actual, (dict, list)):
            return expected in json.dumps(actual, ensure_ascii=False), f"contains {expected!r}"
        return expected in str(actual), f"contains {expected!r}"
    if op == pb.ASSERTION_OP_MATCHES:
        if err := _regex_guard(expected):
            return False, err
        try:
            return re.search(expected, str(actual)) is not None, f"matches /{expected}/"
        except re.error as e:
            return False, f"invalid regex: {e}"
    if op == pb.ASSERTION_OP_TYPE_IS:
        type_name = {
            dict: "object", list: "array", str: "string",
            bool: "boolean", int: "number", float: "number", type(None): "null",
        }.get(type(actual), "unknown")
        return type_name == expected.lower(), f"type is {type_name}"
    return False, f"unsupported op {op}"


def evaluate(assertion: pb.Assertion, resp: Mapping[str, Any] | None,
             scope: Mapping[str, Any]) -> pb.AssertionResult:
    """评估单条断言，返回 AssertionResult proto。"""
    result = pb.AssertionResult(assertion=assertion)
    if resp is None:
        result.passed = False
        result.message = "no response in context"
        return result
    try:
        actual = _target_value(resp, assertion, scope)
        expected = render(assertion.expected, scope)
        ok, msg = _compare(assertion.op, actual, "" if expected is None else str(expected))
        result.passed = ok
        result.actual = "" if actual is None else (
            json.dumps(actual, ensure_ascii=False) if isinstance(actual, (dict, list)) else str(actual))
        result.message = msg
    except ExprError as e:
        result.passed = False
        result.message = f"expr error: {e}"
    except Exception as e:  # 防御：断言失败不应中断执行
        result.passed = False
        result.message = f"assert error: {e}"
    return result
