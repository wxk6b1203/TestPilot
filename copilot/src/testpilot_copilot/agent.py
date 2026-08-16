"""Copilot Agent 装配：grounding 指令 + 工具集 + 上下文压缩。

Prompt 模板默认位于 prompts/system.md / prompts/summarizer.md；
可通过 Settings.system_prompt_file / summarizer_prompt_file 指向自定义文件。
模板占位符：{{schema}}（领域数据字典）、{{sdk_doc}}（低代码 SDK 文档）。
"""

from __future__ import annotations

import logging
from pathlib import Path

from pydantic_ai import Agent, DeferredToolRequests
from pydantic_ai_extensions import ContextCompression

from .config import Settings
from .providers import build_model
from .tools import CopilotDeps, readonly, writes

log = logging.getLogger("testpilot.copilot")

_GROUNDING = Path(__file__).parent / "grounding"
_PROMPTS = Path(__file__).parent / "prompts"

_SYSTEM_PROMPT_FILE = "system.md"
_SUMMARIZER_PROMPT_FILE = "summarizer.md"
_SYSTEM_PLACEHOLDERS = ("{{schema}}", "{{sdk_doc}}")


def _read_prompt(prompt_file: str, default_name: str, label: str) -> str:
    path = Path(prompt_file).expanduser() if prompt_file else _PROMPTS / default_name
    try:
        return path.read_text(encoding="utf-8")
    except OSError as e:
        raise RuntimeError(f"{label} prompt 文件不可读：{path}") from e


def _render_system_prompt(template: str, schema: str, sdk_doc: str) -> str:
    for placeholder in _SYSTEM_PLACEHOLDERS:
        if placeholder not in template:
            log.warning("system prompt 模板缺少占位符 %s，对应 grounding 不会注入", placeholder)
    return template.replace("{{schema}}", schema).replace("{{sdk_doc}}", sdk_doc)


def build_instructions(prompt_file: str = "") -> str:
    """组装主 agent 的 system prompt；prompt_file 为空时使用包内置模板。"""
    schema = (_GROUNDING / "domain-schema.json").read_text(encoding="utf-8")
    sdk_doc = (_GROUNDING / "sdk-api.md").read_text(encoding="utf-8")
    template = _read_prompt(prompt_file, _SYSTEM_PROMPT_FILE, "system")
    return _render_system_prompt(template, schema, sdk_doc)


def _build_instructions() -> str:
    return build_instructions()


def build_summarizer_instructions(prompt_file: str = "") -> str:
    return _read_prompt(prompt_file, _SUMMARIZER_PROMPT_FILE, "summarizer").strip()


def build_agent(settings: Settings) -> Agent[CopilotDeps, str]:
    summarizer_model = build_model(settings, model=settings.summarizer_model or settings.model)
    summarizer = Agent(
        summarizer_model,
        instructions=build_summarizer_instructions(settings.summarizer_prompt_file),
        output_type=str,
    )
    return Agent(
        build_model(settings),
        instructions=build_instructions(settings.system_prompt_file),
        deps_type=CopilotDeps,
        output_type=[str, DeferredToolRequests],  # 审批型工具 → 挂起交前端 HITL
        toolsets=[readonly, writes],
        capabilities=[
            ContextCompression(
                summarizer,
                compress_threshold=("fraction", 0.7),
                max_tokens=settings.context_window,
                keep=("messages", 6),
            ),
        ],
    )
