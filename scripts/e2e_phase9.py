#!/usr/bin/env python3
"""Phase 9 端到端验证：Prometheus 指标 + 审计完善。

前置：dev.sh 全栈已启动（scheduler :8080 / worker / echo :18080）。
链路（OTel）验证见 docs/deployment.md（compose + Jaeger 冒烟）。

用法：worker/.venv/bin/python scripts/e2e_phase9.py
"""

from __future__ import annotations

import re
import time

import httpx

BASE = "http://127.0.0.1:8080"
ECHO = "http://127.0.0.1:18080"

api = httpx.Client(base_url=BASE, timeout=15)


def login(u: str, p: str) -> str:
    r = api.post("/api/v1/auth/login", json={"username": u, "password": p})
    r.raise_for_status()
    return r.json()["token"]


token = login("admin", "admin123")
api.headers["Authorization"] = f"Bearer {token}"
print("✓ login")

stamp = int(time.time())

# ---- /metrics 暴露 + HTTP 计数 ----
m1 = httpx.get(f"{BASE}/metrics").text
assert "testpilot_http_requests_total" in m1, "缺 HTTP 计数指标"
assert "testpilot_workers_online" in m1, "缺 worker gauge"
runs_before = 0
for line in m1.splitlines():
    if line.startswith("testpilot_runs_total"):
        runs_before += int(float(line.rsplit(" ", 1)[1]))

# ---- 触发运行 → runs_total 增加 + dispatch_total ----
r = api.post("/api/v1/projects", json={"name": f"p9-metrics-{stamp}"})
proj = r.json()["id"]
r = api.post("/api/v1/environments", json={"project_id": proj, "name": "local", "base_url": ECHO})
env = r.json()["id"]
r = api.post("/api/v1/cases", json={"project_id": proj, "name": "c1", "type": 1,
                                    "definition": {"steps": [{"id": "1", "type": 1, "name": "g",
                                    "api_call": {"inline": {"method": 1, "uri": "/json"}}}]}})
case = r.json()["id"]
r = api.post("/api/v1/plans", json={"project_id": proj, "env_id": env, "name": "p9",
                                    "items": [{"ref_type": 1, "ref_id": case, "enabled": True}]})
plan = r.json()["id"]
r = api.post(f"/api/v1/plans/{plan}/run")
assert r.status_code == 200, r.text
run_id = r.json()["run_id"]

deadline = time.time() + 60
while time.time() < deadline:
    time.sleep(2)
    m2 = httpx.get(f"{BASE}/metrics").text
    runs_after = sum(int(float(l.rsplit(" ", 1)[1])) for l in m2.splitlines()
                     if l.startswith("testpilot_runs_total"))
    if runs_after > runs_before:
        break
assert runs_after > runs_before, "runs_total 未随运行增加"
assert 'testpilot_dispatch_total{result="ok"}' in m2
assert re.search(r'testpilot_runs_total\{status="passed",trigger="manual"\} [1-9]', m2), m2[-500:]
print(f"✓ 指标: runs_total +{runs_after - runs_before}，dispatch/worker/http 指标齐全")

# ---- 审计：人工变更（上面的 POST 已被中间件记录）----
r = api.get("/api/v1/audit-logs?page_size=50")
rows = [i for i in r.json()["items"] if i["actor"] == 1]
creates = [i for i in rows if i["action"] == "create" and i["resource_type"] == "projects"
           and "/projects" in str(i.get("detail") or "")]
assert creates, "缺人工 create projects 审计"
runs_audit = [i for i in rows if i["action"] == "run" and i["resource_id"] == str(plan)]
assert runs_audit, "缺 run 触发审计"
print("✓ 审计: 人工变更（create/run）已落")

# ---- 审计：敏感变量读取 ----
api.post("/api/v1/variables", json={"project_id": proj, "key": f"sec{stamp}",
                                    "value": "s3cret", "sensitive": True})
r = api.get(f"/api/v1/variables?project_id={proj}")
assert r.status_code == 200
time.sleep(0.5)
r = api.get("/api/v1/audit-logs?page_size=20")
secret_reads = [i for i in r.json()["items"]
                if i["action"] == "secret_read" and i["actor"] == 1]
assert secret_reads, "缺 secret_read 审计"
print("✓ 审计: 敏感变量读取已落")

# ---- 审计：租户切换（落在目标租户）----
r = api.post("/api/v1/tenants", json={"name": f"p9-audit-{stamp}"})
tb = r.json()["id"]
r = api.post("/api/v1/auth/switch-tenant", json={"tenant_id": tb})
assert r.status_code == 200, r.text
tb_token = r.json()["token"]
r = httpx.get(f"{BASE}/api/v1/audit-logs?page_size=10",
              headers={"Authorization": f"Bearer {tb_token}"})
switches = [i for i in r.json()["items"] if i["action"] == "switch_tenant"
            and i["resource_id"] == str(tb)]
assert switches, "目标租户缺 switch_tenant 审计"
print("✓ 审计: 租户切换已落（目标租户可见）")

print("\nPHASE 9 E2E PASSED")
