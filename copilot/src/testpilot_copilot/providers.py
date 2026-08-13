"""LLM Provider 注册表：按枚举选择 Provider（用户要求：OpenAI 兼容端点 + Provider 参数形式）。

新增 Provider：在 _BUILDERS 注册一个构造函数即可。
"""

from __future__ import annotations

from collections.abc import Callable

from pydantic_ai.models.openai import OpenAIChatModel
from pydantic_ai.providers.deepseek import DeepSeekProvider
from pydantic_ai.providers.openai import OpenAIProvider

from .config import Settings


def _deepseek(s: Settings) -> OpenAIChatModel:
    """DeepSeek 官方端点（OpenAI 兼容）；base_url 可覆盖为自建网关。"""
    if s.base_url:
        from openai import AsyncOpenAI
        provider = DeepSeekProvider(
            openai_client=AsyncOpenAI(api_key=s.api_key, base_url=s.base_url))
    else:
        provider = DeepSeekProvider(api_key=s.api_key)  # 默认 https://api.deepseek.com
    return OpenAIChatModel(s.model, provider=provider)


def _openai_compatible(s: Settings) -> OpenAIChatModel:
    """任意 OpenAI 兼容端点（Moonshot/Qwen/vLLM/One-API 网关等）。"""
    provider = OpenAIProvider(api_key=s.api_key, base_url=s.base_url or None)
    return OpenAIChatModel(s.model, provider=provider)


_BUILDERS: dict[str, Callable[[Settings], OpenAIChatModel]] = {
    "deepseek": _deepseek,
    "openai_compatible": _openai_compatible,
}


def build_model(s: Settings, *, model: str | None = None) -> OpenAIChatModel:
    builder = _BUILDERS.get(s.provider)
    if builder is None:
        raise ValueError(f"unknown provider '{s.provider}' (valid: {', '.join(_BUILDERS)})")
    if model:
        s = Settings(**{**s.__dict__, "model": model})
    return builder(s)
