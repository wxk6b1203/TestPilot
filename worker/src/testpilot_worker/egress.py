"""SSRF 出口控制：Worker 出站 HTTP 的 host 白名单 / 私网阻断。

环境变量：
  TP_EGRESS_ALLOW          逗号分隔 host 白名单（精确或后缀匹配，空=不限制）
                           例: "api.example.com,.corp.internal"
  TP_EGRESS_BLOCK_PRIVATE  "1" 时解析目标 host，私网/环回/链路本地地址一律拒绝
                           （dev 默认关：echo 测试服务就是 127.0.0.1）

配置读取是惰性的（每次调用重读 env）：config.apply_environ 在模块 import 之后执行，
模块级常量会导致 CLI/YAML 配置静默失效（"以为开了实际没开"）。

声明式引擎（http_exec.execute）与能力桥（sandbox.bridge_http_handler）共用：
async 上下文请用 acheck_url（DNS 解析走事件循环 executor + 3s 超时，不冻结事件循环）；
check_url 保留同步版本（测试/非事件循环上下文）。
"""

from __future__ import annotations

import asyncio
import ipaddress
import os
import socket
from urllib.parse import urlparse

from httpcore._backends.auto import AutoBackend

# 模块级（测试 monkeypatch 目标；运行时以 env 为准——见 _effective_*）
_ALLOW: list[str] = []
_BLOCK_PRIVATE = False


class EgressDenied(Exception):
    """目标地址被出口策略拒绝。"""


def _effective_allow() -> list[str]:
    raw = os.environ.get("TP_EGRESS_ALLOW", "")
    if raw.strip():
        return [h.strip().lower() for h in raw.split(",") if h.strip()]
    return _ALLOW


def _effective_block_private() -> bool:
    raw = os.environ.get("TP_EGRESS_BLOCK_PRIVATE", "")
    if raw:
        return raw == "1"
    return _BLOCK_PRIVATE


def _allowed(host: str) -> bool:
    allow = _effective_allow()
    if not allow:
        return True
    for pat in allow:
        if pat.startswith("."):
            if host.endswith(pat) or host == pat[1:]:
                return True
        elif host == pat:
            return True
    return False


def _any_private(infos) -> bool:
    for info in infos:
        ip = ipaddress.ip_address(info[4][0])
        if ip.is_private or ip.is_loopback or ip.is_link_local or ip.is_reserved or ip.is_multicast:
            return True
    return False


def _is_private_sync(host: str) -> bool:
    try:
        infos = socket.getaddrinfo(host, None)
    except socket.gaierror:
        return False  # 解析失败交给后续连接报错
    return _any_private(infos)


async def _is_private_async(host: str) -> bool:
    """事件循环 executor 内解析（不冻结事件循环），3s 超时。"""
    loop = asyncio.get_running_loop()
    try:
        infos = await asyncio.wait_for(loop.getaddrinfo(host, None), timeout=3.0)
    except (socket.gaierror, asyncio.TimeoutError):
        return False
    return _any_private(infos)


def _host_of(url: str) -> str:
    host = (urlparse(url).hostname or "").lower()
    if not host:
        raise EgressDenied(f"egress: no host in url {url!r}")
    return host


def check_url(url: str) -> None:
    """同步校验出站 URL（测试/非事件循环上下文）；async 场景用 acheck_url。"""
    host = _host_of(url)
    if not _allowed(host):
        raise EgressDenied(f"egress: host {host!r} not in TP_EGRESS_ALLOW")
    if _effective_block_private() and _is_private_sync(host):
        raise EgressDenied(f"egress: host {host!r} resolves to private/loopback address")


async def acheck_url(url: str) -> None:
    """异步校验出站 URL：DNS 解析经事件循环 executor（非阻塞 + 3s 超时）。"""
    host = _host_of(url)
    if not _allowed(host):
        raise EgressDenied(f"egress: host {host!r} not in TP_EGRESS_ALLOW")
    if _effective_block_private() and await _is_private_async(host):
        raise EgressDenied(f"egress: host {host!r} resolves to private/loopback address")


def _private_ip(raw: str) -> bool:
    try:
        ip = ipaddress.ip_address(raw)
    except ValueError:
        return True  # 解析出非 IP 视为不可信
    return ip.is_private or ip.is_loopback or ip.is_link_local or ip.is_reserved or ip.is_multicast


async def resolve_host_for_connect(host: str) -> str | None:
    """连接前解析 host 并挑一个通过出口策略的 IP。

    返回 None 表示当前无白名单/私网阻断策略，调用方可按原始 host 连接（保留
    happy-eyeballs/多 A 记录回退）。有策略时返回首个允许 IP——httpcore 连接层
    用该 IP 建连，TLS SNI/Host 仍为原始 host，消除 check_url 与 connect 两次
    DNS 解析之间的 rebinding 窗口（TOCTOU）。
    """
    if not _effective_allow() and not _effective_block_private():
        return None
    if not _allowed(host):
        raise EgressDenied(f"egress: host {host!r} not in TP_EGRESS_ALLOW")
    loop = asyncio.get_running_loop()
    try:
        infos = await asyncio.wait_for(loop.getaddrinfo(host, None), timeout=3.0)
    except (socket.gaierror, asyncio.TimeoutError):
        return None  # 解析失败交给连接阶段报错
    if not infos:
        return None
    if _effective_block_private():
        allowed = [i for i in infos if not _private_ip(i[4][0])]
        if not allowed:
            raise EgressDenied(f"egress: host {host!r} resolves only to private/loopback address")
        infos = allowed
    return infos[0][4][0]


class EgressPinnedBackend(AutoBackend):
    """httpcore 网络后端：连接前解析并绑定允许的 IP（见 resolve_host_for_connect）。"""

    async def connect_tcp(
        self,
        host: str,
        port: int,
        timeout: float | None = None,
        local_address: str | None = None,
        socket_options=None,
    ):
        await self._init_backend()
        target = await resolve_host_for_connect(host)
        return await self._backend.connect_tcp(
            target or host, port,
            timeout=timeout, local_address=local_address, socket_options=socket_options,
        )
