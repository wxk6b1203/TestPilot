#!/usr/bin/env python3
"""TestPilot MVP 端到端联调脚本。

前置：Scheduler(:8080) + Worker(:9090 连接) + echo server(:18080) 已启动。
流程：登录 → 建项目/环境/变量/两个用例/计划 → 触发运行 → 轮询 → 校验闭环。

用法：uv run --project worker python scripts/e2e.py [--base http://127.0.0.1:8080]
"""

from __future__ import annotations

import argparse
import json
import sys
import time

import httpx

p = argparse.ArgumentParser()
p.add_argument("--base", default="http://127.0.0.1:8080")
p.add_argument("--timeout", type=float, default=60)
args = p.parse_args()

api = httpx.Client(base_url=args.base, timeout=15)

# ---- 登录 ----
r = api.post("/api/v1/auth/login", json={"username": "admin", "password": "admin123"})
r.raise_for_status()
token = r.json()["token"]
api.headers["Authorization"] = f"Bearer {token}"
print("✓ login")

stamp = int(time.time())

# ---- v2：公开注册（配置开关 registration_enabled；关闭时 403 优雅跳过）----
reg_user = f"e2e-reg-{stamp}"
reg = api.post("/api/v1/auth/register", json={
    "username": reg_user, "password": "register-pass-1",
    "display_name": "e2e registrant", "tenant_name": f"e2e-tenant-{stamp}"})
if reg.status_code == 403:
    print("— registration disabled by config, register flow skipped —")
elif reg.status_code == 200:
    regd = reg.json()
    assert regd["token"] and regd["role"] == 1, regd
    # 独立会话验证新账号登录 + 自建租户可见
    c2 = httpx.Client(base_url=args.base, timeout=15)
    r2 = c2.post("/api/v1/auth/login", json={"username": reg_user, "password": "register-pass-1"})
    assert r2.status_code == 200, r2.text
    c2.headers["Authorization"] = f"Bearer {r2.json()['token']}"
    me = c2.get("/api/v1/me")
    assert me.status_code == 200 and me.json()["user"]["username"] == reg_user, me.text
    print(f"✓ register+login ok（新租户 {me.json()['tenant_id']}，owner）")
else:
    print(f"✗ register unexpected {reg.status_code} {reg.text}")
    sys.exit(1)

# ---- 项目 / 环境 / 变量 ----
proj = api.post("/api/v1/projects", json={"name": f"e2e-{stamp}", "description": "e2e"}).json()
pid = proj["id"]
env = api.post("/api/v1/environments", json={
    "project_id": pid, "name": "local", "base_url": "http://127.0.0.1:18080"}).json()
eid = env["id"]
api.post("/api/v1/variables", json={
    "project_id": pid, "environment_id": eid, "key": "greeting", "value": "hello",
    "scope": 1, "category": 1}).raise_for_status()
api.post("/api/v1/variables", json={
    "project_id": pid, "environment_id": eid, "key": "token", "value": "t-123",
    "scope": 1, "category": 1}).raise_for_status()
print(f"✓ project={pid} env={eid}")

