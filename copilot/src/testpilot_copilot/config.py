"""Copilot 配置：三级覆盖 —— 显式 CLI > 环境变量 > .env > YAML > 内置默认。

YAML 路径：--config > TP_COPILOT_CONFIG > ./copilot.yaml（存在才加载）。
api_key 不走 CLI（进程列表可见），仅 env / .env / YAML。
.env 回填：仅当变量未在环境中设置时写入 os.environ（开发便利；生产用真环境变量），
下游 tracing 等模块按原 env 约定读取。
"""

from __future__ import annotations

import argparse
import os
from dataclasses import dataclass
from pathlib import Path

import yaml

_ENV_FILE = Path(__file__).resolve().parents[2] / ".env"


def _parse_dotenv() -> dict[str, str]:
    if not _ENV_FILE.exists():
        return {}
    out: dict[str, str] = {}
    for line in _ENV_FILE.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        k, v = line.split("=", 1)
        out[k.strip()] = v.strip().strip('"').strip("'")
    return out


def _backfill_environ(dotenv: dict[str, str]) -> None:
    for k, v in dotenv.items():
        os.environ.setdefault(k, v)


_backfill_environ(_parse_dotenv())

# 字段表：dest → (yaml 键, env 键, 默认, 类型, 是否开 CLI)
_FIELDS: dict[str, tuple[str, str, object, type, bool]] = {
    # LLM Provider（registry key：deepseek / openai_compatible）
    "provider":         ("provider", "TP_COPILOT_PROVIDER", "deepseek", str, True),
    "api_key":          ("api_key", "TP_COPILOT_API_KEY", "", str, False),
    "base_url":         ("base_url", "TP_COPILOT_BASE_URL", "", str, True),  # 空 = provider 默认
    "model":            ("model", "TP_COPILOT_MODEL", "deepseek-v4-flash", str, True),
    # 摘要器（上下文压缩用）默认同主模型；可配更便宜的小模型
    "summarizer_model": ("summarizer_model", "TP_COPILOT_SUMMARIZER_MODEL", "", str, True),
    # Prompt 模板路径：空 = 使用包内置 prompts/system.md / prompts/summarizer.md
    "system_prompt_file":     ("system_prompt_file", "TP_COPILOT_SYSTEM_PROMPT_FILE", "", str, True),
    "summarizer_prompt_file": ("summarizer_prompt_file", "TP_COPILOT_SUMMARIZER_PROMPT_FILE", "", str, True),
    "context_window":   ("context_window", "TP_COPILOT_CONTEXT_WINDOW", 64000, int, True),
    "scheduler_grpc":   ("scheduler_grpc", "TP_SCHEDULER_GRPC", "127.0.0.1:9090", str, True),
    "scheduler_rest":   ("scheduler_rest", "TP_SCHEDULER_REST", "http://127.0.0.1:8080", str, True),
    "http_addr":        ("http_addr", "TP_COPILOT_ADDR", "0.0.0.0:8100", str, True),
    "http_timeout":     ("http_timeout", "TP_COPILOT_HTTP_TIMEOUT", 15.0, float, True),  # 调 Scheduler REST 超时（秒）
    "otel_exporter":    ("otel_exporter", "TP_OTEL_EXPORTER", "", str, True),  # ""|stdout|otlp
    "otel_endpoint":    ("otel_endpoint", "TP_OTEL_ENDPOINT", "127.0.0.1:4317", str, True),
}


@dataclass(frozen=True)
class Settings:
    provider: str = "deepseek"
    api_key: str = ""
    base_url: str = ""
    model: str = "deepseek-v4-flash"
    summarizer_model: str = ""
    system_prompt_file: str = ""
    summarizer_prompt_file: str = ""
    context_window: int = 64000
    scheduler_grpc: str = "127.0.0.1:9090"
    scheduler_rest: str = "http://127.0.0.1:8080"
    http_addr: str = "0.0.0.0:8100"
    http_timeout: float = 15.0
    otel_exporter: str = ""
    otel_endpoint: str = "127.0.0.1:4317"

    def validate(self) -> None:
        if not self.api_key:
            raise RuntimeError("TP_COPILOT_API_KEY 未设置（见 copilot/.env.example）")


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(prog="testpilot-copilot", description="TestPilot Copilot")
    p.add_argument("--config", default="", help="YAML 配置路径（> TP_COPILOT_CONFIG > ./copilot.yaml）")
    for dest, (key, _env, _default, typ, cli_ok) in _FIELDS.items():
        if not cli_ok:
            continue
        flag = "--" + dest.replace("_", "-")
        # 默认 None 以区分「未传」与「显式传了默认值」（优先级判定依赖）
        p.add_argument(flag, dest=dest, type=typ if typ is not str else None,
                       default=None, help=key)
    return p


def _config_path(args: argparse.Namespace) -> str:
    if args.config:
        return args.config
    if os.environ.get("TP_COPILOT_CONFIG"):
        return os.environ["TP_COPILOT_CONFIG"]
    return "copilot.yaml" if Path("copilot.yaml").is_file() else ""


def resolve(args: argparse.Namespace, env: dict[str, str] | None = None,
            dotenv: dict[str, str] | None = None) -> Settings:
    """合成最终配置：CLI(非 None) > env > .env > YAML > 默认。"""
    env = os.environ if env is None else env
    dotenv = _parse_dotenv() if dotenv is None else dotenv
    ydoc: dict = {}
    path = _config_path(args)
    if path:
        ydoc = yaml.safe_load(Path(path).read_text(encoding="utf-8")) or {}
        if not isinstance(ydoc, dict):
            raise SystemExit(f"config {path}: 顶层必须是 mapping")

    values: dict[str, object] = {}
    for dest, (key, env_key, default, typ, _cli) in _FIELDS.items():
        v: object = default
        if key in ydoc:
            v = ydoc[key]
        if dotenv.get(env_key):
            v = dotenv[env_key]
        if env.get(env_key):
            v = env[env_key]
        cli = getattr(args, dest, None)
        if cli is not None:
            v = cli
        if typ is not str:
            try:
                v = typ(v)
            except (TypeError, ValueError):
                raise SystemExit(f"config {key}: {v!r} 类型应为 {typ.__name__}") from None
        values[dest] = v
    return Settings(**values)


def load(argv: list[str] | None = None, env: dict[str, str] | None = None,
         dotenv: dict[str, str] | None = None) -> Settings:
    return resolve(build_parser().parse_args(argv), env, dotenv)


def apply_environ(s: Settings) -> None:
    """把解析结果回写环境：entry() 的 CLI 覆盖经此提升为 env 层，
    lifespan 内 load() 与 tracing 等下游按原约定读取即得最终值。
    api_key 刻意不回写（对比 worker 侧 token 的处理）：/proc/<pid>/environ
    可读、子进程继承，密钥只应存在于进程内存。"""
    for dest, (_key, env_key, _default, _typ, _cli) in _FIELDS.items():
        if env_key == "TP_COPILOT_API_KEY":
            continue
        os.environ[env_key] = str(getattr(s, dest))
