"""TestPilot Worker 入口：三级配置（CLI > env > YAML）解析，启动 gRPC 客户端。"""

from __future__ import annotations

import asyncio
import logging
import os
import re
import signal
import sys

from testpilot.common.v1 import types_pb2 as pb

from . import config
from .client import WorkerClient

# 敏感环境变量匹配：TOKEN/KEY/SECRET/PASSWORD 等（用于进程环境清理）
_SENSITIVE_ENV_RE: re.Pattern | None = re.compile(r"(TOKEN|KEY|SECRET|PASSWORD|PASSWD)", re.I)

_CAP_MAP = {
    "functional": pb.CAPABILITY_FUNCTIONAL,
    "lowcode": pb.CAPABILITY_LOWCODE,
    "playwright": pb.CAPABILITY_PLAYWRIGHT,
    "stress": pb.CAPABILITY_STRESS,
}


def entry(argv: list[str] | None = None):
    s = config.load(argv)
    config.apply_environ(s)  # 回写 env：沙箱子进程/egress/tracing/ui 沿用原约定
    log_format = "%(asctime)s %(levelname)s %(name)s [%(trace_id)s] %(message)s"
    logging.basicConfig(
        level=s.log_level.upper(),
        format=log_format,
        stream=sys.stdout,
    )
    # trace_id 是运行时注入字段：给 formatter 提供默认值，避免任何使用该格式的
    # handler（含后续库新增/复制的 handler）在无 span 时抛 KeyError。
    for _h in logging.getLogger().handlers:
        _h.setFormatter(logging.Formatter(log_format, defaults={"trace_id": "", "span_id": ""}))
    from . import tracing

    tracing.init()  # otel_exporter（env 已回写）控制；默认关闭
    tracing.attach_log_filter()  # basicConfig 之后挂到 root handlers
    caps = []
    for name in s.capabilities.split(","):
        name = name.strip()
        if not name:
            continue
        if name not in _CAP_MAP:
            raise SystemExit(f"unknown capability: {name} (valid: {','.join(_CAP_MAP)})")
        caps.append(_CAP_MAP[name])
    tags = [t.strip() for t in s.tags.split(",") if t.strip()]
    client = WorkerClient(
        addr=s.scheduler,
        tags=tags,
        max_concurrency=s.max_concurrency,
        capabilities=caps,
        tenant_id=s.tenant_id,
        name=s.name,
        token=s.token,
    )
    # 沙箱缓解（B1）：敏感 env 已入内存后从进程环境移除——Linux 下沙箱可读
    # /proc/<PPID>/environ 拿到 Worker 全部凭据，env scrub 形同虚设。
    # 除 token 外，运维可能注入其它 *_TOKEN/*_KEY/口令类变量，一并清理。
    for _k in [k for k in os.environ if _SENSITIVE_ENV_RE.search(k)]:
        os.environ.pop(_k, None)
    # 优雅停机：SIGTERM/SIGINT → 停止取任务、取消在途任务、关闭连接（防孤儿进程/沙箱泄漏）
    loop = asyncio.new_event_loop()
    asyncio.set_event_loop(loop)
    for sig in (signal.SIGTERM, signal.SIGINT):
        try:
            loop.add_signal_handler(sig, client.request_stop)
        except (NotImplementedError, RuntimeError):  # 非主线程/平台不支持
            pass
    try:
        loop.run_until_complete(client.run())
    except KeyboardInterrupt:
        client.request_stop()
    finally:
        try:
            loop.run_until_complete(asyncio.sleep(0.1))  # 给在途收尾一点时间
        except Exception:
            pass
        loop.close()


if __name__ == "__main__":
    entry()