# ---- 用例 1：全绿（模板/断言/提取/IF/LOOP/RETRY/DELAY 全覆盖）----
case1_def = {"steps": [
    {"id": "1", "type": 1, "name": "GET echo with template",
     "api_call": {"inline": {"method": 1, "uri": "/echo?x={{greeting}}",
                             "headers": [{"key": "X-Token", "value": "{{token}}"}]}}},
    {"id": "2", "type": 3, "name": "assert echo",
     "assertion": {"assertions": [
         {"target": 1, "op": 1, "expected": "200"},
         {"target": 4, "path": "$.echo", "op": 1, "expected": "true"},
         {"target": 4, "path": "$.query.x", "op": 1, "expected": "hello"},
         {"target": 2, "path": "content-type", "op": 5, "expected": "application/json"},
     ]}},
    {"id": "3", "type": 1, "name": "GET json",
     "api_call": {"inline": {"method": 1, "uri": "/json"}}},
    {"id": "4", "type": 3, "name": "assert json",
     "assertion": {"assertions": [
         {"target": 4, "path": "$.user.name", "op": 1, "expected": "neo"},
         {"target": 4, "path": "$.items[1].price", "op": 7, "expected": "15"},
         {"target": 4, "path": "$.user.roles", "op": 5, "expected": "admin"},
     ]}},
    {"id": "5", "type": 4, "name": "extract id",
     "set_var": {"key": "user_id", "value_expr": "response.json.id"}},
    {"id": "6", "type": 5, "name": "if extracted",
     "if_step": {"condition_expr": "user_id == 42", "then_steps": [
         {"id": "1", "type": 1, "name": "reuse var",
          "api_call": {"inline": {"method": 1, "uri": "/echo?extracted={{user_id}}"}}},
         {"id": "2", "type": 3, "name": "assert reuse",
          "assertion": {"assertions": [
              {"target": 4, "path": "$.query.extracted", "op": 1, "expected": "42"}]}},
     ], "else_steps": []}},
    {"id": "7", "type": 6, "name": "loop twice",
     "loop_step": {"iterator": "idx", "count": 2, "body_steps": [
         {"id": "1", "type": 1, "name": "iter call",
          "api_call": {"inline": {"method": 1, "uri": "/echo?i={{idx}}"}}},
         {"id": "2", "type": 3, "name": "iter assert",
          "assertion": {"assertions": [{"target": 1, "op": 1, "expected": "200"}]}},
     ]}},
    {"id": "8", "type": 7, "name": "retry wrapper",
     "retry_step": {"max_attempts": 2, "backoff": "0.050s",
                    "body_step": {"id": "1", "type": 1, "name": "flaky-ish",
                                  "api_call": {"inline": {"method": 1, "uri": "/status/200"}}}}},
    {"id": "9", "type": 9, "name": "delay", "delay": {"duration": "0.100s"}},
]}
case1 = api.post("/api/v1/cases", json={
    "project_id": pid, "name": "e2e-full", "type": 1, "definition": case1_def}).json()
print(f"✓ case1={case1['id']}")

# ---- 用例 2：故意失败（验证 FAILED 路径与断言详情落库）----
case2_def = {"steps": [
    {"id": "1", "type": 1, "name": "GET json",
     "api_call": {"inline": {"method": 1, "uri": "/json"}}},
    {"id": "2", "type": 3, "name": "assert wrong status",
     "assertion": {"assertions": [{"target": 1, "op": 1, "expected": "500"}]}},
]}
case2 = api.post("/api/v1/cases", json={
    "project_id": pid, "name": "e2e-fail", "type": 1, "definition": case2_def}).json()
print(f"✓ case2={case2['id']}")

# ---- 用例 3：低代码（沙箱 + 能力桥）----
case3_src = '''from testpilot_sdk import Context, assert_that


async def run(ctx: Context):
    ctx.log("lowcode via capability bridge")
    resp = await ctx.http("GET", "/json")
    assert_that(resp.status, "status").eq(200)
    assert_that(resp.body["user"]["name"], "user.name").eq("neo")
    assert_that(resp.body["items"][1]["price"], "price").gt(15)
    await ctx.set_var("lc_uid", resp.body["id"])
'''
case3 = api.post("/api/v1/cases", json={
    "project_id": pid, "name": "e2e-lowcode", "type": 2,
    "definition": {"source": case3_src, "entry": "run"}}).json()
print(f"✓ case3={case3['id']} (lowcode)")

# ---- 用例 4：UI（Playwright 声明式 UI_ACTION）----
case4_def = {"steps": [
    {"id": "1", "type": 10, "name": "open form",
     "ui_action": {"action": 1, "target": "/form"}},
    {"id": "2", "type": 10, "name": "fill username",
     "ui_action": {"action": 3, "target": "#username", "value": "neo"}},
    {"id": "3", "type": 10, "name": "fill password",
     "ui_action": {"action": 3, "target": "#password", "value": "s3cret"}},
    {"id": "4", "type": 10, "name": "check remember",
     "ui_action": {"action": 5, "target": "#remember"}},
    {"id": "5", "type": 10, "name": "click sign in",
     "ui_action": {"action": 2, "target": "#login-btn"}},
    {"id": "6", "type": 10, "name": "expect welcome",
     "ui_action": {"action": 8, "target": "#result", "value": "Welcome, neo!"}},
    {"id": "7", "type": 10, "name": "full screenshot",
     "ui_action": {"action": 10, "value": "full"}},
]}
case4 = api.post("/api/v1/cases", json={
    "project_id": pid, "name": "e2e-ui", "type": 1, "definition": case4_def}).json()
print(f"✓ case4={case4['id']} (ui)")

