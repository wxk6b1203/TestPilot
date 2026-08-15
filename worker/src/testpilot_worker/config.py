"""Worker 配置：三级覆盖 —— 显式 CLI 参数 > 环境变量 > YAML > 内置默认。

YAML 路径：--config > TP_WORKER_CONFIG > ./worker.yaml（存在才加载）。
解析结果在 entry() 回写 os.environ：沙箱子进程（TP_SANDBOX_*）、egress、
ui 产物目录、tracing 等模块继续按原有 env 约定读取，下游零改动。
"""

from __future__ import annotations

import argparse
import os
from dataclasses import dataclass
from pathlib import Path

import yaml

# 字段表：dest → (yaml 键, 环境变量, 默认值, 类型)
# 环境变量沿用既有文档化键名（TP_ARTIFACT_DIR / TP_EGRESS_* / TP_SANDBOX_* / TP_OTEL_*）。
_FIELDS: dict[str, tuple[str, str, object, type]] = {
    "scheduler":       ("scheduler", "TP_WORKER_SCHEDULER", "127.0.0.1:9090", str),
    "token":           ("token", "TP_WORKER_TOKEN", "", str),  # gRPC 认证令牌（须与 Scheduler worker_token 一致）
    "name":            ("name", "TP_WORKER_NAME", "", str),  # 空=主机名
    "capabilities":    ("capabilities", "TP_WORKER_CAPABILITIES", "functional", str),
    "tags":            ("tags", "TP_WORKER_TAGS", "", str),
    "max_concurrency": ("max_concurrency", "TP_WORKER_MAX_CONCURRENCY", 4, int),
    "tenant_id":       ("tenant_id", "TP_WORKER_TENANT_ID", 0, int),  # 0=共享
    "log_level":       ("log_level", "TP_WORKER_LOG_LEVEL", "INFO", str),
    "artifact_dir":    ("artifact_dir", "TP_ARTIFACT_DIR", ".data/artifacts", str),
    "egress_allow":    ("egress_allow", "TP_EGRESS_ALLOW", "", str),
    "egress_block_private": ("egress_block_private", "TP_EGRESS_BLOCK_PRIVATE", False, bool),
    "sandbox_cpu":     ("sandbox_cpu", "TP_SANDBOX_CPU", 30, int),
    "sandbox_mem_mb":  ("sandbox_mem_mb", "TP_SANDBOX_MEM_MB", 1024, int),
    "sandbox_nproc":   ("sandbox_nproc", "TP_SANDBOX_NPROC", 128, int),
    "sandbox_nofile":  ("sandbox_nofile", "TP_SANDBOX_NOFILE", 128, int),
    "sandbox_fsize_mb": ("sandbox_fsize_mb", "TP_SANDBOX_FSIZE_MB", 32, int),
    "sandbox_net":     ("sandbox_net", "TP_SANDBOX_NET", "deny", str),  # deny|allow
    "sandbox_require_isolation": ("sandbox_require_isolation", "TP_SANDBOX_REQUIRE_ISOLATION", False, bool),  # 1=无隔离工具时拒绝执行
    "otel_exporter":   ("otel_exporter", "TP_OTEL_EXPORTER", "", str),  # ""|stdout|otlp
    "otel_endpoint":   ("otel_endpoint", "TP_OTEL_ENDPOINT", "127.0.0.1:4317", str),
}


@dataclass(frozen=True)
class Settings:
    scheduler: str = "127.0.0.1:9090"
    token: str = ""  # gRPC 认证令牌（Scheduler 侧 worker_token；空=无令牌，注册会被拒）
    name: str = ""
    capabilities: str = "functional"
    tags: str = ""
    max_concurrency: int = 4
    tenant_id: int = 0
    log_level: str = "INFO"
    artifact_dir: str = ".data/artifacts"
    egress_allow: str = ""
    egress_block_private: bool = False
    sandbox_cpu: int = 30
    sandbox_mem_mb: int = 1024
    sandbox_nproc: int = 128
    sandbox_nofile: int = 128
    sandbox_fsize_mb: int = 32
    sandbox_net: str = "deny"
    sandbox_require_isolation: bool = False  # 1=无隔离工具（sandbox-exec/bwrap）时沙箱直接失败
    otel_exporter: str = ""
    otel_endpoint: str = "127.0.0.1:4317"


def _to_bool(v: object) -> bool:
    if isinstance(v, bool):
        return v
    return str(v).strip().lower() in ("1", "true", "yes", "on")


def _config_path(args: argparse.Namespace) -> str:
    if args.config:
        return args.config
    if os.environ.get("TP_WORKER_CONFIG"):
        return os.environ["TP_WORKER_CONFIG"]
    return "worker.yaml" if Path("worker.yaml").is_file() else ""


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(prog="testpilot-worker", description="TestPilot Worker")
    p.add_argument("--config", default="", help="YAML 配置路径（> TP_WORKER_CONFIG > ./worker.yaml）")
    for dest, (key, _env, _default, typ) in _FIELDS.items():
        flag = "--" + dest.replace("_", "-")
        # 默认 None 以区分「未传」与「显式传了默认值」（优先级判定依赖）
        if typ is bool:
            p.add_argument(flag, dest=dest, default=None, choices=["true", "false", "1", "0"],
                           help=f"{key}（true/false）")
        elif typ is int:
            p.add_argument(flag, dest=dest, type=int, default=None, help=key)
        else:
            p.add_argument(flag, dest=dest, default=None, help=key)
    return p


def resolve(args: argparse.Namespace, env: dict[str, str] | None = None) -> Settings:
    """合成最终配置：CLI(非 None) > env > YAML > 默认。"""
    env = os.environ if env is None else env
    ydoc: dict = {}
    path = _config_path(args)
    if path:
        raw = Path(path).read_text(encoding="utf-8")
        ydoc = yaml.safe_load(raw) or {}
        if not isinstance(ydoc, dict):
            raise SystemExit(f"config {path}: 顶层必须是 mapping")

    values: dict[str, object] = {}
    for dest, (key, env_key, default, typ) in _FIELDS.items():
        v: object = default
        if key in ydoc:
            v = ydoc[key]
        if env.get(env_key):
            v = env[env_key]
        cli = getattr(args, dest, None)
        if cli is not None:
            v = cli
        if typ is bool:
            v = _to_bool(v)
        elif typ is int:
            try:
                v = int(v)  # env/YAML 字符串数字归一
            except (TypeError, ValueError):
                raise SystemExit(f"config {key}: {v!r} 不是整数") from None
        values[dest] = v
    return Settings(**values)


def load(argv: list[str] | None = None, env: dict[str, str] | None = None) -> Settings:
    return resolve(build_parser().parse_args(argv), env)


def apply_environ(s: Settings) -> None:
    """把解析结果回写环境，供沙箱子进程/egress/tracing/ui 等按原约定读取。
    token 不回写：Worker 凭据不进进程环境（沙箱可读 /proc/<PPID>/environ）。"""
    for dest, (_key, env_key, _default, typ) in _FIELDS.items():
        if dest == "token":
            continue
        v = getattr(s, dest)
        os.environ[env_key] = ("1" if v else "0") if typ is bool else str(v)
