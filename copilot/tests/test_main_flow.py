"""main.py 流式认证窗口 + deadline 代理 + 敏感头脱敏（P0/P1/P2 回归）。"""

import asyncio


def test_attach_auth_stream_token_visible_during_iteration():
    """回归 P0：agent 在流消费时才执行——attach_auth_stream 必须在迭代窗口内
    set token；handler 内 set/reset 对惰性 StreamingResponse 完全无效。"""
    from testpilot_copilot.main import attach_auth_stream
    from testpilot_copilot.scheduler_client import auth_token

    seen = []

    async def inner():
        seen.append(auth_token.get())
        yield b"chunk"

    class FakeResp:
        def __init__(self):
            self.body_iterator = inner()

    resp = FakeResp()
    attach_auth_stream(resp, "jwt-xyz")
    # 迭代前（handler 已返回）token 必须是空
    assert auth_token.get() == ""

    async def consume():
        async for _ in resp.body_iterator:
            pass
        # finally 已 reset
        assert auth_token.get() == ""

    asyncio.run(consume())
    assert seen == ["jwt-xyz"], f"token not visible in stream: {seen}"


def test_attach_auth_stream_non_stream_noop():
    from testpilot_copilot.main import attach_auth_stream
    class NonStream:
        pass
    r = NonStream()
    attach_auth_stream(r, "t")  # 不应抛错


async def _async_ret(cd, r):
    return cd


def test_auth_interceptor_overrides_existing_authorization():
    from testpilot_copilot.scheduler_client import AuthInterceptor, auth_token
    inter = AuthInterceptor()

    class CD:
        def __init__(self):
            self.metadata = [("authorization", "Bearer junk"), ("x-extra", "1")]

        def _replace(self, **kw):
            return kw.get("metadata")

    async def run():
        tok = auth_token.set("jwt-A")
        try:
            out = await inter.intercept_unary_unary(_async_ret, CD(), None)
            md = dict(out)
            assert md["authorization"] == "Bearer jwt-A", md
            assert md["x-extra"] == "1", md
            assert len(md) == 2, md
        finally:
            auth_token.reset(tok)

    asyncio.run(run())


def test_auth_interceptor_no_token_noop():
    from testpilot_copilot.scheduler_client import AuthInterceptor, auth_token
    inter = AuthInterceptor()

    class CD:
        def __init__(self):
            self.metadata = [("authorization", "Bearer junk")]

        def _replace(self, **kw):
            return kw.get("metadata")

    async def run():
        # 未设置 token：透传且不改 metadata
        cd = CD()
        out = await inter.intercept_unary_unary(_async_ret, cd, None)
        assert out is cd
        assert cd.metadata == [("authorization", "Bearer junk")]

    asyncio.run(run())


def test_deadline_proxy_applies_default():
    from testpilot_copilot.scheduler_client import _DeadlineProxy

    class Stub:
        async def ListProjects(self, req, timeout=None):
            return timeout

    p = _DeadlineProxy(Stub())
    assert asyncio.run(p.ListProjects(None)) == 30.0
    assert asyncio.run(p.ListProjects(None, timeout=5)) == 5


def test_redact_sensitive_headers():
    from testpilot_copilot.tools import _redact_headers
    hs = [
        {"key": "Authorization", "value": "Bearer secret"},
        {"key": "x-api-key", "value": "k123"},
        {"key": "X-User", "value": "bob"},
    ]
    _redact_headers(hs)
    assert hs[0]["value"] == "***"
    assert hs[1]["value"] == "***"
    assert hs[2]["value"] == "bob"



def test_context_id_header_accepts_numeric_ids():
    from testpilot_copilot.main import _context_id_header

    assert _context_id_header("123") == "123"
    assert _context_id_header(" 456 ") == "456"
    assert _context_id_header("") == ""
    assert _context_id_header(None) == ""


def test_context_id_header_rejects_non_numeric_or_oversized():
    from testpilot_copilot.main import _context_id_header

    assert _context_id_header("abc") == ""
    assert _context_id_header("12; drop") == ""
    assert _context_id_header("9" * 33) == ""