# ---- v2：suite 引用展开（ref_type=2）----
suite1 = api.post("/api/v1/suites", json={
    "project_id": pid, "name": "e2e-suite",
    "case_ids": [case1["id"], case3["id"]]}).json()
got = api.get(f"/api/v1/suites/{suite1['id']}").json()
assert [str(x) for x in got["case_ids"]] == [str(case1["id"]), str(case3["id"])], \
    f"suite case_ids roundtrip mismatch: {got['case_ids']}"
print(f"✓ suite1={suite1['id']} case_ids={got['case_ids']}")

# ---- v2：lowcode script_ref（脚本资产库 + 派发前内联）----
script1 = api.post("/api/v1/scripts", json={
    "project_id": pid, "name": "lc-flow", "language": "python", "content": case3_src}).json()
case5 = api.post("/api/v1/cases", json={
    "project_id": pid, "name": "e2e-script-ref", "type": 2,
    "definition": {"script_ref": str(script1["id"]), "entry": "run"}}).json()
print(f"✓ script1={script1['id']} case5={case5['id']} (script_ref)")

# ---- v2：低代码 Page 模型（沙箱内经能力桥驱动浏览器）----
page_src = '''from testpilot_sdk import Context, assert_that


async def run(ctx: Context):
    page = ctx.page
    await page.goto("/form")
    await page.fill("#username", "neo")
    await page.fill("#password", "s3cret")
    await page.click("#login-btn")
    await page.expect_text("#result", "Welcome, neo!")
    await page.screenshot()
'''
case9 = api.post("/api/v1/cases", json={
    "project_id": pid, "name": "e2e-page", "type": 2,
    "definition": {"source": page_src, "entry": "run"}}).json()
print(f"✓ case9={case9['id']} (lowcode Page via capability bridge)")

# ---- v2：api_id 派发期解析（api_call 引用 + override 合并）----
api_echo = api.post("/api/v1/apis", json={
    "project_id": pid, "method": 1, "uri": "/echo",
    "params": [{"key": "page", "value": ""}]}).json()
case6 = api.post("/api/v1/cases", json={
    "project_id": pid, "name": "e2e-api-ref", "type": 1,
    "definition": {"steps": [
        {"id": "1", "type": 1, "name": "call by api_id",
         "api_call": {"api_id": str(api_echo["id"]),
                      "override": {"params": [{"key": "page", "value": "42"}]}}},
        {"id": "2", "type": 3, "name": "assert page",
         "assertion": {"assertions": [
             {"target": 4, "path": "$.query.page", "op": 1, "expected": "42"}]}},
    ]}}).json()
print(f"✓ api_echo={api_echo['id']} case6={case6['id']} (api_id inline at dispatch)")

# ---- v2：plan item param_overrides（模板变量最高优先级）----
case7 = api.post("/api/v1/cases", json={
    "project_id": pid, "name": "e2e-param-overrides", "type": 1,
    "definition": {"steps": [
        {"id": "1", "type": 1, "name": "templated call",
         "api_call": {"inline": {"method": 1, "uri": "/echo?n={{n}}"}}},
        {"id": "2", "type": 3, "name": "assert n from override",
         "assertion": {"assertions": [
             {"target": 4, "path": "$.query.n", "op": 1, "expected": "7"}]}},
    ]}}).json()
print(f"✓ case7={case7['id']} (param_overrides)")

# ---- v2：HEADER 类变量自动注入（默认 auth）----
# 环境已建 category=HEADER 的 token/greeting 变量 → 每个 api_call 自动携带
case11 = api.post("/api/v1/cases", json={
    "project_id": pid, "name": "e2e-auto-header", "type": 1,
    "definition": {"steps": [
        {"id": "1", "type": 1, "name": "echo with auto headers",
         "api_call": {"inline": {"method": 1, "uri": "/echo"}}},
        {"id": "2", "type": 3, "name": "assert injected headers",
         "assertion": {"assertions": [
             {"target": 4, "path": "$.headers.token", "op": 1, "expected": "t-123"},
             {"target": 4, "path": "$.headers.greeting", "op": 1, "expected": "hello"}]}},
    ]}}).json()
print(f"✓ case11={case11['id']} (HEADER 类变量自动注入)")

# ---- v2：gRPC 接口（GRPC_CALL 步骤 + server reflection；目标地址取环境 base_url）----
grpc_env = api.post("/api/v1/environments", json={
    "project_id": pid, "name": "grpc-target", "base_url": "127.0.0.1:19090"}).json()
