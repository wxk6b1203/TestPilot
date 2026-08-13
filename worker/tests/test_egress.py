"""egress.py：SSRF 出口控制判定（allow 白名单 + 私网阻断）。

离线约定：字面 IP 走 getaddrinfo 不做 DNS；主机名解析一律 monkeypatch 打桩。
"""

import socket

import pytest

from testpilot_worker import egress
from testpilot_worker.egress import EgressDenied, check_url


@pytest.fixture
def policy(monkeypatch):
    """每个用例显式设置策略，结束后由 monkeypatch 还原。"""

    def set_policy(allow=(), block_private=False):
        monkeypatch.setattr(egress, "_ALLOW", list(allow))
        monkeypatch.setattr(egress, "_BLOCK_PRIVATE", block_private)

    return set_policy


def _gai_result(ip: str):
    return [(socket.AF_INET, socket.SOCK_STREAM, 6, "", (ip, 0))]


# ---- allow 白名单 ----

def test_empty_allow_permits_any(policy):
    policy(allow=())
    check_url("http://anything.example/")
    check_url("http://127.0.0.1:8080/")  # block_private 默认关


def test_allow_exact_match(policy):
    policy(allow=["api.example.com"])
    check_url("https://api.example.com/v1")
    with pytest.raises(EgressDenied, match="not in TP_EGRESS_ALLOW"):
        check_url("https://other.example.com/")


def test_allow_host_matching_is_case_insensitive(policy):
    policy(allow=["api.example.com"])
    check_url("https://API.Example.COM/v1")


def test_allow_dot_suffix_matches_subdomain_and_bare(policy):
    policy(allow=[".corp.internal"])
    check_url("http://svc.corp.internal/")
    check_url("http://deep.svc.corp.internal/")
    check_url("http://corp.internal/")  # 去掉点前缀的裸域也命中


def test_allow_dot_suffix_rejects_lookalike(policy):
    policy(allow=[".corp.internal"])
    with pytest.raises(EgressDenied):
        check_url("http://evilcorp.internal/")
    with pytest.raises(EgressDenied):
        check_url("http://corp.internal.evil.com/")


def test_no_host_in_url_denied(policy):
    policy(allow=())
    with pytest.raises(EgressDenied, match="no host in url"):
        check_url("not-a-url")


# ---- block_private：字面 IP（getaddrinfo 数字解析，无 DNS）----

@pytest.mark.parametrize("ip", [
    "10.0.0.1", "10.255.255.254",        # 10/8
    "172.16.0.1", "172.31.255.254",      # 172.16/12
    "192.168.0.1",                        # 192.168/16
    "127.0.0.1", "127.53.0.9",           # 127/8 环回
    "169.254.1.1",                        # 169.254/16 链路本地
])
def test_block_private_denies_private_ipv4(policy, ip):
    policy(block_private=True)
    with pytest.raises(EgressDenied, match="private/loopback"):
        check_url(f"http://{ip}/")


@pytest.mark.parametrize("ip", ["::1", "fd00::1"])  # 环回 / fc00::/7 ULA
def test_block_private_denies_private_ipv6(policy, ip):
    policy(block_private=True)
    with pytest.raises(EgressDenied, match="private/loopback"):
        check_url(f"http://[{ip}]/")


def test_block_private_allows_public_ip(policy):
    policy(block_private=True)
    check_url("http://8.8.8.8/")
    check_url("http://93.184.216.34/")


def test_block_private_off_allows_private_ip(policy):
    policy(block_private=False)
    check_url("http://127.0.0.1/")
    check_url("http://192.168.1.1/")


# ---- block_private：主机名解析打桩 ----

def test_hostname_resolving_private_denied(policy, monkeypatch):
    policy(block_private=True)
    monkeypatch.setattr(socket, "getaddrinfo",
                        lambda host, port: _gai_result("10.1.2.3"))
    with pytest.raises(EgressDenied, match="private/loopback"):
        check_url("http://internal.svc/")


def test_hostname_resolving_public_allowed(policy, monkeypatch):
    policy(block_private=True)
    monkeypatch.setattr(socket, "getaddrinfo",
                        lambda host, port: _gai_result("93.184.216.34"))
    check_url("http://public.example/")


def test_resolution_failure_not_blocked_by_private_check(policy, monkeypatch):
    """解析失败交由后续连接报错，判定本身放行。"""
    policy(block_private=True)

    def _boom(host, port):
        raise socket.gaierror("name or service not known")

    monkeypatch.setattr(socket, "getaddrinfo", _boom)
    check_url("http://unresolvable.invalid/")


# ---- allow 与 block_private 组合优先级 ----

def test_allow_passes_but_block_private_still_denies(policy, monkeypatch):
    """两道关卡串联：先 allow 后 block_private，白名单命中不能豁免私网。"""
    policy(allow=["internal.svc"], block_private=True)
    monkeypatch.setattr(socket, "getaddrinfo",
                        lambda host, port: _gai_result("192.168.10.5"))
    with pytest.raises(EgressDenied, match="private/loopback"):
        check_url("http://internal.svc/")


def test_allow_miss_denies_before_resolution(policy, monkeypatch):
    """allow 未命中直接拒，不应触发解析。"""
    policy(allow=["api.example.com"], block_private=True)

    def _boom(host, port):
        raise AssertionError("getaddrinfo should not be called")

    monkeypatch.setattr(socket, "getaddrinfo", _boom)
    with pytest.raises(EgressDenied, match="not in TP_EGRESS_ALLOW"):
        check_url("http://blocked.example/")
