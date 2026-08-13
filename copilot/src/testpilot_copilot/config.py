"""Copilot 配置：环境变量驱动，Provider 可选（枚举注册表）。

.env 加载：仅当变量未在环境中设置时回填（开发便利；生产用真环境变量）。
"""

from __future__ import annotations

import os
from dataclasses import dataclass
from pathlib import Path

_ENV_FILE = Path(__file__).resolve().parents[2] / ".env"


def _load_dotenv() -> None:
    if not _ENV_FILE.exists():
        return
    for line in _ENV_FILE.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        k, v = line.split("=", 1)
        os.environ.setdefault(k.strip(), v.strip().strip('"').strip("'"))


_load_dotenv()


@dataclass(frozen=True)
class Settings:
    # LLM Provider（registry key：deepseek / openai_compatible / anthropic_compatible）
    provider: str = os.environ.get("TP_COPILOT_PROVIDER", "deepseek")
    api_key: str = os.environ.get("TP_COPILOT_API_KEY", "")
    base_url: str = os.environ.get("TP_COPILOT_BASE_URL", "")  # 空 = provider 默认
    model: str = os.environ.get("TP_COPILOT_MODEL", "deepseek-v4-flash")
    # 摘要器（上下文压缩用）默认同主模型；可配更便宜的小模型
    summarizer_model: str = os.environ.get("TP_COPILOT_SUMMARIZER_MODEL", "")
    context_window: int = int(os.environ.get("TP_COPILOT_CONTEXT_WINDOW", "64000"))

    scheduler_grpc: str = os.environ.get("TP_SCHEDULER_GRPC", "127.0.0.1:9090")
    scheduler_rest: str = os.environ.get("TP_SCHEDULER_REST", "http://127.0.0.1:8080")
    http_addr: str = os.environ.get("TP_COPILOT_ADDR", "0.0.0.0:8100")

    def validate(self) -> None:
        if not self.api_key:
            raise RuntimeError("TP_COPILOT_API_KEY 未设置（见 copilot/.env.example）")


def load() -> Settings:
    return Settings()