grpc_api = api.post("/api/v1/grpc-apis", json={
    "project_id": pid, "full_service": "testpilot.echo.v1.EchoService", "method": "Echo",
    "request_message": {"message": "hi", "repeat": 2}, "deadline_ms": 5000}).json()
case8 = api.post("/api/v1/cases", json={
    "project_id": pid, "name": "e2e-grpc", "type": 1,
    "definition": {"steps": [
        {"id": "1", "type": 11, "name": "grpc echo",
         "grpc_call": {"grpc_api_id": str(grpc_api["id"])}},
        {"id": "2", "type": 3, "name": "assert msg",
         "assertion": {"assertions": [
             {"target": 4, "path": "$.message", "op": 1, "expected": "hihi"},
             {"target": 4, "path": "$.length", "op": 1, "expected": "4"}]}},
    ]}}).json()
grpc_plan = api.post("/api/v1/plans", json={
    "project_id": pid, "env_id": grpc_env["id"], "name": f"e2e-grpc-plan-{stamp}",
    "items": [{"ref_type": 1, "ref_id": case8["id"], "enabled": True, "order": 1}]}).json()
r = api.post(f"/api/v1/plans/{grpc_plan['id']}/run", json={})
assert r.status_code == 200, r.text
grpc_run_id = r.json()["run_id"]
deadline = time.time() + 30
grpc_run = None
while time.time() < deadline:
    grpc_run = api.get(f"/api/v1/runs/{grpc_run_id}").json()
    if grpc_run["status"] not in (0, 1):
        break
    time.sleep(1)
assert grpc_run["status"] == 2, json.dumps(grpc_run.get("summary"))
assert grpc_run["cases"][0]["status"] == 2, grpc_run["cases"]
print(f"✓ grpc run={grpc_run_id}（reflection + GRPC_CALL + JSONPATH 断言）")

# ---- v2：接口调试（POST /apis/debug，同步派发等待）----
r = api.post("/api/v1/apis/debug", json={
    "project_id": pid, "method": 1, "uri": "http://127.0.0.1:18080/json"})
assert r.status_code == 200, r.text
d = r.json()
assert d["status"] == 2 and d["step"]["status"] == 2, d
assert d["step"]["response"]["status"] == 200, d
print(f"✓ api debug: GET /json → 200（{d['duration_ms']}ms）")
# 失败路径：拒连 → step FAILED 但外层 200
r = api.post("/api/v1/apis/debug", json={
    "project_id": pid, "method": 1, "uri": "http://127.0.0.1:9/nope"})
assert r.status_code == 200 and r.json()["status"] == 3, r.text
print("✓ api debug: 拒连 → step FAILED（外层 200，错误进 body）")
# 调试 run 不进 runs 列表（plan_id=0 过滤），但可按 id 查看
dbg_run = api.get(f"/api/v1/runs/{d['run_id']}")
assert dbg_run.status_code == 200, dbg_run.text
runs = api.get("/api/v1/runs").json()["items"]
assert all(str(x["id"]) != str(d["run_id"]) for x in runs), "调试 run 不应出现在列表"
print("✓ api debug: 调试 run 落库可查且不污染列表")

# ---- 计划 + 触发 ----
plan = api.post("/api/v1/plans", json={
    "project_id": pid, "env_id": eid, "name": f"e2e-plan-{stamp}",
    "items": [
        {"ref_type": 1, "ref_id": case1["id"], "enabled": True, "order": 1},
        {"ref_type": 1, "ref_id": case2["id"], "enabled": True, "order": 2},
        {"ref_type": 1, "ref_id": case3["id"], "enabled": True, "order": 3},
        {"ref_type": 1, "ref_id": case4["id"], "enabled": True, "order": 4},
        {"ref_type": 2, "ref_id": suite1["id"], "enabled": True, "order": 5},
        {"ref_type": 1, "ref_id": case5["id"], "enabled": True, "order": 6},
        {"ref_type": 1, "ref_id": case6["id"], "enabled": True, "order": 7},
        {"ref_type": 1, "ref_id": case7["id"], "enabled": True, "order": 8,
         "param_overrides": {"n": 7}},
        {"ref_type": 1, "ref_id": case9["id"], "enabled": True, "order": 9},
        {"ref_type": 1, "ref_id": case11["id"], "enabled": True, "order": 10},
    ]}).json()
r = api.post(f"/api/v1/plans/{plan['id']}/run", json={})
if r.status_code != 200:
    print(f"✗ trigger failed: {r.text}")
    sys.exit(1)
