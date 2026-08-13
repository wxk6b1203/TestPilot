"""SSRF 出口控制：Worker 出站 HTTP 的 host 白名单 / 私网阻断。

环境变量：
  TP_EGRESS_ALLOW          逗号分隔 host 白名单（精确或后缀匹配，空=不限制）
                           例: "api.example.com,.corp.internal"
  TP_EGRESS_BLOCK_PRIVATE  "1" 时解析目标 host，私网/环回/链路本地地址一律拒绝
                           （dev 默认关：echo 测试服务就是 127.0.0.1）

声明式引擎（http_exec.execute）与能力桥（sandbox.bridge_http_handler）共用。
"""

from __future__ import annotations

import ipaddress
import os
import socket
from urllib.parse import urlparse

_ALLOW = [h.strip().lower() for h in os.environ.get("TP_EGRESS_ALLOW", "").split(",") if h.strip()]
_BLOCK_PRIVATE = os.environ.get("TP_EGRESS_BLOCK_PRIVATE", "") == "1"


class EgressDenied(Exception):
    """目标地址被出口策略拒绝。"""


def _allowed(host: str) -> bool:
    if not _ALLOW:
        return True
    for pat in _ALLOW:
        if pat.startswith("."):
            if host.endswith(pat) or host == pat[1:]:
                return True
        elif host == pat:
            return True
    return False


def _is_private(host: str) -> bool:
    try:
        infos = socket.getaddrinfo(host, None)
    except socket.gaierror:
        return False  # 解析失败交给后续连接报错
    for info in infos:
        ip = ipaddress.ip_address(info[4][0])
        if ip.is_private or ip.is_loopback or ip.is_link_local or ip.is_reserved or ip.is_multicast:
            return True
    return False


def check_url(url: str) -> None:
    """校验出站 URL；拒绝时抛 EgressDenied。"""
    host = (urlparse(url).hostname or "").lower()
    if not host:
        raise EgressDenied(f"egress: no host in url {url!r}")
    if not _allowed(host):
        raise EgressDenied(f"egress: host {host!r} not in TP_EGRESS_ALLOW")
    if _BLOCK_PRIVATE and _is_private(host):
        raise EgressDenied(f"egress: host {host!r} resolves to private/loopback address")
