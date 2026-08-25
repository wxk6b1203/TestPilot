"""grounding 与 prompt 模板相关单测。

- src/testpilot_copilot/grounding/ 只有数据文件（domain-schema.json / sdk-api.md）
- src/testpilot_copilot/prompts/ 是 prompt 模板（system.md / summarizer.md）
- agent.py 的 build_instructions() 读模板并注入 schema/sdk 文档（不触网）

这里覆盖默认组装、数据文件 sanity、自定义模板与缺失路径报错。
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from testpilot_copilot.agent import _PROMPTS, _build_instructions, build_instructions


def test_instructions_embed_schema_and_sdk_verbatim():
    schema = (_PROMPTS.parent / "grounding" / "domain-schema.json").read_text(encoding="utf-8")
    sdk_doc = (_PROMPTS.parent / "grounding" / "sdk-api.md").read_text(encoding="utf-8")
    text = _build_instructions()
    assert schema in text
    assert sdk_doc in text


def test_instructions_structure():
    text = _build_instructions()
    assert "## 工作准则" in text
    assert "## Playwright UI 用例生成" in text
    assert "## 数据字典（领域 schema）" in text
    assert "## 低代码 SDK" in text
    assert "update_api" in text and "update_test_case" in text
    # 使用准则在 grounding 段落之前；schema 段落在 SDK 段落之前
    assert text.index("## Playwright UI 用例生成") < text.index("## 数据字典")
    assert text.index("## 数据字典") < text.index("## 低代码 SDK")


def test_grounding_files_exist_and_nonempty():
    grounding = _PROMPTS.parent / "grounding"
    schema_path = grounding / "domain-schema.json"
    sdk_path = grounding / "sdk-api.md"
    assert schema_path.is_file() and schema_path.stat().st_size > 0
    assert sdk_path.is_file() and sdk_path.stat().st_size > 0


def test_grounding_sdk_documents_playwright_page_model():
    grounding = _PROMPTS.parent / "grounding"
    sdk_doc = (grounding / "sdk-api.md").read_text(encoding="utf-8")
    assert "## Page（Playwright UI 用例" in sdk_doc
    assert "ctx.page.fill" in sdk_doc
    assert "expect_text" in sdk_doc and "wait_for" in sdk_doc
    assert "expect_hidden" in sdk_doc and "wait_for_selector" in sdk_doc
    # 防止 LLM 生成沙箱内不可用的 playwright import
    assert "禁止 `from playwright" in sdk_doc


def test_domain_schema_is_valid_json_with_expected_shape():
    grounding = _PROMPTS.parent / "grounding"
    schema = json.loads((grounding / "domain-schema.json").read_text(encoding="utf-8"))
    assert isinstance(schema, dict)
    assert schema["messages"]  # proto 消息字典非空
    assert schema["enums"]     # 枚举字典非空
    # prompt 组装依赖该路径指向包内 grounding 目录
    assert Path(grounding).name == "grounding"


# ---------------------------------------------------------------------------
# Prompt 模板可配置
# ---------------------------------------------------------------------------

def test_default_prompt_templates_exist_and_have_placeholders():
    system = (_PROMPTS / "system.md").read_text(encoding="utf-8")
    summarizer = (_PROMPTS / "summarizer.md").read_text(encoding="utf-8")
    assert "{{schema}}" in system
    assert "{{sdk_doc}}" in system
    assert "上下文压缩器" in summarizer


def test_custom_system_prompt_file_replaces_default(tmp_path):
    custom = tmp_path / "custom-system.md"
    custom.write_text("你是自定义测试助手。\n\n数据：\n{{schema}}\n", encoding="utf-8")
    text = build_instructions(str(custom))
    assert "你是自定义测试助手" in text
    assert "## 工作准则" not in text
    assert "数据字典" not in text
    assert "domain-schema" not in text  # 确认注入的是 schema 内容而非默认模板


def test_missing_system_prompt_file_raises():
    with pytest.raises(RuntimeError, match="system prompt 文件不可读"):
        build_instructions("/no/such/copilot-prompt.md")
