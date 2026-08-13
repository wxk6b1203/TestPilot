"""TestPilot Worker 入口：解析参数，启动 gRPC 客户端。"""

from __future__ import annotations

import argparse
import asyncio
import logging
import sys

from testpilot.common.v1 import types_pb2 as pb

from .client import WorkerClient

_CAP_MAP = {
    "functional": pb.CAPABILITY_FUNCTIONAL,
    "lowcode": pb.CAPABILITY_LOWCODE,
    "playwright": pb.CAPABILITY_PLAYWRIGHT,
    "stress": pb.CAPABILITY_STRESS,
}


def _parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    p = argparse.ArgumentParser(prog="testpilot-worker", description="TestPilot Worker")
    p.add_argument("--scheduler", default="127.0.0.1:9090", help="scheduler gRPC 地址")
    p.add_argument("--capabilities", default="functional",
                   help="逗号分隔: functional,lowcode,playwright,stress")
    p.add_argument("--tags", default="", help="逗号分隔标签，如 region=cn,env=dev")
    p.add_argument("--max-concurrency", type=int, default=4)
    p.add_argument("--tenant-id", type=int, default=0, help="独占租户 ID；0=共享")
    p.add_argument("--log-level", default="INFO")
    return p.parse_args(argv)


def entry(argv: list[str] | None = None):
    args = _parse_args(argv)
    logging.basicConfig(
        level=args.log_level.upper(),
        format="%(asctime)s %(levelname)s %(name)s [%(trace_id)s] %(message)s",
        stream=sys.stdout,
    )
    from . import tracing

    tracing.init()  # TP_OTEL_EXPORTER 控制；默认关闭
    tracing.attach_log_filter()  # basicConfig 之后挂到 root handlers
    caps = []
    for name in args.capabilities.split(","):
        name = name.strip()
        if not name:
            continue
        if name not in _CAP_MAP:
            raise SystemExit(f"unknown capability: {name} (valid: {','.join(_CAP_MAP)})")
        caps.append(_CAP_MAP[name])
    tags = [t.strip() for t in args.tags.split(",") if t.strip()]
    client = WorkerClient(
        addr=args.scheduler,
        tags=tags,
        max_concurrency=args.max_concurrency,
        capabilities=caps,
        tenant_id=args.tenant_id,
    )
    try:
        asyncio.run(client.run())
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    entry()
