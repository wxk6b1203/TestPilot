"""assert_that：链式断言，结果汇入 ctx，失败即抛 AssertionError（fail-fast）。"""

from __future__ import annotations

import json
import re
from typing import Any

_records: list[dict] = []  # 由 entry 在每次运行前重置/收集


def reset_records() -> None:
    _records.clear()


def records() -> list[dict]:
    return list(_records)


class _Assertion:
    def __init__(self, actual: Any, label: str = ""):
        self.actual = actual
        self.label = label or repr(actual)[:40]

    def _check(self, op: str, passed: bool, expected: Any = None, message: str = "") -> "_Assertion":
        _records.append({
            "label": self.label, "op": op,
            "actual": _safe(self.actual), "expected": _safe(expected),
            "passed": passed, "message": message or f"{op} {_safe(expected)}",
        })
        if not passed:
            raise AssertionError(
                f"assert_that({self.label}).{op}({_safe(expected)}) failed: actual={_safe(self.actual)}")
        return self

    def eq(self, expected: Any) -> "_Assertion":
        return self._check("eq", self.actual == expected, expected)

    def ne(self, expected: Any) -> "_Assertion":
        return self._check("ne", self.actual != expected, expected)

    def gt(self, expected: Any) -> "_Assertion":
        try:
            ok = float(self.actual) > float(expected)
        except (TypeError, ValueError):
            ok = False
        return self._check("gt", ok, expected)

    def ge(self, expected: Any) -> "_Assertion":
        try:
            ok = float(self.actual) >= float(expected)
        except (TypeError, ValueError):
            ok = False
        return self._check("ge", ok, expected)

    def lt(self, expected: Any) -> "_Assertion":
        try:
            ok = float(self.actual) < float(expected)
        except (TypeError, ValueError):
            ok = False
        return self._check("lt", ok, expected)

    def le(self, expected: Any) -> "_Assertion":
        try:
            ok = float(self.actual) <= float(expected)
        except (TypeError, ValueError):
            ok = False
        return self._check("le", ok, expected)

    def contains(self, item: Any) -> "_Assertion":
        hay = self.actual
        if isinstance(hay, (dict, list)):
            ok = item in json.dumps(hay, ensure_ascii=False)
        else:
            ok = str(item) in str(hay)
        return self._check("contains", ok, item)

    def matches(self, pattern: str) -> "_Assertion":
        return self._check("matches", re.search(pattern, str(self.actual)) is not None, pattern)

    def exists(self) -> "_Assertion":
        return self._check("exists", self.actual is not None)

    def type_is(self, type_name: str) -> "_Assertion":
        name = {
            dict: "object", list: "array", str: "string",
            bool: "boolean", int: "number", float: "number", type(None): "null",
        }.get(type(self.actual), "unknown")
        return self._check("type_is", name == type_name, type_name)


def _safe(v: Any) -> Any:
    if isinstance(v, (str, int, float, bool)) or v is None:
        return v
    try:
        json.dumps(v)
        return v
    except TypeError:
        return repr(v)


def assert_that(actual: Any, label: str = "") -> _Assertion:
    return _Assertion(actual, label)