run_id = r.json()["run_id"]
print(f"✓ plan={plan['id']} run={run_id}")

# ---- 轮询 ----
deadline = time.time() + args.timeout
run = None
while time.time() < deadline:
    run = api.get(f"/api/v1/runs/{run_id}").json()
    if run["status"] not in (0, 1):
        break
    time.sleep(1)
else:
    print("✗ run polling timeout")
    sys.exit(1)

# ---- 校验闭环 ----
ok = True
expect = {case1["name"]: 2, case2["name"]: 3, case3["name"]: 2,
          case4["name"]: 2, case5["name"]: 2, case6["name"]: 2,
          case7["name"]: 2, case9["name"]: 2,
          case11["name"]: 2}  # suite 展开使 case1/case3 各出现两次
summary = run.get("summary") or {}
print(f"run status={run['status']} summary={json.dumps(summary)}")
jr = api.get(f"/api/v1/runs/{run_id}/junit")
if jr.status_code != 200 or "<testsuites" not in jr.text or 'failures="1"' not in jr.text:
    print(f"✗ junit export failed: {jr.status_code}\n{jr.text[:300]}")
    sys.exit(1)
print("✓ junit export: testsuites with expected failures")
if run["status"] != 3:  # 一个 case 失败 → run FAILED
    print(f"✗ expect run FAILED(3), got {run['status']}")
    ok = False
for c in run["cases"]:
    name = c["case_name"]
    exp = expect.get(name)
    mark = "✓" if c["status"] == exp else "✗"
    if c["status"] != exp:
        ok = False
    print(f"{mark} case {name}: status={c['status']} steps={len(c['steps'])} err={c['error'][:80]}")
    for s in c["steps"]:
        print(f"    step {s['step_path']}: status={s['status']} {s['duration_ms']}ms")
        for a in (s.get("assertions") or []):
            print(f"      assert[{a['assertion']['target']}] pass={a['passed']} actual={a.get('actual','')[:50]} msg={a['message'][:60]}")
        for art in (s.get("artifacts") or []):
            print(f"      artifact kind={art['kind']} uri={art['uri']} size={art['size']}")
            # 产物可经 REST 取回
            rc = api.get(f"/api/v1/artifacts/{art['id']}/content")
            if rc.status_code != 200 or len(rc.content) == 0:
                print(f"      ✗ artifact {art['id']} fetch failed: {rc.status_code}")
                ok = False

# UI 用例应产出截图 + trace
ui_case = next((c for c in run["cases"] if c["case_name"] == case4["name"]), None)
if ui_case is not None:
    kinds = {a["kind"] for s in ui_case["steps"] for a in (s.get("artifacts") or [])}
    if 1 not in kinds or 3 not in kinds:
        print(f"✗ ui case artifacts missing screenshot/trace: kinds={kinds}")
        ok = False

