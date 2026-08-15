"""受限表达式引擎：{{...}} 模板渲染 + 安全求值。

- 模板：字符串中的 {{ expr }} 片段被求值并替换；整体恰好是一个 {{ expr }} 时保留原生类型。
- 求值：基于 ast 的白名单解释器（常量/名称/下标/属性/比较/布尔/算术/in），
  不允许函数调用、不允许 dunder 访问，杜绝任意代码执行。
- 作用域：用例变量平铺 + vars / env / response / last 等保留根。
"""

from __future__ import annotations

import ast
import re
from typing import Any, Mapping

_TEMPLATE_RE = re.compile(r"\{\{\s*(.+?)\s*\}\}", re.DOTALL)
_FULL_RE = re.compile(r"^\{\{\s*(.+?)\s*\}\}$", re.DOTALL)


# 资源上限（P0：表达式在 Worker 进程内求值，恶意/误配用例可 OOM/栈溢出）
_MAX_EXPR_LEN = 2000         # 单条表达式最大长度
_MAX_DEPTH = 64              # AST 嵌套深度
_MAX_STR_MUL = 1_000_000     # 字符串乘法结果最大长度
_MAX_LIST_MUL = 100_000      # 列表乘法结果最大元素数
_MAX_COLLECT_LEN = 2_000     # 容器字面量最大元素数


class ExprError(Exception):
    pass


def _bounded(node: ast.AST, depth: int) -> None:
    """深度守卫：超深嵌套抛 ExprError（ast.parse 对深嵌套会裸 RecursionError）。"""
    if depth > _MAX_DEPTH:
        raise ExprError(f"expression too deeply nested (> {_MAX_DEPTH})")


# ---- 安全求值 ----

_ALLOWED_BINOP = (ast.Add, ast.Sub, ast.Mult, ast.Div, ast.Mod, ast.FloorDiv)
_ALLOWED_CMP = (ast.Eq, ast.NotEq, ast.Lt, ast.LtE, ast.Gt, ast.GtE, ast.In, ast.NotIn)


def _lookup(scope: Mapping[str, Any], name: str) -> Any:
    if name in scope:
        return scope[name]
    raise ExprError(f"undefined name: {name}")


def _eval(node: ast.AST, scope: Mapping[str, Any], depth: int = 0) -> Any:
    _bounded(node, depth)
    if isinstance(node, ast.Expression):
        return _eval(node.body, scope, depth + 1)
    if isinstance(node, ast.Constant):
        return node.value
    if isinstance(node, ast.Name):
        return _lookup(scope, node.id)
    if isinstance(node, ast.List):
        if len(node.elts) > _MAX_COLLECT_LEN:
            raise ExprError("list literal too large")
        return [_eval(e, scope, depth + 1) for e in node.elts]
    if isinstance(node, ast.Tuple):
        if len(node.elts) > _MAX_COLLECT_LEN:
            raise ExprError("tuple literal too large")
        return tuple(_eval(e, scope, depth + 1) for e in node.elts)
    if isinstance(node, ast.Dict):
        if len(node.keys) > _MAX_COLLECT_LEN:
            raise ExprError("dict literal too large")
        return {_eval(k, scope, depth + 1): _eval(v, scope, depth + 1)
                for k, v in zip(node.keys, node.values)}
    if isinstance(node, ast.Attribute):
        base = _eval(node.value, scope, depth + 1)
        if node.attr.startswith("_"):
            raise ExprError("dunder access not allowed")
        if isinstance(base, Mapping):
            return base.get(node.attr)
        raise ExprError(f"attribute access on non-mapping: {node.attr}")
    if isinstance(node, ast.Subscript):
        base = _eval(node.value, scope, depth + 1)
        key = _eval(node.slice, scope, depth + 1)
        try:
            return base[key]
        except (KeyError, IndexError, TypeError):
            return None
    if isinstance(node, ast.UnaryOp):
        val = _eval(node.operand, scope, depth + 1)
        if isinstance(node.op, ast.Not):
            return not val
        if isinstance(node.op, ast.USub):
            return -val
        if isinstance(node.op, ast.UAdd):
            return +val
        raise ExprError("unsupported unary op")
    if isinstance(node, ast.BoolOp):
        if isinstance(node.op, ast.And):
            result = True
            for v in node.values:
                result = _eval(v, scope, depth + 1)
                if not result:
                    return result
            return result
        if isinstance(node.op, ast.Or):
            result = False
            for v in node.values:
                result = _eval(v, scope, depth + 1)
                if result:
                    return result
            return result
        raise ExprError("unsupported bool op")
    if isinstance(node, ast.BinOp):
        if not isinstance(node.op, _ALLOWED_BINOP):
            raise ExprError("unsupported binary op")
        left, right = _eval(node.left, scope, depth + 1), _eval(node.right, scope, depth + 1)
        return _apply_binop(node.op, left, right)
    if isinstance(node, ast.Compare):
        if not all(isinstance(op, _ALLOWED_CMP) for op in node.ops):
            raise ExprError("unsupported comparison")
        left = _eval(node.left, scope, depth + 1)
        for op, comp in zip(node.ops, node.comparators):
            right = _eval(comp, scope, depth + 1)
            if not _apply_cmp(op, left, right):
                return False
            left = right
        return True
    if isinstance(node, ast.IfExp):
        return _eval(node.body, scope, depth + 1) if _eval(node.test, scope, depth + 1) \
            else _eval(node.orelse, scope, depth + 1)
    raise ExprError(f"unsupported expression node: {type(node).__name__}")


