"""LLM Provider 注册表：按枚举选择 Provider（用户要求：OpenAI 兼容端点 + Provider 参数形式）。

新增 Provider：在 _BUILDERS 注册一个构造函数即可。
"""

from __future__ import annotations

from collections.abc import Callable

import httpx
from openai import AsyncOpenAI
from pydantic_ai.models.openai import OpenAIChatModel
from pydantic_ai.providers.deepseek import DeepSeekProvider
from pydantic_ai.providers.openai import OpenAIProvider

from .config import Settings


def _model_timeout(s: Settings) -> httpx.Timeout | None:
    """OpenAI 兼容客户端的读超时。

    流式请求中 read 超时等价于「首 token / token 间空闲上限」；0 或负数 = 不限。
    connect 保持 10s，避免 DNS/建连阶段跟着 model_timeout 一起放大。
    """
    if s.model_timeout <= 0:
        return None
    return httpx.Timeout(timeout=s.model_timeout, connect=10.0)


def _client(s: Settings, base_url: str | None = None) -> AsyncOpenAI:
    kwargs: dict = {"api_key": s.api_key}
    if base_url:
        kwargs["base_url"] = base_url
    kwargs["timeout"] = _model_timeout(s)  # None=不限（显式覆盖 SDK 默认 600s）
    return AsyncOpenAI(**kwargs)


def _deepseek(s: Settings) -> OpenAIChatModel:
    """DeepSeek 官方端点（OpenAI 兼容）；base_url 可覆盖为自建网关。"""
    provider = DeepSeekProvider(
        openai_client=_client(s, s.base_url or "https://api.deepseek.com"))
    return OpenAIChatModel(s.model, provider=provider)


def _openai_compatible(s: Settings) -> OpenAIChatModel:
    """任意 OpenAI 兼容端点（Moonshot/Qwen/vLLM/One-API 网关等）。"""
    provider = OpenAIProvider(openai_client=_client(s, s.base_url or None))
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
