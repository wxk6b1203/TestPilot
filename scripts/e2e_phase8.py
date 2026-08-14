#!/usr/bin/env python3
"""Phase 8 端到端验证：RBAC / 跨租户隔离 / 配额 / OIDC / 定时+通知。

前置：dev.sh 全栈已启动（scheduler :8080 / worker / echo :18080）。
脚本会自起 mock OIDC IdP(:18100)，结束时清理。

用法：worker/.venv/bin/python scripts/e2e_phase8.py
"""

from __future__ import annotations

import json
import subprocess
import sys
import time

import httpx

BASE = "http://127.0.0.1:8080"
ECHO = "http://127.0.0.1:18080"

api = httpx.Client(base_url=BASE, timeout=15)


def login(u: str, p: str) -> str:
    r = api.post("/api/v1/auth/login", json={"username": u, "password": p})
    r.raise_for_status()
    return r.json()["token"]


def authed(token: str) -> httpx.Client:
    return httpx.Client(base_url=BASE, timeout=15,
                        headers={"Authorization": f"Bearer {token}"})


stamp = int(time.time())
token = login("admin", "admin123")
api.headers["Authorization"] = f"Bearer {token}"
print("✓ login")

# ---- RBAC：viewer 只读、member 可写、最后 owner 保护 ----
api.post("/api/v1/tenant/members", json={"username": f"p8viewer{stamp}", "password": "view12345", "role": 4})
api.post("/api/v1/tenant/members", json={"username": f"p8member{stamp}", "password": "memb12345", "role": 3})
viewer = authed(login(f"p8viewer{stamp}", "view12345"))
member = authed(login(f"p8member{stamp}", "memb12345"))

r = viewer.get("/api/v1/projects")
assert r.status_code == 200, r.text
r = viewer.post("/api/v1/projects", json={"name": "x"})
assert r.status_code == 403, f"viewer POST 应 403: {r.status_code}"
r = viewer.get("/api/v1/tenant/members")
assert r.status_code == 403, "viewer 不应能看成员列表"
r = member.post("/api/v1/projects", json={"name": f"p8-rbac-{stamp}"})
assert r.status_code == 200, f"member 应可建项目: {r.text}"
rbac_project = r.json()["id"]
r = member.get("/api/v1/tenant/members")
assert r.status_code == 403, "member 不应能看成员列表"
# 最后 owner 保护
me = api.get("/api/v1/me").json()
r = api.put(f"/api/v1/tenant/members/{me['user']['id']}", json={"role": 2})
assert r.status_code == 409 and "LAST_OWNER" in r.text, r.text
print("✓ RBAC: viewer 只读 / member 可写 / last-owner 保护")

# ---- 跨租户隔离 ----
r = api.post("/api/v1/tenants", json={"name": f"tenantB-{stamp}"})
tenant_b = r.json()["id"]
r = api.post("/api/v1/auth/switch-tenant", json={"tenant_id": tenant_b})
token_b = r.json()["token"]
tb = authed(token_b)
r = tb.post("/api/v1/projects", json={"name": f"p8-tenantB-{stamp}"})
proj_b = r.json()["id"]
# A 看不到 B 的项目；B 看不到 A 的
r = api.get(f"/api/v1/projects/{proj_b}")
assert r.status_code == 404, f"A 不应能读 B 的项目: {r.status_code}"
r = tb.get(f"/api/v1/projects/{rbac_project}")
assert r.status_code == 404, f"B 不应能读 A 的项目: {r.status_code}"
ids_a = [p["id"] for p in api.get("/api/v1/projects?page_size=100").json()["items"]]
assert proj_b not in ids_a
ids_b = [p["id"] for p in tb.get("/api/v1/projects?page_size=100").json()["items"]]
assert ids_b == [proj_b], ids_b
# B 的 viewer 越权：tenant B 的 token 调 A 的 run
runs_a = api.get("/api/v1/runs?page_size=1").json()["items"]
if runs_a:
    r = tb.get(f"/api/v1/runs/{runs_a[0]['id']}")
    assert r.status_code == 404, f"B 不应能读 A 的 run: {r.status_code}"
print("✓ 跨租户隔离: 项目/run 互不可见")

# ---- 配额 ----
quotas = {q["metric"]: q for q in api.get("/api/v1/tenant/quotas").json()["items"]}
used = quotas["monthly_runs"]["used"]
api.put("/api/v1/tenant/quotas/monthly_runs", json={"limit": used})  # 已满
# 建一个计划用于触发
r = api.post("/api/v1/environments", json={"project_id": rbac_project, "name": "local",
                                           "base_url": ECHO})
env_id = r.json()["id"]
r = api.post("/api/v1/cases", json={"project_id": rbac_project, "name": "c1", "type": 1,
                                    "definition": {"steps": [{"id": "1", "type": 1, "name": "g",
                                    "api_call": {"inline": {"method": 1, "uri": "/json"}}}]}})
case_id = r.json()["id"]
r = api.post("/api/v1/plans", json={"project_id": rbac_project, "env_id": env_id, "name": "p1",
                                    "items": [{"ref_type": 1, "ref_id": case_id, "enabled": True}]})
