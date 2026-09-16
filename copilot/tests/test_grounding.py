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
    grounding = _PROMPTS.parent / "grounding"
    toc = (grounding / "schema-toc.md").read_text(encoding="utf-8")
    sdk_doc = (grounding / "sdk-api.md").read_text(encoding="utf-8")
    text = _build_instructions()
    assert toc in text      # {{schema}} 注入的是目录（非全量 schema）
    assert sdk_doc in text


def test_instructions_use_toc_not_full_schema():
    """system prompt 只注入 schema 目录（省 token）：全量定义经 query_schema(topic) 按需拉。"""
    grounding = _PROMPTS.parent / "grounding"
    toc = (grounding / "schema-toc.md").read_text(encoding="utf-8")
    full = (grounding / "domain-schema.json").read_text(encoding="utf-8")
    text = _build_instructions()
    assert toc in text                       # 目录已注入
    assert full not in text                  # 全量 schema 不再注入
    assert "- HttpApi:" in toc and "- TestStep:" in toc   # 实体清单
    assert "query_schema(topic=" in toc      # 按需查询指引
    assert "以上是数据字典目录" in text


def test_instructions_structure():
    text = _build_instructions()
    assert "## 工作准则" in text
    assert "## Playwright UI 用例生成" in text
    assert "## 数据字典目录" in text      # {{schema}} 注入的 TOC 自带章节标题
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
    assert "## 数据字典（领域 schema）" not in text   # 默认模板章节标题不应出现（TOC 内容自带“数据字典目录”字样属正常）
    assert "domain-schema" not in text  # 确认注入的是 schema 内容而非默认模板


def test_missing_system_prompt_file_raises():
    with pytest.raises(RuntimeError, match="prompt file is unreadable"):
        build_instructions("/no/such/copilot-prompt.md")


def test_instructions_language_directive():
    """{{language_directive}} 占位符按 default_language 解析；未知值回退中文。"""
    zh = build_instructions()
    assert "始终用中文回答，简洁直接。" in zh
    assert "{{language_directive}}" not in zh
    en = build_instructions(default_language="en")
    assert "Always respond in English" in en and "始终用中文回答" not in en
    fallback = build_instructions(default_language="fr")
    assert "始终用中文回答，简洁直接。" in fallback