def _apply_binop(op: ast.operator, left: Any, right: Any) -> Any:
    try:
        if isinstance(op, ast.Add):
            return left + right
        if isinstance(op, ast.Sub):
            return left - right
        if isinstance(op, ast.Mult):
            # 字符串/序列乘法结果长度上限——双向检查：int 在左同样能构造
            # 巨量字符串（10**9 * "x" 绕过旧守卫即 OOM Worker 进程）
            if isinstance(right, int):
                if isinstance(left, str):
                    if len(left) * right > _MAX_STR_MUL:
                        raise ExprError("string multiplication result too large")
                elif isinstance(left, (list, tuple, bytes)):
                    if len(left) * right > _MAX_LIST_MUL:
                        raise ExprError("sequence multiplication result too large")
            elif isinstance(left, int):
                if isinstance(right, str):
                    if len(right) * left > _MAX_STR_MUL:
                        raise ExprError("string multiplication result too large")
                elif isinstance(right, (list, tuple, bytes)):
                    if len(right) * left > _MAX_LIST_MUL:
                        raise ExprError("sequence multiplication result too large")
            return left * right
        if isinstance(op, ast.Div):
            return left / right
        if isinstance(op, ast.Mod):
            return left % right
        if isinstance(op, ast.FloorDiv):
            return left // right
        raise ExprError("unsupported binary op")
    except TypeError as e:
        raise ExprError(f"type error: {e}") from e


def _apply_cmp(op: ast.cmpop, left: Any, right: Any) -> bool:
    if isinstance(op, ast.Eq):
        return left == right
    if isinstance(op, ast.NotEq):
        return left != right
    if isinstance(op, ast.Lt):
        return left < right
    if isinstance(op, ast.LtE):
        return left <= right
    if isinstance(op, ast.Gt):
        return left > right
    if isinstance(op, ast.GtE):
        return left >= right
    if isinstance(op, ast.In):
        return left in right
    if isinstance(op, ast.NotIn):
        return left not in right
    raise ExprError("unsupported comparison")


def eval_expr(expr: str, scope: Mapping[str, Any]) -> Any:
    """求值单条受限表达式。"""
    expr = expr.strip()
    if not expr:
        raise ExprError("empty expression")
    if len(expr) > _MAX_EXPR_LEN:
        raise ExprError(f"expression too long ({len(expr)} > {_MAX_EXPR_LEN})")
    try:
        tree = ast.parse(expr, mode="eval")
    except SyntaxError as e:
        raise ExprError(f"syntax error: {e.msg}") from e
    try:
        return _eval(tree, scope)
    except RecursionError:
        raise ExprError("expression too deeply nested") from None


# ---- 模板渲染 ----

def render(value: Any, scope: Mapping[str, Any]) -> Any:
    """渲染字符串模板；非字符串原样返回。

    整体为单个 {{ expr }} 时返回原生类型（便于 SET_VAR 提取结构）。
    """
    if not isinstance(value, str):
        return value
    if len(value) > _MAX_EXPR_LEN * 10:
        raise ExprError("template too long")
    m = _FULL_RE.match(value)
    # 捕获组内不得再含模板定界符：形如 "{{a}}/api/{{b}}" 的多片段模板
    # 首尾恰为 {{ }}，若误判为单表达式会把 "}}/api/{{" 吞进表达式。
    if m and "{{" not in m.group(1) and "}}" not in m.group(1):
        return eval_expr(m.group(1), scope)

    def _sub(match: re.Match[str]) -> str:
        v = eval_expr(match.group(1), scope)
        return "" if v is None else str(v)

    return _TEMPLATE_RE.sub(_sub, value)


def render_map(pairs: Any, scope: Mapping[str, Any]) -> dict[str, str]:
    """渲染 KeyValue 列表（[{key,value}] 或 {k:v}）为 dict。"""
    out: dict[str, str] = {}
    if isinstance(pairs, Mapping):
        for k, v in pairs.items():
            out[str(k)] = str(render(v, scope))
        return out
    for kv in pairs or []:
        key = render(getattr(kv, "key", ""), scope)
        val = render(getattr(kv, "value", ""), scope)
        if key:
            out[str(key)] = "" if val is None else str(val)
    return out
