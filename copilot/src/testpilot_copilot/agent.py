"""Copilot Agent 装配：grounding 指令 + 工具集 + 上下文压缩。"""

from __future__ import annotations

from pathlib import Path

from pydantic_ai import Agent, DeferredToolRequests
from pydantic_ai_extensions import ContextCompression

from .config import Settings
from .providers import build_model
from .tools import CopilotDeps, readonly, writes

_GROUNDING = Path(__file__).parent / "grounding"


def _build_instructions() -> str:
    schema = (_GROUNDING / "domain-schema.json").read_text(encoding="utf-8")
    sdk_doc = (_GROUNDING / "sdk-api.md").read_text(encoding="utf-8")
    return f"""你是 TestPilot 的 AI Copilot —— 集成测试平台的内置助手，帮助用户：生成/维护 HTTP 接口与测试用例、分析运行失败根因、做覆盖率分析、触发运行与压测。

## 工作准则
- 始终用中文回答，简洁直接。
- 需要先了解现状再行动：写用例前先 query_schema 查数据字典，再 list_apis/get_api 看接口定义；分析失败先 get_run(include_steps=true)。
- 所有写操作（create_*/import_openapi/trigger_*）都会向用户发起审批，你只需发起调用；不要重复发起已被拒绝的调用。
- definition 等 JSON 参数必须严格符合数据字典中的结构（字段名 camelCase）。
- 不确定项目 ID 时先 list_projects。

## 数据字典（领域 schema）
{schema}

## 低代码 SDK（case_type=lowcode 时 definition.source 的编程接口）
{sdk_doc}
"""


def build_agent(settings: Settings) -> Agent[CopilotDeps, str]:
    summarizer_model = build_model(settings, model=settings.summarizer_model or settings.model)
    summarizer = Agent(
        summarizer_model,
        instructions="你是上下文压缩器。把对话历史压缩为简洁摘要，保留：用户目标、已创建的实体及其 ID、关键参数、失败原因、当前进度。输出纯文本。",
        output_type=str,
    )
    return Agent(
        build_model(settings),
        instructions=_build_instructions(),
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
