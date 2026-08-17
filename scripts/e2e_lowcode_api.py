#!/usr/bin/env python3
"""低代码按接口 ID 调用与自动封装端到端（docs/lowcode-api-invocation.md）。

前置：Scheduler(:8080) + Worker + echo(:18080) + grpc-echo(:19090) 已启动。
流程：建项目/环境/HTTP 接口/gRPC 接口 → 封装预览 → 低代码用例按 ID 运行 →
改接口 URI 后重跑自动生效 → gRPC by-id 运行。

用法：worker/venv/bin/python scripts/e2e_lowcode_api.py [--base http://127.0.0.1:8080]
"""

from __future__ import annotations

import argparse
import sys
import time

import httpx

p = argparse.ArgumentParser()
p.add_argument("--base", default="http://127.0.0.1:8080")
p.add_argument("--timeout", type=float, default=60)
args = p.parse_args()

api = httpx.Client(base_url=args.base, timeout=20)
r = api.post("/api/v1/auth/login", json={"username": "admin", "password": "admin123"})
r.raise_for_status()
api.headers["Authorization"] = f"Bearer {r.json()['token']}"
print("✓ login")

stamp = int(time.time())
proj = api.post("/api/v1/projects", json={"name": f"e2e-lc-api-{stamp}"}).json()
pid = proj["id"]
env = api.post("/api/v1/environments", json={
    "project_id": pid, "name": "http", "base_url": "http://127.0.0.1:18080"}).json()
genv = api.post("/api/v1/environments", json={
    "project_id": pid, "name": "grpc", "base_url": "127.0.0.1:19090"}).json()

hapi = api.post("/api/v1/apis", json={
    "project_id": pid, "name": "CreateUser", "method": 2, "uri": "/echo",
    "headers": [{"key": "X-Wrap", "value": "1"}]}).json()
hid = hapi["id"]
gapi = api.post("/api/v1/grpc-apis", json={
    "project_id": pid, "address": "127.0.0.1:19090",
    "full_service": "testpilot.echo.v1.EchoService", "method": "Echo",
    "request_message": {}}).json()
gid = gapi["id"]
print(f"✓ project={pid} http_api={hid} grpc_api={gid}")

# 封装预览：稳定类名 Api<ID> + 可读别名
prev = api.get(f"/api/v1/projects/{pid}/api-wrappers?http_ids={hid}&grpc_ids={gid}").json()
assert f"class Api{hid}(HttpAPI):" in prev["source"], prev
assert f"class Api{gid}(GrpcAPI):" in prev["source"], prev
assert 'method: str = "POST"' in prev["source"], prev
assert 'uri: str = "/echo"' in prev["source"], prev
assert "CreateUser = Api" + str(hid) in prev["source"], prev
stub = api.get(f"/api/v1/projects/{pid}/api-wrappers?http_ids={hid}&format=stub").json()
assert "IDE completion stub" in stub["source"] and "async def run" in stub["source"]
assert "testpilot_sdk" not in stub["source"]  # 自包含补全，不依赖 SDK 安装
print("✓ wrapper preview（含 .pyi 补全 stub）")

# 低代码用例：只写接口 ID（ctx.api 静态提取 + 显式 grpc ref 混合）
src = f'''from tp_api_wrappers import Api{hid}


async def run(ctx):
    a = await Api{hid}().run(body={{"n": 1}})
    assert a.status == 200 and a.body["body"]["n"] == 1
    b = await ctx.api("{hid}").run(body={{"n": 2}})
    assert b.body["body"]["n"] == 2
'''
case = api.post("/api/v1/cases", json={
    "project_id": pid, "name": "by-id", "type": 2,
    "definition": {"source": src, "entry": "run"}}).json()

# HTTP 环境计划验证 HTTP 部分；gRPC 部分用独立用例（base_url 不同）。
http_plan = api.post("/api/v1/plans", json={
    "project_id": pid, "env_id": env["id"], "name": "http-plan",
    "items": [{"ref_type": 1, "ref_id": case["id"], "enabled": True, "order": 1}]}).json()


def wait_run(rid: str) -> dict:
    for _ in range(int(args.timeout * 2)):
        rr = api.get(f"/api/v1/runs/{rid}").json()
        if rr["status"] in (2, 3, 4, 5):
            return rr
        time.sleep(0.5)
    return rr


rr = wait_run(api.post(f"/api/v1/plans/{http_plan['id']}/run").json()["run_id"])
assert rr["status"] == 2 and rr["cases"][0]["status"] == 2, rr
print("✓ http by-id run passed")

# 修改接口 URI → 同一脚本/计划零改动，下次运行自动用新定义
api.put(f"/api/v1/apis/{hid}", json={
    "project_id": pid, "name": "CreateUser", "method": 2, "uri": "/echo?updated=1",
    "headers": [{"key": "X-Wrap", "value": "2"}]}).raise_for_status()
rr = wait_run(api.post(f"/api/v1/plans/{http_plan['id']}/run").json()["run_id"])
assert rr["status"] == 2 and rr["cases"][0]["status"] == 2, rr
logs = [x for s in rr["cases"][0]["steps"] for x in (s.get("logs") or [])]
assert any("/echo?updated=1" in x for x in logs), logs
print("✓ api update auto-effective（脚本零改动）")

# gRPC by-id 独立用例（base_url 指向 grpc-echo）
grpc_src = f'''from tp_api_wrappers import Api{gid}


async def run(ctx):
    r = await Api{gid}().run(request={{"message": "hi", "repeat": 2}})
    assert r.json["message"] == "hihi"
    g = await ctx.grpc_api("{gid}").run(request={{"message": "x"}})
    assert g.json["message"] == "x"
'''
grpc_case = api.post("/api/v1/cases", json={
    "project_id": pid, "name": "grpc-by-id", "type": 2,
    "definition": {"source": grpc_src, "entry": "run", "grpcApiRefs": [str(gid)]}}).json()
grpc_plan = api.post("/api/v1/plans", json={
    "project_id": pid, "env_id": genv["id"], "name": "grpc-plan",
    "items": [{"ref_type": 1, "ref_id": grpc_case["id"], "enabled": True, "order": 1}]}).json()
rr = wait_run(api.post(f"/api/v1/plans/{grpc_plan['id']}/run").json()["run_id"])
assert rr["status"] == 2 and rr["cases"][0]["status"] == 2, rr
print("✓ grpc by-id run passed")

print("LOWCODE API E2E PASSED")
