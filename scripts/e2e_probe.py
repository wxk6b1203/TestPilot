#!/usr/bin/env python3
"""UI 探测 e2e（离线，无需 LLM key，设计：docs/ui-probe-design.md §4.10）。

链路：CopilotToolService(gRPC) → Scheduler probe.Hub → Worker 命令流 → Playwright。
前置：dev 栈运行中（scripts/dev.sh start；scheduler 已注入 TP_PROBE_ENABLED=1）。
运行：worker/venv/bin/python scripts/e2e_probe.py
"""

from __future__ import annotations

import sys
import time
from pathlib import Path

import grpc
import httpx

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "worker" / "src"))

from testpilot.common.v1 import types_pb2 as pb  # noqa: E402
from testpilot.copilot.v1 import copilot_pb2 as cpb  # noqa: E402
from testpilot.copilot.v1 import copilot_pb2_grpc as cpbg  # noqa: E402

BASE = "http://127.0.0.1:8080"
GRPC_ADDR = "127.0.0.1:9090"
FORM = "http://127.0.0.1:18080/form"  # echo 登录测试页（Sign in → Welcome, <user>!）

api = httpx.Client(base_url=BASE, timeout=15)
r = api.post("/api/v1/auth/login", json={"username": "admin", "password": "admin123"})
r.raise_for_status()
token = r.json()["token"]
api.headers["Authorization"] = f"Bearer {token}"
me = api.get("/api/v1/me").json()
tenant_id, user_id = int(me["tenant_id"]), str(me["user"]["id"])
print(f"✓ login tenant={tenant_id} user={user_id}")

stub = cpbg.CopilotToolServiceStub(grpc.insecure_channel(GRPC_ADDR))
MD = (("authorization", f"Bearer {token}"),)
SESSION = f"e2e-probe-{time.time_ns()}"


def ctx() -> pb.RequestContext:
    return pb.RequestContext(tenant_id=tenant_id, user_id=user_id,
                             actor="copilot", request_id=f"e2e-{time.time_ns()}")


# 1) open：建会话 + 导航 + ARIA 快照
r = stub.OpenProbe(cpb.OpenProbeRequest(ctx=ctx(), session_id=SESSION, url=FORM), metadata=MD)
assert "Sign in" in r.aria_snapshot, f"快照缺登录按钮：\n{r.aria_snapshot}"
assert not r.snapshot_truncated
print(f"✓ open+snapshot worker={r.worker_id} url={r.final_url} title={r.title!r}")

# 2) act：填账号 → 点登录（每步自动回快照）
r = stub.ActProbe(cpb.ActProbeRequest(
    ctx=ctx(), session_id=SESSION,
    action=pb.UiActionStep(action=pb.UI_ACTION_FILL, target="#username", value="neo")), metadata=MD)
r = stub.ActProbe(cpb.ActProbeRequest(
    ctx=ctx(), session_id=SESSION,
    action=pb.UiActionStep(action=pb.UI_ACTION_CLICK, target="#login-btn")), metadata=MD)
print("✓ act fill+click")

# 3) eval：断言登录反馈（页面上下文 JS）
r = stub.EvalProbe(cpb.EvalProbeRequest(
    ctx=ctx(), session_id=SESSION,
    expression="document.getElementById('result').textContent"), metadata=MD)
assert "Welcome, neo" in r.result_json, f"登录反馈不符：{r.result_json}"
print(f"✓ eval result={r.result_json}")

# 3.5) run_py：常驻沙箱执行 Python（枚举页面按钮 + helper 跨帧复用）
run1 = (
    "async def run(ctx):\n"
    "    print('hello-from-sandbox')\n"
    "    return await ctx.page.evaluate(\"[...document.querySelectorAll('button')].map(b => b.textContent.trim())\")\n"
)
r = stub.RunProbe(cpb.RunProbeRequest(ctx=ctx(), session_id=SESSION, source=run1), metadata=MD)
assert "Sign in" in r.repr, f"run_py 枚举失败：{r.repr}"
assert any("hello-from-sandbox" in ln for ln in r.logs), f"print 未随帧回传：{r.logs}"

# 频率闸：紧接第二次 run → ResourceExhausted/PROBE_LIMIT
try:
    stub.RunProbe(cpb.RunProbeRequest(ctx=ctx(), session_id=SESSION, source=run1), metadata=MD)
    raise AssertionError("immediate second run must be rate limited")
except grpc.RpcError as e:
    assert e.code() == grpc.StatusCode.RESOURCE_EXHAUSTED, e.code()
    assert "PROBE_LIMIT" in e.details(), e.details()

time.sleep(2.1)  # 每会话 run 频率闸 2s
run2 = (
    "helper_seen = 42\n"
    "async def run(ctx):\n"
    "    return helper_seen\n"
)
r = stub.RunProbe(cpb.RunProbeRequest(ctx=ctx(), session_id=SESSION, source=run2), metadata=MD)
assert r.repr == "42", f"namespace 持久化失败：{r.repr}"
print("✓ run_py enumerate + namespace persist")

# 4) snapshot：登录后页面仍可读
r = stub.GetProbeSnapshot(cpb.GetProbeSnapshotRequest(ctx=ctx(), session_id=SESSION), metadata=MD)
assert r.aria_snapshot
print("✓ snapshot after actions")

# 5) close：幂等释放
r = stub.CloseProbe(cpb.CloseProbeRequest(ctx=ctx(), session_id=SESSION), metadata=MD)
assert r.ok
print("✓ close")

# 6) 死会话：NOT_FOUND + PROBE_SESSION_NOT_FOUND 前缀
try:
    stub.ActProbe(cpb.ActProbeRequest(
        ctx=ctx(), session_id=SESSION,
        action=pb.UiActionStep(action=pb.UI_ACTION_CLICK, target="#login-btn")), metadata=MD)
    raise AssertionError("act on closed session must fail")
except grpc.RpcError as e:
    assert e.code() == grpc.StatusCode.NOT_FOUND, e.code()
    assert "PROBE_SESSION_NOT_FOUND" in e.details(), e.details()
print("✓ dead session → NOT_FOUND/PROBE_SESSION_NOT_FOUND")

print("\nPROBE E2E PASSED")