plan_id = r.json()["id"]
r = api.post(f"/api/v1/plans/{plan_id}/run")
assert r.status_code == 429 and "QUOTA_EXCEEDED" in r.text, f"应 429: {r.text}"
api.put("/api/v1/tenant/quotas/monthly_runs", json={"limit": 0})
r = api.post(f"/api/v1/plans/{plan_id}/run")
assert r.status_code == 200, f"解除限额后应可运行: {r.text}"
print(f"✓ 配额: monthly_runs 超限 429，解除恢复（used={used}）")

# ---- OIDC（mock IdP）----
idp = subprocess.Popen([sys.executable, "scripts/mock_oidc.py", "18100"],
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
try:
    time.sleep(0.8)
    r = api.post("/api/v1/identity-providers", json={
        "name": "mock-idp", "issuer": "http://127.0.0.1:18100",
        "client_id": "mock-client", "client_secret": "mock-secret"})
    prov_id = r.json()["id"]
    pub = httpx.get(f"{BASE}/api/v1/auth/oidc/providers").json()["items"]
    assert any(str(p["id"]) == str(prov_id) for p in pub), "providers 公开列表缺项"
    browser = httpx.Client(follow_redirects=True, timeout=15)
    r = browser.get(f"{BASE}/api/v1/auth/oidc/{prov_id}/login")
    data = r.json()
    assert data.get("token"), f"OIDC 未签发 token: {data}"
    oidc = authed(data["token"])
    r = oidc.get("/api/v1/me")
    assert r.status_code == 200 and r.json()["user"]["email"] == "mock@oidc.local", r.text
    r = oidc.post("/api/v1/projects", json={"name": "x"})
    assert r.status_code == 403, "OIDC 默认角色 viewer 应只读"
    print(f"✓ OIDC: mock IdP 登录 → {data['user']['username']}（viewer）token 可用")
finally:
    idp.terminate()

# ---- OAuth2 授权码（mock 提供方，无 discovery → 走 IdP config 显式端点）----
oa2 = subprocess.Popen([sys.executable, "scripts/mock_oauth2.py", "18110"],
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
try:
    time.sleep(0.8)
    r = api.post("/api/v1/identity-providers", json={
        "name": "mock-oauth2", "type": "oauth2", "issuer": "http://127.0.0.1:18110",
        "client_id": "mock-client", "client_secret": "mock-secret",
        "authorization_endpoint": "http://127.0.0.1:18110/authorize",
        "token_endpoint": "http://127.0.0.1:18110/token",
        "userinfo_endpoint": "http://127.0.0.1:18110/userinfo"})
    assert r.status_code == 200, r.text
    prov2 = r.json()
    assert prov2["type"] == "oauth2", prov2
    browser = httpx.Client(follow_redirects=True, timeout=15)
    r = browser.get(f"{BASE}/api/v1/auth/oidc/{prov2['id']}/login")
    data = r.json()
    assert data.get("token"), f"OAuth2 未签发 token: {data}"
    oauth2cli = authed(data["token"])
    r = oauth2cli.get("/api/v1/me")
    assert r.status_code == 200 and r.json()["user"]["email"] == "mock@oauth2.local", r.text
    r = oauth2cli.post("/api/v1/projects", json={"name": "x"})
    assert r.status_code == 403, "OAuth2 默认角色 viewer 应只读"
    print(f"✓ OAuth2: mock 提供方登录 → {data['user']['username']}（viewer，userinfo 身份）")
finally:
    oa2.terminate()

# ---- 定时调度 + 通知 ----
r = api.post("/api/v1/notifications", json={
    "name": "e2e-sink", "type": 1, "url": f"{ECHO}/sink",
    "events": "run_finished,stress_finished"})
chan_id = r.json()["id"]
r = api.post("/api/v1/schedules", json={
    "plan_id": plan_id, "env_id": env_id, "name": f"p8-cron-{stamp}",
    "cron_expr": "* * * * *", "overlap_policy": 1})
sched_id = r.json()["id"]

deadline = time.time() + 90
run_ok = None
while time.time() < deadline:
    time.sleep(5)
    runs = api.get(f"/api/v1/runs?page_size=10").json()["items"]
    for run in runs:
        if run["plan_id"] == plan_id and run["trigger"] == 2 and run["status"] == 2:
            run_ok = run
            break
    if run_ok:
        break
assert run_ok, "90s 内无成功的定时运行"
# 通知到达 sink
time.sleep(2)
sink = httpx.get(f"{ECHO}/sink/dump").json()["items"]
hit = [i for i in sink if isinstance(i.get("body"), dict)
       and i["body"].get("event") == "run_finished"
       and str(i["body"].get("plan_id")) == str(plan_id)]
assert hit, f"sink 无该计划的 run_finished: {sink}"
api.delete(f"/api/v1/schedules/{sched_id}")
api.delete(f"/api/v1/notifications/{chan_id}")
print(f"✓ 定时+通知: schedule 触发 run={run_ok['id']}（trigger=SCHEDULED），webhook 已送达")

print("\nPHASE 8 E2E PASSED")