# ---- v2：Postman Collection 导入导出 ----
pm_doc = {"info": {"name": "pm-demo", "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
          "item": [{"name": "pm ping", "request": {
              "method": "GET", "url": {"raw": "https://h.example/ping?x=1"}}}]}
r = api.post("/api/v1/import/postman", json={"project_id": pid, "document": pm_doc})
assert r.status_code == 200 and r.json()["created"] == 1, r.text
r = api.get("/api/v1/export/postman", params={"project_id": pid})
assert r.status_code == 200, r.text
col = r.json()
pm_ping = next((it for it in col["item"] if it["name"] == "GET /ping"), None)
assert pm_ping is not None and "x=1" in pm_ping["request"]["url"]["raw"], col
print("✓ postman import + export roundtrip")

# ---- v2：租户配置开关（tenant_settings，admin+）----
r = api.put("/api/v1/tenant/settings/e2e_flag", json={"value": "on"})
assert r.status_code == 200, r.text
settings = api.get("/api/v1/tenant/settings").json()
assert any(s["key"] == "e2e_flag" and s["value"] == "on" for s in settings["items"]), settings
r = api.put("/api/v1/tenant/settings/e2e_flag", json={"value": "off"})
assert r.status_code == 200 and r.json()["value"] == "off", r.text
assert api.delete("/api/v1/tenant/settings/e2e_flag").status_code == 200
print("✓ tenant settings CRUD（upsert/list/delete）")

# ---- 压测：对 /json 阶梯发压（Locust 子进程）----
print("— stress —")
api_json = api.post("/api/v1/apis", json={
    "project_id": pid, "method": 1, "uri": "/json"}).json()
splan = api.post("/api/v1/stress-plans", json={
    "project_id": pid, "env_id": eid, "target_type": 1, "target_id": api_json["id"],
    "load_profile": {
        "ramp": [{"at": "0s", "target": 2}, {"at": "5s", "target": 8}],
        "duration": "12s", "concurrency_per_worker": 8,
    },
    "worker_count": 1, "metrics_interval_ms": 1000}).json()
r = api.post(f"/api/v1/stress-plans/{splan['id']}/run", json={})
if r.status_code != 200:
    print(f"✗ stress trigger failed: {r.text}")
    ok = False
else:
    srun_id = r.json()["run_id"]
    print(f"✓ stress plan={splan['id']} run={srun_id}")
    deadline = time.time() + 60
    srun = None
    while time.time() < deadline:
        srun = api.get(f"/api/v1/stress-runs/{srun_id}").json()
        if srun["status"] not in (0, 1):
            break
        time.sleep(1.5)
    summary = srun.get("summary") or {}
    metrics = srun.get("metrics") or []
    print(f"stress status={srun['status']} summary={json.dumps(summary)} points={len(metrics)}")
    if srun["status"] != 2:
        print(f"✗ expect stress run PASSED(2), got {srun['status']}")
        ok = False
    if len(metrics) < 5:
        print(f"✗ expect >=5 metric points, got {len(metrics)}")
        ok = False
    else:
        avg_rps = sum(m["rps"] for m in metrics) / len(metrics)
        max_conc = max(m["concurrency"] for m in metrics)
        total_err = sum(m["error_rate"] for m in metrics)
        print(f"  avg_rps={avg_rps:.0f} max_concurrency={max_conc} err_sum={total_err:.3f}")
        if avg_rps < 10 or max_conc < 2:
            print("✗ stress metrics look wrong (rps/concurrency too low)")
            ok = False

# ---- v2：行为压测（target_type=2：沙箱常驻循环 + 能力桥）----
behavior_src = '''from testpilot_sdk import Context, assert_that


async def run(ctx: Context):
    resp = await ctx.http("GET", "/json")
    assert_that(resp.status, "status").eq(200)
'''
case10 = api.post("/api/v1/cases", json={
    "project_id": pid, "name": "e2e-behavior-load", "type": 2,
    "definition": {"source": behavior_src, "entry": "run"}}).json()
bsplan = api.post("/api/v1/stress-plans", json={
    "project_id": pid, "env_id": eid, "target_type": 2, "target_id": case10["id"],
    "load_profile": {
        "ramp": [{"at": "0s", "target": 2}, {"at": "2s", "target": 4}],
        "duration": "6s", "concurrency_per_worker": 4,
    },
    "worker_count": 1, "metrics_interval_ms": 1000}).json()
r = api.post(f"/api/v1/stress-plans/{bsplan['id']}/run", json={})
if r.status_code != 200:
    print(f"✗ behavior stress trigger failed: {r.text}")
    ok = False
else:
    bsrun_id = r.json()["run_id"]
    print(f"✓ behavior stress plan={bsplan['id']} run={bsrun_id}")
    deadline = time.time() + 60
    bsrun = None
    while time.time() < deadline:
        bsrun = api.get(f"/api/v1/stress-runs/{bsrun_id}").json()
        if bsrun["status"] not in (0, 1):
            break
        time.sleep(1.5)
    bmetrics = bsrun.get("metrics") or []
    bsummary = bsrun.get("summary") or {}
    print(f"behavior stress status={bsrun['status']} summary={json.dumps(bsummary)} points={len(bmetrics)}")
    if bsrun["status"] != 2:
        print(f"✗ expect behavior stress PASSED(2), got {bsrun['status']}")
        ok = False
    if len(bmetrics) < 3:
        print(f"✗ expect >=3 metric points, got {len(bmetrics)}")
        ok = False
    else:
        total_err = sum(m["error_rate"] for m in bmetrics)
        total_rps = sum(m["rps"] for m in bmetrics)
        print(f"  behavior: total_rps={total_rps:.0f} err_sum={total_err:.3f}")
        if total_rps <= 0 or total_err > 0:
            print("✗ behavior stress metrics wrong")
            ok = False

print("E2E " + ("PASSED" if ok else "FAILED"))
sys.exit(0 if ok else 1)
