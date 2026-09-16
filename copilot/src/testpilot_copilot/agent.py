"""Copilot Agent 装配：grounding 指令 + 工具集 + 上下文压缩。

Prompt 模板默认位于 prompts/system.md / prompts/summarizer.md；
可通过 Settings.system_prompt_file / summarizer_prompt_file 指向自定义文件。
模板占位符：{{schema}}（领域数据字典）、{{sdk_doc}}（低代码 SDK 文档）、
{{language_directive}}（默认回复语言指令，可被请求头 X-TP-Lang 按请求覆盖）。
"""

from __future__ import annotations

import logging
from pathlib import Path

from pydantic_ai import Agent, DeferredToolRequests, ModelSettings, RunContext
from pydantic_ai_extensions import ContextCompression

from .config import Settings
from .providers import build_model
from .tools import CopilotDeps, probe, readonly, writes

log = logging.getLogger("testpilot.copilot")

_GROUNDING = Path(__file__).parent / "grounding"
_PROMPTS = Path(__file__).parent / "prompts"

_SYSTEM_PROMPT_FILE = "system.md"
_SUMMARIZER_PROMPT_FILE = "summarizer.md"
_SYSTEM_PLACEHOLDERS = ("{{schema}}", "{{sdk_doc}}", "{{language_directive}}")

# 回复语言指令：{{language_directive}} 占位符的取值。
# 默认语言由 Settings.default_language 决定（zh），单次请求可被 X-TP-Lang 头覆盖
# （main.py → CopilotDeps.language → 动态指令，见 build_agent）；未知值回退中文。
_LANGUAGE_DIRECTIVES: dict[str, str] = {
    "zh": "始终用中文回答，简洁直接。",
    "en": "Always respond in English. Be concise and direct.",
}


def _language_directive(lang: str) -> str:
    return _LANGUAGE_DIRECTIVES.get((lang or "").strip().lower(), _LANGUAGE_DIRECTIVES["zh"])


def _read_prompt(prompt_file: str, default_name: str, label: str) -> str:
    path = Path(prompt_file).expanduser() if prompt_file else _PROMPTS / default_name
    try:
        return path.read_text(encoding="utf-8")
    except OSError as e:
        raise RuntimeError(f"{label} prompt file is unreadable: {path}") from e


def _render_system_prompt(template: str, schema: str, sdk_doc: str, directive: str) -> str:
    for placeholder in _SYSTEM_PLACEHOLDERS:
        if placeholder not in template:
            log.warning("system prompt template is missing placeholder %s; grounding will not be injected", placeholder)
    return (template
            .replace("{{schema}}", schema)
            .replace("{{sdk_doc}}", sdk_doc)
            .replace("{{language_directive}}", directive))


def build_instructions(prompt_file: str = "", default_language: str = "zh") -> str:
    """组装主 agent 的 system prompt；prompt_file 为空时使用包内置模板。

    {{schema}} 注入的是数据字典“目录”（schema-toc.md，由 scripts/gen_grounding.py
    从 proto 同步生成）：实体 → 字段名一览 + 按需查询指引；完整定义由 LLM 经
    query_schema(topic=...) 分片拉取进消息历史（可被上下文压缩回收），避免每轮
    固定注入 14KB 全量 schema。
    {{language_directive}} 由 default_language 解析为默认回复语言指令。
    """
    schema = (_GROUNDING / "schema-toc.md").read_text(encoding="utf-8")
    sdk_doc = (_GROUNDING / "sdk-api.md").read_text(encoding="utf-8")
    template = _read_prompt(prompt_file, _SYSTEM_PROMPT_FILE, "system")
    return _render_system_prompt(template, schema, sdk_doc,
                                 _language_directive(default_language))


def _build_instructions() -> str:
    return build_instructions()


def build_summarizer_instructions(prompt_file: str = "") -> str:
    return _read_prompt(prompt_file, _SUMMARIZER_PROMPT_FILE, "summarizer").strip()


def _model_settings(temperature: float | None, top_p: float | None) -> ModelSettings | None:
    """采样参数只包含显式配置的键；全未配置时返回 None（请求不带字段，按 Provider 默认）。"""
    ms: ModelSettings = {}
    if temperature is not None:
        ms["temperature"] = temperature
    if top_p is not None:
        ms["top_p"] = top_p
    return ms or None


def build_agent(settings: Settings) -> Agent[CopilotDeps, str]:
    summarizer_model = build_model(settings, model=settings.summarizer_model or settings.model)
    summarizer = Agent(
        summarizer_model,
        instructions=build_summarizer_instructions(settings.summarizer_prompt_file),
        output_type=str,
        model_settings=_model_settings(settings.summarizer_temperature, settings.summarizer_top_p),
    )
    agent = Agent(
        build_model(settings),
        instructions=build_instructions(settings.system_prompt_file,
                                        default_language=settings.default_language),
        deps_type=CopilotDeps,
        output_type=[str, DeferredToolRequests],  # 审批型工具 → 挂起交前端 HITL
        toolsets=[readonly, writes, probe],
        model_settings=_model_settings(settings.temperature, settings.top_p),
        capabilities=[
            ContextCompression(
                summarizer,
                compress_threshold=("fraction", 0.7),
                max_tokens=settings.context_window,
                keep=("messages", 6),
            ),
        ],
    )

    # 回复语言按请求覆盖：前端每次 chat 带 X-TP-Lang 头（zh/en），main.py 解析后
    # 放进 CopilotDeps.language；未带头时返回空串，走 system prompt 的默认语言。
    # 指令写成显式 override 措辞，避免与模板里默认语言指令冲突时被模型忽略。
    @agent.instructions
    def _reply_language(ctx: RunContext[CopilotDeps]) -> str:
        lang = (getattr(ctx.deps, "language", "") or "").strip().lower()
        if not lang:
            return ""
        name = {"zh": "Chinese (Simplified)", "en": "English"}.get(lang, lang)
        return (f"The user interface language is {name}. "
                f"Always respond in {name}, overriding any other language instruction.")

    return agent
