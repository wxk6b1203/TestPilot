#!/usr/bin/env python3
"""Phase 6 Copilot 端到端验证（真实 DeepSeek 调用）。

前置：dev.sh 全栈已启动（scheduler :8080 / copilot :8100 / echo :18080）。
流程：登录 → 建项目/环境 → 对话创建接口（HITL 批准）→ 对话创建用例（HITL 批准）
     → 校验落库行 + 审计日志 + 会话消息持久化。

用法：worker/.venv/bin/python scripts/e2e_copilot.py
"""

from __future__ import annotations

import json
import sys
import time

import httpx

SCHED = "http://127.0.0.1:8080"
COPILOT = "http://127.0.0.1:8100"

api = httpx.Client(base_url=SCHED, timeout=15)
r = api.post("/api/v1/auth/login", json={"username": "admin", "password": "admin123"})
r.raise_for_status()
token = r.json()["token"]
api.headers["Authorization"] = f"Bearer {token}"
print("✓ login")

stamp = int(time.time())
proj = api.post("/api/v1/projects", json={"name": f"copilot-e2e-{stamp}", "description": "e2e"}).json()
pid = proj["id"]
env = api.post("/api/v1/environments", json={
    "project_id": pid, "name": "local", "base_url": "http://127.0.0.1:18080"}).json()
print(f"✓ project={pid} env={env['id']}")

CH = {"Authorization": f"Bearer {token}", "Content-Type": "application/json"}


def sse_events(resp: httpx.Response):
    """逐事件产出 Vercel SSE data JSON。"""
    for line in resp.iter_lines():
        if not line.startswith("data:"):
            continue
        data = line[5:].strip()
        if data == "[DONE]":
            return
        yield json.loads(data)


def chat_turn(messages: list[dict], session_id: str, approve_all: bool = True):
    """驱动一轮对话：自动批准所有 approval-request，直到模型收束。

    审批重发时事件合并回同一条 assistant 消息（与 Vercel useChat 行为一致）。
    返回 (final_text, session_id, assistant_message)。
    """
    assistant: dict | None = None
    sid = session_id
    for _round in range(6):
        headers = dict(CH)
        if sid:
            headers["X-Session-Id"] = sid
        body = {"trigger": "submit-message", "id": f"e2e-{stamp}", "messages": messages}
        with httpx.stream("POST", f"{COPILOT}/api/chat", headers=headers, json=body,
                          timeout=180) as resp:
            assert resp.status_code == 200, f"chat HTTP {resp.status_code}"
            sid = resp.headers.get("X-Session-Id") or sid

            if assistant is None:
                assistant = {"id": f"a-{stamp}", "role": "assistant", "parts": []}
                messages.append(assistant)
            parts: list[dict] = assistant["parts"]
            text_idx: int | None = None
            pending_approvals: list[str] = []
            for ev in sse_events(resp):
                t = ev.get("type")
                if t == "text-start":
                    parts.append({"type": "text", "text": ""})
                    text_idx = len(parts) - 1
                elif t == "text-delta":
                    if text_idx is None:
                        parts.append({"type": "text", "text": ""})
                        text_idx = len(parts) - 1
                    parts[text_idx]["text"] += ev["delta"]
                elif t == "text-end":
                    text_idx = None
                elif t == "tool-input-start":
                    p = _tool_part(parts, ev["toolCallId"])
                    p.update(type=f"tool-{ev['toolName']}", state="input-streaming")
                elif t == "tool-input-available":
                    p = _tool_part(parts, ev["toolCallId"])
                    p.update(state="input-available", input=ev.get("input"))
                elif t == "tool-approval-request":
                    p = _tool_part(parts, ev["toolCallId"])
                    p.update(state="approval-requested", approval={"id": ev["approvalId"]})
                    pending_approvals.append(ev["toolCallId"])
                elif t == "tool-output-available":
                    p = _tool_part(parts, ev["toolCallId"])
                    p.update(state="output-available", output=ev.get("output"))
                elif t == "tool-output-error":
                    p = _tool_part(parts, ev["toolCallId"])
                    p.update(state="output-error", errorText=ev.get("errorText"))
                elif t == "error":
                    raise AssertionError(f"stream error: {ev.get('errorText')}")

        if not pending_approvals:
            final = "".join(p.get("text", "") for p in parts if p["type"] == "text")
            return final, sid, assistant
        # 批准（或拒绝）后整体重发：part 就地标记 approval-responded
        for p in parts:
            if p.get("state") == "approval-requested":
                p["state"] = "approval-responded"
                p["approval"]["approved"] = approve_all
    raise AssertionError("approval loop did not converge")


def _tool_part(parts: list[dict], call_id: str) -> dict:
    for p in reversed(parts):
        if p.get("toolCallId") == call_id:
            return p
    # 容错：部分事件可能先于 tool-input-start 到达
    parts.append({"type": "tool-unknown", "toolCallId": call_id, "state": "input-streaming"})
    return parts[-1]


session_id = ""

# ---- 对话 1：创建接口（HITL 批准）----
messages = [{"id": "u1", "role": "user", "parts": [{"type": "text", "text":
    f"在项目 {pid} 中创建一个接口：GET /json，描述 echo json。不要追问，直接发起创建。"}]}]
final, session_id, _ = chat_turn(messages, session_id)
print(f"✓ turn1 done: {final[:80]}...")

apis = api.get(f"/api/v1/apis?project_id={pid}").json()["items"]
hit = [a for a in apis if a["uri"] == "/json"]
assert hit, f"接口未落库: {apis}"
api_id = hit[0]["id"]
print(f"✓ api created via HITL: {api_id}")

# ---- 对话 2：创建声明式用例（HITL 批准）----
messages.append({"id": "u2", "role": "user", "parts": [{"type": "text", "text":
    f"在项目 {pid} 中创建声明式测试用例「json-冒烟」：调用接口 {api_id}（GET /json），"
    "断言响应状态码等于 200。先 query_schema 确认结构，然后直接发起创建，不要追问。"}]})
final, session_id, _ = chat_turn(messages, session_id)
print(f"✓ turn2 done: {final[:80]}...")

cases = api.get(f"/api/v1/cases?project_id={pid}").json()["items"]
hit = [c for c in cases if "冒烟" in c["name"]]
assert hit, f"用例未落库: {[c['name'] for c in cases]}"
case_id = hit[0]["id"]
detail = api.get(f"/api/v1/cases/{case_id}").json()
steps = (detail.get("definition") or {}).get("steps", [])
assert steps, "用例无步骤"
print(f"✓ case created via HITL: {case_id} steps={len(steps)}")

# ---- 审计：两条 copilot 写操作 ----
logs = api.get("/api/v1/audit-logs?page_size=20").json()["items"]
mine = [l for l in logs if l.get("actor") == 2 and l.get("resource_id") in (api_id, case_id)]
assert len(mine) >= 2, f"审计缺失: {mine}"
assert all(l.get("approved_by") for l in mine), "审计缺 approved_by"
print(f"✓ audit: {[(l['action'], l['resource_type']) for l in mine]}")

# ---- 会话持久化：用户消息两条、无重复 ----
msgs = api.get(f"/api/v1/copilot/sessions/{session_id}/messages").json()["items"]
user_rows = [m for m in msgs if m["role"] == 1]
assert len(user_rows) == 2, f"用户消息持久化异常: {len(user_rows)}"
assert any(m["role"] == 3 for m in msgs), "工具结果行缺失"
print(f"✓ session persisted: {len(msgs)} rows ({len(user_rows)} user)")

print("\nCOPILOT E2E PASSED")
