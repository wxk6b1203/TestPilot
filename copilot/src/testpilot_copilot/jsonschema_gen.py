"""JSON → JSON Schema 推断（copilot 侧，供 create/update_data_model 的 json 参数）。

与 web/src/lib/jsonSchema.ts 的 jsonToSchema 保持同一套类型语义：
null→any / bool→boolean / 整数→integer / 浮点→number / 字符串→string /
数组→array(items 归并) / 对象→object(properties 逐字段)。
"""

from __future__ import annotations

from typing import Any


def json_to_schema(value: Any) -> dict[str, Any]:
    """从示例 JSON 推断 JSON Schema（draft-07 子集）。"""
    if value is None:
        return {"type": "any"}
    if isinstance(value, bool):  # bool 必须在 int 之前判（bool 是 int 子类）
        return {"type": "boolean"}
    if isinstance(value, int):
        return {"type": "integer"}
    if isinstance(value, float):
        return {"type": "number"}
    if isinstance(value, str):
        return {"type": "string"}
    if isinstance(value, list):
        items = _merge_schemas([json_to_schema(x) for x in value])
        return {"type": "array", "items": items or {"type": "any"}}
    if isinstance(value, dict):
        return {
            "type": "object",
            "properties": {str(k): json_to_schema(v) for k, v in value.items()},
        }
    return {"type": "any"}


def _merge_schemas(schemas: list[dict[str, Any]]) -> dict[str, Any] | None:
    """多字段归并：同类型标量取该类型；object 深合并 properties；类型不一致兜底 any。"""
    if not schemas:
        return None
    first = schemas[0]
    if all(s.get("type") == "object" for s in schemas):
        properties: dict[str, Any] = dict(first.get("properties") or {})
        for s in schemas[1:]:
            for k, v in (s.get("properties") or {}).items():
                if k in properties:
                    merged = _merge_schemas([properties[k], v])
                    properties[k] = merged or v
                else:
                    properties[k] = v
        return {"type": "object", "properties": properties}
    if all(s.get("type") == first.get("type") for s in schemas):
        return first
    return {"type": "any"}
