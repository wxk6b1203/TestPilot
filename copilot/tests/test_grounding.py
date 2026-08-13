"""grounding 相关单测。

src/testpilot_copilot/grounding/ 只有数据文件（domain-schema.json / sdk-api.md），
无 Python 逻辑；唯一消费它们的纯函数是 agent.py 的 _build_instructions()
（prompt 组装，仅读包内文件，不触网）。这里测组装结果与数据文件 sanity。
"""

from __future__ import annotations

import json
from pathlib import Path

from testpilot_copilot.agent import _GROUNDING, _build_instructions


def test_instructions_embed_schema_and_sdk_verbatim():
    schema = (_GROUNDING / "domain-schema.json").read_text(encoding="utf-8")
    sdk_doc = (_GROUNDING / "sdk-api.md").read_text(encoding="utf-8")
    text = _build_instructions()
    assert schema in text
    assert sdk_doc in text


def test_instructions_structure():
    text = _build_instructions()
    assert "## 工作准则" in text
    assert "## 数据字典（领域 schema）" in text
    assert "## 低代码 SDK" in text
    # schema 段落在 SDK 段落之前
    assert text.index("## 数据字典") < text.index("## 低代码 SDK")


def test_grounding_files_exist_and_nonempty():
    schema_path = _GROUNDING / "domain-schema.json"
    sdk_path = _GROUNDING / "sdk-api.md"
    assert schema_path.is_file() and schema_path.stat().st_size > 0
    assert sdk_path.is_file() and sdk_path.stat().st_size > 0


def test_domain_schema_is_valid_json_with_expected_shape():
    schema = json.loads((_GROUNDING / "domain-schema.json").read_text(encoding="utf-8"))
    assert isinstance(schema, dict)
    assert schema["messages"]  # proto 消息字典非空
    assert schema["enums"]     # 枚举字典非空
    # prompt 组装依赖该路径指向包内 grounding 目录
    assert Path(_GROUNDING).name == "grounding"
